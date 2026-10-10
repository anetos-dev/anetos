// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/internal/dbutil"
	"anetos.dev/anetos/queue"
)

func init() {
	extra = append(extra, test{"Embeddings", testEmbeddings})
	migrationTables = append(migrationTables, "st_e_notes_embeddings", "st_e_notes")
}

// stENote is a model whose records ai.Embeddings keeps.
type stENote struct {
	db.Model
	Title  string `db:"title"`
	Body   string `db:"body"`
	Public bool   `db:"public"`
}

// TableName implements db.Tabler.
func (stENote) TableName() string { return "st_e_notes" }

// passages summarizes search results for test failures.
func passages(found []ai.Passage[stENote]) string {
	var b strings.Builder
	for _, p := range found {
		fmt.Fprintf(&b, "[%s #%d %.3f %q] ", p.Record.Title, p.Position, p.Distance, p.Text)
	}
	return b.String()
}

// testEmbeddings syncs and searches records' embeddings with ai.Embeddings
// and the fake embedder.
func testEmbeddings(t *testing.T, ctx context.Context) {
	if ok, err := d(ctx).Supports(ctx, db.VectorSearch); err != nil || !ok {
		check(t, err)
		return // Vectors checks the refusal
	}
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	drop := func() {
		ctx := context.WithoutCancel(ctx)
		_ = s.DropEmbeddings("st_e_notes")
		_ = s.DropIfExists("st_e_notes")
		_, _ = db.Exec(ctx, "DELETE FROM "+dbutil.SearchIndexesTable+" WHERE table_name = 'st_e_notes'")
	}
	drop()
	t.Cleanup(drop)
	check(t, s.Create("st_e_notes", func(t *migrate.Table) {
		t.ID()
		t.String("title", 200)
		t.Text("body")
		t.Boolean("public").Default(true)
		t.Timestamps()
	}))
	check(t, s.CreateEmbeddings("st_e_notes", 32))

	for _, queued := range []bool{true, false} {
		app, err := anetos.New(anetos.WithSource(config.Map{"AI_PROVIDER": "fake", "APP_KEY": encryption.GenerateKey()}), anetos.WithLogOutput(io.Discard))
		check(t, err)
		var jobs int // ai.embed jobs dispatched
		if queued {
			q, err := queue.New(app) // sync: jobs run when dispatched
			check(t, err)
			q.Observe(func(_ context.Context, d queue.Dispatched) {
				if d.Job == "ai.embed:st_e_notes" {
					jobs++
				}
			})
		}
		client, err := ai.New(app)
		check(t, err)
		if _, err := ai.EmbeddingsFor(app, ai.EmbeddingsConfig[stENote]{}); err == nil {
			t.Error("EmbeddingsFor without Text")
		}
		notes, err := ai.EmbeddingsFor(app, ai.EmbeddingsConfig[stENote]{
			Text:       func(n stENote) string { return n.Title + "\n\n" + n.Body },
			Title:      func(n stENote) string { return n.Title },
			Dimensions: 32,
			ChunkSize:  60,
			Scope: func(_ context.Context, q *db.Q[stENote]) *db.Q[stENote] {
				return q.Where(db.Col[bool]("public").Eq(true))
			},
		})
		check(t, err)
		if _, err := ai.EmbeddingsFor(app, ai.EmbeddingsConfig[stENote]{Text: func(stENote) string { return "" }, Dimensions: 32}); err == nil {
			t.Error("EmbeddingsFor twice for a table")
		}
		ctx := app.Context(ctx)
		fake := client.Fake()
		_, err = db.Exec(ctx, "DELETE FROM st_e_notes")
		check(t, err)

		rows := []stENote{
			{Title: "Sleepy cats", Body: "Cats sleep most of the day, in the sun.", Public: true},
			{Title: "Rockets", Body: "Rockets go to orbit, high above the clouds. Satellites go around the earth up there.", Public: true},
			{Title: "Secret cats", Body: "Cats plotting in the dark.", Public: false},
			{Title: "Tomatoes", Body: "Growing tomatoes needs sun and water.", Public: true},
		}
		check(t, db.CreateMany(ctx, rows))
		all, err := db.Query[stENote](ctx).OrderBy(db.Col[int64]("id").Asc()).Get()
		check(t, err)
		check(t, db.Tx(ctx, func(ctx context.Context) error { return notes.Sync(ctx, all...) }))
		chunks, err := db.Chunks[stENote](ctx)
		check(t, err)
		// The rocket note's text is longer than a chunk.
		if len(chunks) != 5 || chunks[0].EmbeddingModel != "fake-embedding" || len(chunks[0].Embedding) != 32 || len(chunks[0].ContentHash) != 64 {
			t.Fatalf("queued %v: chunks %+v", queued, chunks)
		}
		embedded := len(fake.EmbedRequests())

		// Unchanged records aren't embedded again; changed chunks only are.
		check(t, notes.SyncNow(ctx, all...))
		if len(fake.EmbedRequests()) != embedded {
			t.Error("unchanged records were embedded again")
		}
		all[1].Body = "Rockets go to orbit, high above the clouds. The moon goes around the earth up there."
		check(t, db.Save(ctx, &all[1]))
		check(t, notes.Sync(ctx, all[1]))
		if reqs := fake.EmbedRequests(); len(reqs) != embedded+1 || len(reqs[embedded].Inputs) != 1 || !strings.Contains(reqs[embedded].Inputs[0], "moon") {
			t.Errorf("re-embedded: %+v", reqs[embedded:])
		}

		// Search: by meaning, with the passages, within the scope.
		found, err := notes.Search(ctx, "where do cats sleep", 3)
		check(t, err)
		if len(found) != 3 || found[0].Record.Title != "Sleepy cats" || found[0].Position != 0 || found[0].Distance <= 0 || found[0].Distance >= 1 ||
			slices.ContainsFunc(found, func(p ai.Passage[stENote]) bool { return !p.Record.Public }) {
			t.Errorf("Search: %s", passages(found))
		}
		found, err = notes.Search(ctx, "does the moon go around the earth", 1)
		check(t, err)
		if len(found) != 1 || found[0].Record.Title != "Rockets" || found[0].Position != 1 || !strings.Contains(found[0].Text, "moon") {
			t.Errorf("Search's passage: %s", passages(found))
		}
		found, err = notes.Search(ctx, "cats", 0, func(q *db.Q[stENote]) *db.Q[stENote] {
			return q.Where(db.Col[string]("title").Ne("Sleepy cats"))
		})
		check(t, err)
		if len(found) != 2 || slices.ContainsFunc(found, func(p ai.Passage[stENote]) bool { return p.Record.Title == "Sleepy cats" }) {
			t.Errorf("Search with a scope: %s", passages(found))
		}

		// The tool, for agents.
		tool := notes.Tool("search_notes", "Search the notes", 2)
		out, err := tool.Call(ctx, json.RawMessage(`{"query":"tomatoes need water"}`))
		check(t, err)
		var results []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
			Text  string `json:"text"`
		}
		check(t, json.Unmarshal([]byte(out), &results))
		if len(results) != 2 || results[0].Title != "Tomatoes" || results[0].ID != all[3].ID || !strings.Contains(results[0].Text, "water") {
			t.Errorf("tool: %s", out)
		}
		if _, err := tool.Call(ctx, json.RawMessage(`{}`)); err == nil {
			t.Error("the tool without a query")
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Error("a tool with a negative limit")
				}
			}()
			notes.Tool("search_notes", "Search the notes", -1)
		}()

		// A new model: searches see only its chunks, until ai:embed.
		client.SetEmbedder(fake, "fake-2")
		if found, err := notes.Search(ctx, "cats", 5); err != nil || len(found) != 0 {
			t.Errorf("before ai:embed: %s, %v", passages(found), err)
		}
		var outb bytes.Buffer
		run := func(args ...string) error {
			for _, c := range app.Commands() {
				if c.Name == "ai:embed" {
					return c.Run(ctx, &cmd.Args{Name: c.Name, Args: args, Stdout: &outb, Stderr: &outb})
				}
			}
			t.Fatal("no ai:embed command")
			return nil
		}
		check(t, run())
		if !strings.Contains(outb.String(), "st_e_notes: 4 records synced.") {
			t.Errorf("ai:embed said %q", outb.String())
		}
		if err := run("nope"); err == nil || !strings.Contains(err.Error(), "the tables are [st_e_notes]") {
			t.Errorf("ai:embed nope: %v", err)
		}
		if found, err := notes.Search(ctx, "cats", 5); err != nil || len(found) != 3 {
			t.Errorf("after ai:embed: %s, %v", passages(found), err)
		}
		if cs, _ := db.Chunks[stENote](ctx); len(cs) != 5 || cs[0].EmbeddingModel != "fake-2" {
			t.Errorf("chunks after ai:embed: %+v", cs)
		}

		// Many records go in jobs of a hundred.
		if queued {
			many := make([]stENote, 210)
			for i := range many {
				many[i] = stENote{Title: "Bulk " + strconv.Itoa(i), Body: "bulk", Public: false}
			}
			check(t, db.CreateMany(ctx, many))
			many, err = db.Query[stENote](ctx).Where(db.Col[string]("body").Eq("bulk")).OrderBy(db.Col[int64]("id").Asc()).Get()
			check(t, err)
			jobs = 0
			check(t, notes.Sync(ctx, many...))
			if jobs != 3 {
				t.Errorf("Sync of 210 records dispatched %d jobs", jobs)
			}
			if cs, _ := db.Chunks[stENote](ctx, many[209].ID); len(cs) != 1 {
				t.Errorf("the last bulk record's chunks: %+v", cs)
			}
			_, err = db.Query[stENote](ctx).Where(db.Col[string]("body").Eq("bulk")).ForceDelete()
			check(t, err)
		}

		// A blank query finds nothing, without asking the model.
		before := len(fake.EmbedRequests())
		if found, err := notes.Search(ctx, "  ", 5); err != nil || len(found) != 0 || len(fake.EmbedRequests()) != before {
			t.Errorf("a blank Search: %s, %v", passages(found), err)
		}

		// A record whose text is empty has no chunks; a deleted one's go.
		all[0].Title, all[0].Body = "", " "
		check(t, db.Save(ctx, &all[0]))
		check(t, notes.SyncNow(ctx, all[0]))
		check(t, db.ForceDelete(ctx, &all[2]))
		if cs, _ := db.Chunks[stENote](ctx, all[0].ID, all[2].ID); len(cs) != 0 {
			t.Errorf("chunks of an empty and a deleted record: %+v", cs)
		}
		_ = app.Close()
	}

	// Hybrid, with a search index: a rare word finds its record first.
	base := ctx
	app, err := anetos.New(anetos.WithSource(config.Map{"AI_PROVIDER": "fake", "APP_KEY": encryption.GenerateKey()}), anetos.WithLogOutput(io.Discard))
	check(t, err)
	defer func() { _ = app.Close() }()
	_, err = ai.New(app)
	check(t, err)
	notes, err := ai.EmbeddingsFor(app, ai.EmbeddingsConfig[stENote]{Text: func(n stENote) string { return n.Body }, Dimensions: 32})
	check(t, err)
	ctx = app.Context(ctx)
	_, err = db.Exec(ctx, "DELETE FROM st_e_notes")
	check(t, err)
	check(t, s.Alter("st_e_notes", func(t *migrate.Table) { t.SearchIndex("title", "body") }))
	rows := []stENote{
		{Title: "Zanzibar spice guide", Body: "", Public: true}, // no text to embed: words only
		{Title: "Gears", Body: "gears turn gears", Public: true},
	}
	check(t, db.CreateMany(ctx, rows))
	all, err := db.Query[stENote](ctx).OrderBy(db.Col[int64]("id").Asc()).Get()
	check(t, err)
	check(t, notes.SyncNow(ctx, all...))
	found, err := notes.Search(ctx, "zanzibar", 5)
	check(t, err)
	if i := slices.IndexFunc(found, func(p ai.Passage[stENote]) bool { return p.Record.Title == "Zanzibar spice guide" }); len(found) != 2 || i < 0 || found[i].Position != -1 || found[i].Distance != 1 {
		t.Errorf("hybrid Search: %s", passages(found))
	}

	// A model of one size isn't asked for another; its vectors must fit.
	app2, err := anetos.New(anetos.WithSource(config.Map{"AI_PROVIDER": "fake", "APP_KEY": encryption.GenerateKey()}), anetos.WithLogOutput(io.Discard))
	check(t, err)
	defer func() { _ = app2.Close() }()
	client, err := ai.New(app2)
	check(t, err)
	fixed, err := ai.EmbeddingsFor(app2, ai.EmbeddingsConfig[stENote]{Text: func(n stENote) string { return n.Body }, Dimensions: 32, FixedSize: true})
	check(t, err)
	ctx = app2.Context(base) // not app's values
	fake := client.Fake()
	fresh := stENote{Title: "Fresh", Body: "words nobody embedded", Public: true}
	check(t, db.Create(ctx, &fresh))
	// (The fake makes the size expected.)
	check(t, fixed.SyncNow(ctx, fresh))
	if reqs := fake.EmbedRequests(); len(reqs) != 1 || reqs[0].Dimensions != 0 {
		t.Errorf("a fixed-size model was asked for a size: %+v", reqs)
	}
	if cs, _ := db.Chunks[stENote](ctx, fresh.ID); len(cs) != 1 || len(cs[0].Embedding) != 32 {
		t.Errorf("a fixed-size model's chunks: %+v", cs)
	}
}
