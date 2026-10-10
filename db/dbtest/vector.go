// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/internal/dbutil"
)

func init() {
	extra = append(extra, test{"Vectors", testVectors})
	migrationTables = append(migrationTables, "st_v_docs_embeddings", "st_v_docs")
}

// stDoc is a model with embeddings.
type stDoc struct {
	db.Model
	Title     string `db:"title"`
	Body      string `db:"body"`
	Published bool   `db:"published"`
}

// TableName implements db.Tabler.
func (stDoc) TableName() string { return "st_v_docs" }

// stChunk is a row of st_v_docs_embeddings.
type stChunk struct {
	db.Model
	RecordID    int64     `db:"record_id"`
	Chunk       int       `db:"chunk"`
	Content     string    `db:"content"`
	ContentHash string    `db:"content_hash"`
	EmbedModel  string    `db:"model"`
	Embedding   db.Vector `db:"embedding"`
}

// TableName implements db.Tabler.
func (stChunk) TableName() string { return "st_v_docs_embeddings" }

func testVectors(t *testing.T, ctx context.Context) {
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	_ = s.DropEmbeddings("st_v_docs")
	_ = s.DropIfExists("st_v_docs")
	t.Cleanup(func() {
		_ = s.DropEmbeddings("st_v_docs")
		_ = s.DropIfExists("st_v_docs")
		_, _ = db.Exec(context.WithoutCancel(ctx), "DELETE FROM "+dbutil.SearchIndexesTable+" WHERE table_name = 'st_v_docs'")
	})
	check(t, s.Create("st_v_docs", func(t *migrate.Table) {
		t.ID()
		t.String("title", 200)
		t.Text("body")
		t.Boolean("published").Default(true)
		t.Timestamps()
		t.SearchIndex("title", "body")
	}))
	ok, err := d(ctx).Supports(ctx, db.VectorSearch)
	check(t, err)
	if !ok {
		err := s.CreateEmbeddings("st_v_docs", 3)
		if err == nil || !strings.Contains(err.Error(), "vector search") {
			t.Errorf("CreateEmbeddings without vector search: %v", err)
		}
		if _, err := db.Query[stDoc](ctx).Similar("m", db.Vector{1, 0, 0}).Get(); err == nil {
			t.Error("Similar without vector search")
		}
		return
	}
	check(t, s.CreateEmbeddings("st_v_docs", 3))

	docs := []stDoc{
		{Title: "Kittens", Body: "small cats", Published: true},
		{Title: "Rockets", Body: "going to orbit", Published: true},
		{Title: "Rocket cats", Body: "cats in space", Published: false},
		{Title: "Gardens", Body: "growing tomatoes", Published: true},
	}
	check(t, db.CreateMany(ctx, docs))
	all, err := db.Query[stDoc](ctx).OrderBy(db.Col[int64]("id").Asc()).Get()
	check(t, err)
	id := func(title string) int64 {
		for _, d := range all {
			if d.Title == title {
				return d.ID
			}
		}
		t.Fatalf("no doc %q", title)
		return 0
	}
	chunk := func(title string, n int, model string, v db.Vector) stChunk {
		return stChunk{RecordID: id(title), Chunk: n, Content: title, ContentHash: "h", EmbedModel: model, Embedding: v}
	}
	check(t, db.CreateMany(ctx, []stChunk{
		chunk("Kittens", 0, "m1", db.Vector{1, 0, 0}),
		chunk("Rockets", 0, "m1", db.Vector{0, 1, 0}),
		chunk("Rocket cats", 0, "m1", db.Vector{0.7, 0.7, 0}),
		chunk("Rocket cats", 1, "m1", db.Vector{0, 0.2, 1}),
		chunk("Gardens", 0, "m1", db.Vector{0, 0, 1}),
		chunk("Gardens", 1, "m2", db.Vector{1, 0, 0}), // another model's: ignored
	}))

	// Vectors round-trip.
	got, err := db.Query[stChunk](ctx).Where(db.Col[int]("chunk").Eq(1), db.Col[string]("model").Eq("m1")).First()
	check(t, err)
	if want := (db.Vector{0, 0.2, 1}); !slices.EqualFunc(got.Embedding, want, func(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-6 }) {
		t.Errorf("stored vector %v, want %v", got.Embedding, want)
	}

	titles := func(q *db.Q[stDoc]) []string {
		t.Helper()
		rows, err := q.Get()
		check(t, err)
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.Title
		}
		return out
	}
	// Nearest first, by each record's nearest chunk, among model m1's.
	if got, want := titles(db.Query[stDoc](ctx).Similar("m1", db.Vector{0.9, 0.1, 0})), []string{"Kittens", "Rocket cats", "Rockets", "Gardens"}; !slices.Equal(got, want) {
		t.Errorf("Similar = %v, want %v", got, want)
	}
	if got, want := titles(db.Query[stDoc](ctx).Similar("m1", db.Vector{0, 0, 1}).Limit(2)), []string{"Gardens", "Rocket cats"}; !slices.Equal(got, want) {
		t.Errorf("Similar near the third axis = %v, want %v", got, want)
	}
	// Conditions and counts apply to the records.
	pub := db.Col[bool]("published").Eq(true)
	if got, want := titles(db.Query[stDoc](ctx).Where(pub).Similar("m1", db.Vector{0.7, 0.7, 0})), []string{"Kittens", "Rockets", "Gardens"}; !slices.Equal(got[:2], want[:2]) && !slices.Equal(got[:2], []string{"Rockets", "Kittens"}) || len(got) != 3 {
		t.Errorf("Similar with Where = %v", got)
	}
	if n, err := db.Query[stDoc](ctx).Where(pub).Similar("m1", db.Vector{1, 0, 0}).Count(); err != nil || n != 3 {
		t.Errorf("Count = %d, %v", n, err)
	}
	page, err := db.Query[stDoc](ctx).Similar("m1", db.Vector{1, 0, 0}).Paginate(1, 2)
	check(t, err)
	if page.Total != 4 || len(page.Data) != 2 || page.Data[0].Title != "Kittens" {
		t.Errorf("Paginate = %+v", page)
	}
	// Model m2 knows only Gardens.
	if got := titles(db.Query[stDoc](ctx).Similar("m2", db.Vector{1, 0, 0})); !slices.Equal(got, []string{"Gardens"}) {
		t.Errorf("Similar for m2 = %v", got)
	}

	// Hybrid: "rocket" finds Rockets and Rocket cats by their words; the
	// vector is nearest Rocket cats, then Kittens, then Rockets. Rocket
	// cats, high in both, comes first.
	got2 := titles(db.Query[stDoc](ctx).Hybrid("rocket", "m1", db.Vector{0.75, 0.65, 0}))
	if len(got2) != 4 || got2[0] != "Rocket cats" {
		t.Errorf("Hybrid = %v", got2)
	}
	if got := titles(db.Query[stDoc](ctx).Where(pub).Hybrid("rocket", "m1", db.Vector{0.75, 0.65, 0}).Limit(2)); len(got) != 2 || slices.Contains(got, "Rocket cats") {
		t.Errorf("Hybrid with Where = %v", got)
	}
	if n, err := db.Query[stDoc](ctx).Hybrid("rocket", "m1", db.Vector{0.75, 0.65, 0}).Count(); err != nil || n != 4 {
		t.Errorf("Hybrid Count = %d, %v", n, err)
	}
	// Words that are no words: Similar.
	if got := titles(db.Query[stDoc](ctx).Hybrid("!!", "m1", db.Vector{1, 0, 0}).Limit(1)); !slices.Equal(got, []string{"Kittens"}) {
		t.Errorf("Hybrid without words = %v", got)
	}

	// Similar replaces Search and the other way round; writes refuse it.
	if got := titles(db.Query[stDoc](ctx).Search("gardens").Similar("m1", db.Vector{1, 0, 0}).Limit(1)); !slices.Equal(got, []string{"Kittens"}) {
		t.Errorf("Search then Similar = %v", got)
	}
	if got := titles(db.Query[stDoc](ctx).Similar("m1", db.Vector{1, 0, 0}).Search("gardens")); !slices.Equal(got, []string{"Gardens"}) {
		t.Errorf("Similar then Search = %v", got)
	}
	if _, err := db.Query[stDoc](ctx).Similar("m1", db.Vector{1, 0, 0}).Delete(); err == nil {
		t.Error("Delete with Similar")
	}
	if _, err := db.Query[stDoc](ctx).Similar("m1", db.Vector{1, 0, 0}).CursorPaginate("", 2); err == nil {
		t.Error("CursorPaginate with Similar")
	}
	if _, err := db.Query[stDoc](ctx).Similar("m1", nil).Get(); err == nil {
		t.Error("Similar without a vector")
	}

	// Chunks: read, replaced, and the nearest of some records.
	cs, err := db.Chunks[stDoc](ctx, id("Rocket cats"), id("Gardens"))
	check(t, err)
	if len(cs) != 4 || cs[0].RecordID != id("Rocket cats") || cs[1].Position != 1 || len(cs[3].Embedding) != 3 {
		t.Errorf("Chunks = %+v", cs)
	}
	check(t, db.ReplaceChunks[stDoc](ctx, id("Gardens"), []db.Chunk{
		{Content: "growing", ContentHash: "g", EmbeddingModel: "m1", Embedding: db.Vector{0, 0, 1}},
	}))
	if cs, err := db.Chunks[stDoc](ctx, id("Gardens")); err != nil || len(cs) != 1 || cs[0].Content != "growing" || cs[0].Position != 0 {
		t.Errorf("after ReplaceChunks: %+v, %v", cs, err)
	}
	near, err := db.NearestChunks[stDoc](ctx, "m1", db.Vector{0, 0.2, 1}, id("Rocket cats"), id("Gardens"))
	check(t, err)
	if len(near) != 3 || near[0].RecordID != id("Rocket cats") || near[0].Position != 1 || near[0].Distance > 1e-5 || near[2].Distance < near[1].Distance {
		t.Errorf("NearestChunks = %+v", near)
	}
	if near, err := db.NearestChunks[stDoc](ctx, "m1", db.Vector{1, 0, 0}); err != nil || len(near) != 0 {
		t.Errorf("NearestChunks of no records: %v, %v", near, err)
	}

	// A record's chunks go with it.
	victim := all[0]
	check(t, db.ForceDelete(ctx, &victim))
	if n, err := db.Query[stChunk](ctx).Where(db.Col[int64]("record_id").Eq(victim.ID)).Count(); err != nil || n != 0 {
		t.Errorf("chunks of a deleted record: %d, %v", n, err)
	}

	// Vectors of another size are an error, not a wrong answer.
	if _, err := db.Query[stDoc](ctx).Similar("m1", db.Vector{1, 0}).Get(); err == nil {
		t.Error("a vector of another size")
	} else if errors.Is(err, context.Canceled) {
		t.Error(err)
	}
}
