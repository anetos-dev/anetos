// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"math"
	"slices"
	"strconv"
	"sync"
	"testing"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

func init() {
	extra = append(extra, test{"VectorDetails", testVectorDetails})
	migrationTables = append(migrationTables, "st_v2_items_embeddings", "st_v2_items", "st_v2_owners")
}

// stVItem is a record owned by a row of st_v2_owners, deleted with it.
type stVItem struct {
	db.Model
	OwnerID int64  `db:"owner_id"`
	Name    string `db:"name"`
}

// TableName implements db.Tabler.
func (stVItem) TableName() string { return "st_v2_items" }

// testVectorDetails checks what a few rows don't show: binary vectors
// that look like text, more candidates than an index returns by default,
// ties, concurrent replacements, and records deleted by another table's
// cascade.
func testVectorDetails(t *testing.T, ctx context.Context) {
	if ok, err := d(ctx).Supports(ctx, db.VectorSearch); err != nil || !ok {
		check(t, err)
		return
	}
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	drop := func() {
		_ = s.DropEmbeddings("st_v2_items")
		_ = s.DropIfExists("st_v2_items")
		_ = s.DropIfExists("st_v2_owners")
	}
	drop()
	t.Cleanup(drop)
	check(t, s.Create("st_v2_owners", func(t *migrate.Table) { t.ID() }))
	check(t, s.Create("st_v2_items", func(t *migrate.Table) {
		t.ID()
		t.ForeignID("owner_id").References("st_v2_owners").CascadeOnDelete()
		t.String("name", 100)
		t.Timestamps()
	}))
	check(t, s.CreateEmbeddings("st_v2_items", 3))
	_, err = db.Exec(ctx, "INSERT INTO st_v2_owners (id) VALUES (1), (2)")
	check(t, err)
	items := make([]stVItem, 120)
	for i := range items {
		items[i] = stVItem{OwnerID: int64(1 + i%2), Name: strconv.Itoa(i)}
	}
	check(t, db.CreateMany(ctx, items))
	all, err := db.Query[stVItem](ctx).OrderBy(db.Col[int64]("id").Asc()).Get()
	check(t, err)

	// A binary vector whose first byte is '[' (MariaDB, SQLite) is still
	// binary.
	odd := db.Vector{math.Float32frombits(0x3F80005B), 0.5, 0.25}
	check(t, db.ReplaceChunks[stVItem](ctx, all[0].ID, []db.Chunk{{Content: "odd", ContentHash: "o", Model: "m", Embedding: odd}}))
	if cs, err := db.Chunks[stVItem](ctx, all[0].ID); err != nil || len(cs) != 1 || !slices.Equal(cs[0].Embedding, odd) {
		t.Fatalf("a vector starting with '[': %+v, %v", cs, err)
	}

	// More candidates than pgvector's HNSW returns by default (40), with
	// another model's chunks, nearer, filtered out: every record is found.
	for i, it := range all[1:] {
		check(t, db.ReplaceChunks[stVItem](ctx, it.ID, []db.Chunk{
			{Content: "a", ContentHash: "a", Model: "m", Embedding: db.Vector{1, float32(i) / 200, 0}},
			{Content: "b", ContentHash: "b", Model: "noise", Embedding: db.Vector{1, 0, 0}},
		}))
	}
	count := func(ctx context.Context) int64 {
		n, err := db.Query[stVItem](ctx).Similar("m", db.Vector{1, 0, 0}).Count()
		check(t, err)
		return n
	}
	if d(ctx).Dialect().Name() == "postgres" {
		check(t, db.Tx(ctx, func(ctx context.Context) error {
			if _, err := db.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil { // the index, as on a big table
				return err
			}
			// HNSW is approximate: one or two may be missed, not 80.
			if n := count(ctx); n < 110 {
				t.Errorf("Similar through the HNSW index found %d records of 120", n)
			}
			return nil
		}))
	} else if n := count(ctx); n != 120 {
		t.Errorf("Similar found %d records, not 120", n)
	}

	// Ties are ordered by primary key: pages neither repeat nor skip.
	tied := all[1:6]
	for _, it := range tied {
		check(t, db.ReplaceChunks[stVItem](ctx, it.ID, []db.Chunk{
			{Content: "a", ContentHash: "a", Model: "m", Embedding: db.Vector{1, 0, 0}},
			{Content: "t", ContentHash: "t", Model: "tie", Embedding: db.Vector{0, 0, 1}},
		}))
	}
	var seen []int64
	for page := 1; page <= 3; page++ {
		p, err := db.Query[stVItem](ctx).Similar("tie", db.Vector{0, 0, 1}).Paginate(page, 2)
		check(t, err)
		for _, it := range p.Data {
			seen = append(seen, it.ID)
		}
	}
	if want := []int64{tied[0].ID, tied[1].ID, tied[2].ID, tied[3].ID, tied[4].ID}; !slices.Equal(seen, want) {
		t.Errorf("tied pages: %v, want %v", seen, want)
	}

	// Concurrent replacements of one record's chunks take turns.
	if d(ctx).Dialect().Name() != "sqlite" {
		var wg sync.WaitGroup
		errs := make(chan error, 10)
		for i := range 10 {
			wg.Go(func() {
				errs <- db.ReplaceChunks[stVItem](ctx, all[7].ID, []db.Chunk{
					{Content: "x", ContentHash: strconv.Itoa(i), Model: "m", Embedding: db.Vector{1, 0, 0}},
					{Content: "y", ContentHash: "y", Model: "m", Embedding: db.Vector{0, 1, 0}},
				})
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("concurrent ReplaceChunks: %v", err)
			}
		}
		if cs, _ := db.Chunks[stVItem](ctx, all[7].ID); len(cs) != 2 {
			t.Errorf("after concurrent ReplaceChunks: %d chunks", len(cs))
		}
	}

	// Records deleted by another table's cascade are never found; their
	// chunks go with them, or (MariaDB) with PruneChunks.
	_, err = db.Exec(ctx, "DELETE FROM st_v2_owners WHERE id = 2")
	check(t, err)
	if n := count(ctx); n != 60 {
		t.Errorf("after the cascade, Similar found %d records, not 60", n)
	}
	pruned, err := db.PruneChunks[stVItem](ctx)
	check(t, err)
	type countRow struct {
		N int64 `db:"n"`
	}
	rows, err := db.Raw[countRow](ctx, "SELECT COUNT(*) AS n FROM st_v2_items_embeddings WHERE record_id NOT IN (SELECT id FROM st_v2_items)")
	check(t, err)
	if orphans := rows[0].N; orphans != 0 {
		t.Errorf("%d chunks of deleted records after PruneChunks (pruned %d)", orphans, pruned)
	}
	if d(ctx).Dialect().Name() != "mysql" && pruned != 0 {
		t.Errorf("PruneChunks deleted %d chunks, which the foreign key had", pruned)
	}
	// More chunks of deleted records than candidates don't hide the live
	// ones, pruned or not.
	_, err = db.Exec(ctx, "INSERT INTO st_v2_owners (id) VALUES (3)")
	check(t, err)
	crowd := make([]stVItem, 210)
	for i := range crowd {
		crowd[i] = stVItem{OwnerID: 3, Name: "crowd"}
	}
	check(t, db.CreateMany(ctx, crowd))
	crowd, err = db.Query[stVItem](ctx).Where(db.Col[int64]("owner_id").Eq(3)).Get()
	check(t, err)
	for _, it := range append(crowd, all[0], all[2], all[4]) {
		v := db.Vector{0, 1, 0}
		if it.OwnerID != 3 {
			v = db.Vector{0, 1, 1} // farther
		}
		check(t, db.ReplaceChunks[stVItem](ctx, it.ID, []db.Chunk{{Content: "c", ContentHash: "c", Model: "crowd", Embedding: v}}))
	}
	_, err = db.Exec(ctx, "DELETE FROM st_v2_owners WHERE id = 3")
	check(t, err)
	if n, err := db.Query[stVItem](ctx).Similar("crowd", db.Vector{0, 1, 0}).Count(); err != nil || n != 3 {
		t.Errorf("behind 210 deleted records' chunks: %d records, %v", n, err)
	}

	// Text the database returns as bytes is text.
	if vs, err := db.Raw[db.Vector](ctx, "SELECT '[1,2.5,-3]'"); err != nil || len(vs) != 1 || !slices.Equal(vs[0], db.Vector{1, 2.5, -3}) {
		t.Errorf("a vector's text: %v, %v", vs, err)
	}

	// A replacement for a deleted record stores nothing.
	check(t, db.ReplaceChunks[stVItem](ctx, all[1].ID, []db.Chunk{{Content: "z", ContentHash: "z", Model: "m", Embedding: db.Vector{1, 0, 0}}}))
	if cs, _ := db.Chunks[stVItem](ctx, all[1].ID); len(cs) != 0 {
		t.Errorf("chunks of a deleted record: %+v", cs)
	}
}
