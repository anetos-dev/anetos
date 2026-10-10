// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/queue"
)

// EmbeddingsConfig says what of a model's records is embedded, for
// [EmbeddingsFor].
type EmbeddingsConfig[T any] struct {
	// Text returns a record's text: what is split into chunks and
	// embedded (its title and body, say). "" for none: the record has
	// no chunks, and only a search's words find it. Required.
	Text func(T) string
	// Dimensions is the vectors' size: the embeddings table's
	// (migrate.Schema.CreateEmbeddings), which the embedding model is
	// asked to shorten its own to (OpenAI's text-embedding-3 models and
	// Gemini's can). Required.
	Dimensions int
	// FixedSize is for models that make vectors of one size and can't be
	// asked for another (text-embedding-ada-002, most models of
	// OpenAI-compatible servers): no size is asked for, and the vectors
	// must have Dimensions.
	FixedSize bool
	// ChunkSize is the most characters in a chunk: the text is split on
	// paragraphs, then sentences, then words, to fit. Default 2000
	// (about 500 tokens).
	ChunkSize int
	// Title names a record in the results of the search tool
	// ([Embeddings.Tool]), so the model can cite it. Optional.
	Title func(T) string
	// Scope narrows every search ([Embeddings.Search], the tool) to the
	// records the context's user may see: their team's, the published
	// ones. It runs with the search's context. Optional (all records).
	Scope func(ctx context.Context, q *db.Q[T]) *db.Q[T]
}

// defaultChunkSize is ChunkSize's default, in characters.
const defaultChunkSize = 2000

// Embeddings keeps the embeddings of a model's records, in its
// embeddings table, and searches them: [EmbeddingsFor] makes one at
// startup. It is safe for concurrent use.
type Embeddings[T any] struct {
	cfg     EmbeddingsConfig[T]
	table   string
	job     string
	queue   *queue.Queue // nil: Sync embeds right away
	indexed atomic.Bool  // the table has a search index
}

// embeddingSets are the app's Embeddings, by table, for ai:embed.
type embeddingSets struct {
	mu   sync.Mutex
	sync map[string]func(context.Context, func(n int)) error
}

// EmbeddingsFor keeps the embeddings of T's records, in the table that
// migrate.Schema.CreateEmbeddings made for T's (its name plus
// "_embeddings"), with the app's embedding model (AI_EMBEDDING_MODEL):
//
//	articles, err := ai.EmbeddingsFor(app, ai.EmbeddingsConfig[models.Article]{
//		Text:       func(a models.Article) string { return a.Title + "\n\n" + a.Body },
//		Dimensions: 1536,
//	})
//
// Call [Embeddings.Sync] when records change, and search them with
// [Embeddings.Search] or give agents [Embeddings.Tool]. With the app's
// queue (queue.New, called first), Sync embeds in a job
// ("ai.embed:<table>"), which a worker must have registered too: call
// EmbeddingsFor in the setup both share. The command ai:embed syncs
// every record, after a change of model or Text. When the app's
// database comes from db.Connect, the app refuses to boot if it can't
// search vectors (db.VectorSearch: MySQL Community, MariaDB before 11.7,
// PostgreSQL without pgvector).
func EmbeddingsFor[T any](app *anetos.App, cfg EmbeddingsConfig[T]) (*Embeddings[T], error) {
	table, err := db.TableOf[T]()
	if err != nil {
		return nil, err
	}
	switch {
	case cfg.Text == nil:
		return nil, fmt.Errorf("ai: EmbeddingsFor[%T]: no Text", *new(T))
	case cfg.Dimensions < 1:
		return nil, fmt.Errorf("ai: EmbeddingsFor[%T]: Dimensions is %d: set it to the embeddings table's", *new(T), cfg.Dimensions)
	case cfg.ChunkSize < 0:
		return nil, fmt.Errorf("ai: EmbeddingsFor[%T]: ChunkSize is %d", *new(T), cfg.ChunkSize)
	case cfg.ChunkSize == 0:
		cfg.ChunkSize = defaultChunkSize
	}
	if _, err := anetos.Resolve[*Client](app); err != nil {
		return nil, errors.New("ai: EmbeddingsFor needs the app's AI client: call ai.New first")
	}
	// The app's database (db.Connect) must search vectors: checked when
	// the app boots, as search settings are.
	if d, ok := anetos.Lookup[*db.DB](app); ok && d != nil {
		if err := d.Require("the embeddings of "+table+" (ai.EmbeddingsFor)", db.VectorSearch); err != nil {
			return nil, err
		}
	}
	e := &Embeddings[T]{cfg: cfg, table: table, job: "ai.embed:" + table}
	if q, err := anetos.Resolve[*queue.Queue](app); err == nil {
		if err := queue.RegisterFunc(q, e.job, func(ctx context.Context, ids []int64) error {
			rows, err := db.Query[T](ctx).WhereKeys(anys(ids)...).Get()
			if err != nil {
				return err
			}
			return e.SyncNow(ctx, rows...) // deleted since: their chunks went with them
		}); err != nil {
			return nil, err
		}
		e.queue = q
	}
	sets, ok := anetos.Lookup[*embeddingSets](app)
	if !ok {
		sets = &embeddingSets{sync: map[string]func(context.Context, func(int)) error{}}
		anetos.Provide(app, sets)
		if err := app.AddCommand(embedCommand(sets)); err != nil {
			return nil, err
		}
	}
	sets.mu.Lock()
	defer sets.mu.Unlock()
	if _, dup := sets.sync[table]; dup {
		return nil, fmt.Errorf("ai: EmbeddingsFor called twice for the table %s", table)
	}
	sets.sync[table] = e.syncAll
	return e, nil
}

// embedCommand is ai:embed.
func embedCommand(sets *embeddingSets) cmd.Command {
	return cmd.Command{
		Name:        "ai:embed",
		Usage:       "[table…]",
		Description: "Embed the records whose text or embedding model changed (all tables', or those named)",
		Run: func(ctx context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("ai:embed", flag.ContinueOnError)
			if err := args.Parse(fs); err != nil {
				return err
			}
			sets.mu.Lock()
			all := maps.Clone(sets.sync)
			sets.mu.Unlock()
			tables := fs.Args()
			if len(tables) == 0 {
				tables = slices.Sorted(maps.Keys(all))
			}
			for _, t := range tables {
				if all[t] == nil {
					return cmd.Usagef("no embeddings for the table %q: the tables are [%s]", t, strings.Join(slices.Sorted(maps.Keys(all)), ", "))
				}
			}
			for _, t := range tables {
				n := 0
				if err := all[t](ctx, func(k int) { n += k }); err != nil {
					return fmt.Errorf("%s: %w", t, err)
				}
				if _, err := fmt.Fprintf(args.Stdout, "%s: %d records synced.\n", t, n); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// Sync updates the embeddings of rows, after they were created or
// changed: with the app's queue, in a job dispatched when the
// transaction in ctx, if any, commits; without, right away (or when the
// transaction commits; an error then is logged). Only chunks whose text
// changed are embedded again. A deleted record's chunks are deleted
// with it. With the queue, the rows go in jobs of a hundred; if a
// dispatch fails, the batches before it are dispatched already.
func (e *Embeddings[T]) Sync(ctx context.Context, rows ...T) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		id, err := db.RecordID(r)
		if err != nil {
			return err
		}
		ids[i] = id
	}
	if e.queue != nil {
		for batch := range slices.Chunk(ids, syncBatch) {
			if err := e.queue.DispatchFunc(ctx, e.job, batch, queue.AfterCommit()); err != nil {
				return err
			}
		}
		return nil
	}
	if !db.InTx(ctx) {
		return e.SyncNow(ctx, rows...)
	}
	db.AfterCommit(ctx, func(ctx context.Context) {
		var err error
		for batch := range slices.Chunk(ids, syncBatch) {
			var rows []T
			if rows, err = db.Query[T](ctx).WhereKeys(anys(batch)...).Get(); err == nil {
				err = e.SyncNow(ctx, rows...)
			}
			if err != nil {
				break
			}
		}
		if err != nil {
			anetos.Logger(ctx).ErrorContext(ctx, "ai: syncing embeddings failed", "table", e.table, "error", err)
		}
	})
	return nil
}

// syncBatch is how many records a sync job, or a step of SyncNow, takes.
const syncBatch = 100

// SyncNow updates the embeddings of rows right away: it splits each
// record's text into chunks, embeds those whose text or model changed,
// in batches, and replaces the record's chunks, a hundred records at a
// time. Don't call it inside a transaction: models are slow, and the
// transaction would hold its locks meanwhile.
func (e *Embeddings[T]) SyncNow(ctx context.Context, rows ...T) error {
	for batch := range slices.Chunk(rows, syncBatch) {
		if err := e.syncNow(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}

func (e *Embeddings[T]) syncNow(ctx context.Context, rows []T) error {
	cl, err := From(ctx)
	if err != nil {
		return err
	}
	_, model, err := cl.embedder()
	if err != nil {
		return err
	}
	type record struct {
		id     int64
		chunks []db.Chunk
	}
	recs := make([]record, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		id, err := db.RecordID(r)
		if err != nil {
			return err
		}
		var chunks []db.Chunk
		for _, text := range splitChunks(e.cfg.Text(r), e.cfg.ChunkSize, 0) {
			sum := sha256.Sum256([]byte(text))
			chunks = append(chunks, db.Chunk{Content: text, ContentHash: hex.EncodeToString(sum[:]), EmbeddingModel: model})
		}
		recs = append(recs, record{id, chunks})
		ids = append(ids, id)
	}
	stored, err := db.Chunks[T](ctx, ids...)
	if err != nil {
		return err
	}
	// The vectors already stored, by record and text (of this model).
	type key struct {
		id   int64
		hash string
	}
	have := map[key]db.Vector{}
	byRecord := map[int64][]db.Chunk{}
	for _, c := range stored {
		byRecord[c.RecordID] = append(byRecord[c.RecordID], c)
		if c.EmbeddingModel == model && len(c.Embedding) == e.cfg.Dimensions {
			have[key{c.RecordID, c.ContentHash}] = c.Embedding
		}
	}
	var texts []string
	var missing []*db.Chunk
	changed := recs[:0:0]
	for _, r := range recs {
		same := len(byRecord[r.id]) == len(r.chunks)
		for i := range r.chunks {
			c := &r.chunks[i]
			if v, ok := have[key{r.id, c.ContentHash}]; ok {
				c.Embedding = v
			} else {
				texts = append(texts, c.Content)
				missing = append(missing, c)
				same = false
			}
			if same {
				old := byRecord[r.id][i]
				same = old.Position == i && old.ContentHash == c.ContentHash && old.EmbeddingModel == model
			}
		}
		if !same {
			changed = append(changed, r)
		}
	}
	if len(texts) > 0 {
		vs, err := embed(ctx, EmbedForDocument, e.ask(), e.cfg.Dimensions, texts)
		if err != nil {
			return err
		}
		for i, c := range missing {
			c.Embedding = vs[i]
		}
	}
	for _, r := range changed {
		if err := db.ReplaceChunks[T](ctx, r.id, r.chunks); err != nil {
			return err
		}
	}
	return nil
}

// ask is the vector size to ask the model for: 0 for a FixedSize one.
func (e *Embeddings[T]) ask() int {
	if e.cfg.FixedSize {
		return 0
	}
	return e.cfg.Dimensions
}

// syncAll syncs every record, a hundred at a time, reporting how many,
// after deleting chunks left by deleted records.
func (e *Embeddings[T]) syncAll(ctx context.Context, done func(n int)) error {
	if _, err := db.PruneChunks[T](ctx); err != nil {
		return err
	}
	cursor := ""
	for {
		page, err := db.Query[T](ctx).CursorPaginate(cursor, 100)
		if err != nil {
			return err
		}
		if err := e.SyncNow(ctx, page.Data...); err != nil {
			return err
		}
		done(len(page.Data))
		if page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

// SyncAll syncs every record ([Embeddings.SyncNow]), a hundred at a
// time: after a change of embedding model or of Text. Unchanged chunks
// aren't embedded again; chunks of records that no longer exist are
// deleted (db.PruneChunks). The command ai:embed runs it.
func (e *Embeddings[T]) SyncAll(ctx context.Context) error {
	return e.syncAll(ctx, func(int) {})
}

// Passage is a search's result: a record and its passage nearest the
// query.
type Passage[T any] struct {
	// Record is the record found.
	Record T
	// Text is its chunk nearest the query; for a record found only by
	// its words ([Embeddings.Search] with a search index), the start of
	// its text.
	Text string
	// Position is the chunk's position in the record, from 0; -1 for the
	// start of the text of a record found only by its words.
	Position int
	// Distance is the chunk's cosine distance to the query: 0 is the
	// same meaning, 1 unrelated; 1 for a record found only by its words.
	Distance float64
}

// Search finds the records nearest query, the best first, at most limit
// (at least 1; 10 if 0), with their passages nearest it, among those
// the config's Scope and scopes allow. If T's table has a search index
// (migrate.Table.SearchIndex), it is a hybrid search: records that its
// words find rank high too (db.Q.Hybrid), so names, codes and rare words
// aren't missed; otherwise, by meaning alone (db.Q.Similar). Only
// chunks of the app's embedding model count: after changing it, run
// ai:embed. The query is embedded as one ([EmbedQuery]).
func (e *Embeddings[T]) Search(ctx context.Context, query string, limit int, scopes ...func(*db.Q[T]) *db.Q[T]) ([]Passage[T], error) {
	switch {
	case limit < 0:
		return nil, fmt.Errorf("ai: search with a limit of %d", limit)
	case limit == 0:
		limit = 10
	}
	if strings.TrimSpace(query) == "" {
		return []Passage[T]{}, nil
	}
	cl, err := From(ctx)
	if err != nil {
		return nil, err
	}
	_, model, err := cl.embedder()
	if err != nil {
		return nil, err
	}
	vs, err := embed(ctx, EmbedForQuery, e.ask(), e.cfg.Dimensions, []string{query})
	if err != nil {
		return nil, err
	}
	v := vs[0]
	q := db.Query[T](ctx)
	if e.cfg.Scope != nil {
		q = e.cfg.Scope(ctx, q)
	}
	q = q.Scope(scopes...)
	indexed, err := e.searchIndexed(ctx)
	if err != nil {
		return nil, err
	}
	if indexed {
		q = q.Hybrid(query, model, v)
	} else {
		q = q.Similar(model, v)
	}
	rows, err := q.Limit(limit).Get()
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		if ids[i], err = db.RecordID(r); err != nil {
			return nil, err
		}
	}
	near, err := db.NearestChunks[T](ctx, model, v, ids...)
	if err != nil {
		return nil, err
	}
	best := map[int64]db.ChunkMatch{}
	for _, c := range near { // nearest first
		if _, ok := best[c.RecordID]; !ok {
			best[c.RecordID] = c
		}
	}
	out := make([]Passage[T], len(rows))
	for i, r := range rows {
		if c, ok := best[ids[i]]; ok {
			out[i] = Passage[T]{Record: r, Text: c.Content, Position: c.Position, Distance: c.Distance}
			continue
		}
		text := ""
		if cs := splitChunks(e.cfg.Text(r), e.cfg.ChunkSize, 1); len(cs) > 0 {
			text = cs[0]
		}
		out[i] = Passage[T]{Record: r, Text: text, Position: -1, Distance: 1}
	}
	return out, nil
}

// searchIndexed reports whether the table has a search index; a yes is
// kept (migrations don't drop them while the app runs).
func (e *Embeddings[T]) searchIndexed(ctx context.Context) (bool, error) {
	if e.indexed.Load() {
		return true, nil
	}
	idx, err := db.SearchIndexes(ctx)
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(idx, func(i db.SearchIndex) bool { return i.Table == e.table }) {
		e.indexed.Store(true)
		return true, nil
	}
	return false, nil
}

// searchInput is the search tool's input.
type searchInput struct {
	Query string `json:"query" description:"What to look for, in words: a question or the topic" validate:"required|max:1000"`
}

// SearchResult is a result of the search tool ([Embeddings.Tool]), as
// the model reads it.
type SearchResult struct {
	// ID is the record's primary key.
	ID int64 `json:"id"`
	// Title names the record (EmbeddingsConfig.Title), if set.
	Title string `json:"title,omitempty"`
	// Text is the record's passage nearest the query.
	Text string `json:"text"`
}

// Tool returns a tool that searches the records ([Embeddings.Search]),
// for an agent to answer from them (retrieval-augmented generation): the
// model sends a query and gets the best limit records' passages
// ([SearchResult]: ID, Title, Text). The config's Scope applies, with
// the context of the call, so the tool finds only what the user may see.
//
//	agent := ai.Agent{
//		Name:         "support",
//		Instructions: "Answer from the help articles; search them first, and cite their titles.",
//		Tools:        []ai.Tool{articles.Tool("search_articles", "Search the help articles", 5)},
//	}
//
// Tool panics if name isn't a valid tool name, as [NewTool] does.
func (e *Embeddings[T]) Tool(name, description string, limit int) Tool {
	return NewTool(name, description, func(ctx context.Context, in searchInput) ([]SearchResult, error) {
		found, err := e.Search(ctx, in.Query, limit)
		if err != nil {
			return nil, err
		}
		out := make([]SearchResult, len(found))
		for i, p := range found {
			id, err := db.RecordID(p.Record)
			if err != nil {
				return nil, err
			}
			out[i] = SearchResult{ID: id, Text: p.Text}
			if e.cfg.Title != nil {
				out[i].Title = e.cfg.Title(p.Record)
			}
		}
		return out, nil
	})
}

// anys returns ids as []any.
func anys(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// splitChunks splits text into chunks of at most size characters,
// filled in order with whole paragraphs (split on blank lines) while
// they fit, or a long paragraph's sentences, a long sentence's words,
// and a long word's characters; at most limit chunks (0: all).
// Whitespace inside a paragraph is collapsed.
func splitChunks(text string, size, limit int) []string {
	var chunks []string
	var cur strings.Builder
	curLen := 0
	full := func() bool { return limit > 0 && len(chunks) >= limit }
	// add appends a piece of n characters, after sep, or starts a chunk
	// with it if it doesn't fit.
	add := func(piece string, n int, sep string) {
		if curLen > 0 && curLen+len(sep)+n > size {
			chunks = append(chunks, cur.String())
			cur.Reset()
			curLen = 0
		}
		if curLen > 0 {
			cur.WriteString(sep)
			curLen += len(sep) // ASCII
		}
		cur.WriteString(piece)
		curLen += n
	}
	for _, para := range paragraphs(text) {
		if full() {
			break
		}
		sep := "\n\n"
		if n := utf8.RuneCountInString(para); n <= size {
			add(para, n, sep)
			continue
		}
		for _, sentence := range sentences(para) {
			if full() {
				break
			}
			if n := utf8.RuneCountInString(sentence); n <= size {
				add(sentence, n, sep)
				sep = " "
				continue
			}
			for word := range strings.FieldsSeq(sentence) {
				if full() {
					break
				}
				r := []rune(word)
				for len(r) > size && !full() {
					add(string(r[:size]), size, sep)
					sep = " "
					r = r[size:]
				}
				if full() {
					break
				}
				add(string(r), len(r), sep)
				sep = " "
			}
		}
	}
	if curLen > 0 && !full() {
		chunks = append(chunks, cur.String())
	}
	return chunks
}

// paragraphBreak is a blank line: a line break, then whitespace with
// another.
var paragraphBreak = regexp.MustCompile(`\n[ \t\f\v]*\n`)

// paragraphs returns text's paragraphs, with their whitespace collapsed.
func paragraphs(text string) []string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var out []string
	for _, p := range paragraphBreak.Split(text, -1) {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sentences splits a paragraph after each ., ! or ? followed by a space,
// and after each full-width 。！？ (Chinese and Japanese don't put spaces
// between sentences).
func sentences(para string) []string {
	var out []string
	start := 0
	rs := []rune(para)
	for i := range rs {
		end := false
		switch rs[i] {
		case '.', '!', '?':
			end = i+1 < len(rs) && unicode.IsSpace(rs[i+1])
		case '。', '！', '？':
			end = true
		}
		if end {
			if s := strings.TrimSpace(string(rs[start : i+1])); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(string(rs[start:])); rest != "" {
		out = append(out, rest)
	}
	return out
}
