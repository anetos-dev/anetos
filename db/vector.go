// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos/internal/dbutil"
)

// Vector is an embedding: a column of the vector type migrations make
// (migrate.Table.Vector), read and written as the database needs it
// (pgvector's text, MariaDB's and SQLite's binary float32s).
type Vector []float32

// Scan implements sql.Scanner: a string is pgvector's text ("[1,2,3]",
// which the PostgreSQL driver returns), bytes are 4-byte little-endian
// floats (MariaDB's and SQLite's storage).
func (v *Vector) Scan(src any) error {
	switch s := src.(type) {
	case nil:
		*v = nil
		return nil
	case []byte:
		// Text a driver returns as bytes (VEC_ToText, raw SQL), or the
		// binary form, which never parses as text.
		if out, ok := parseVectorText(string(s)); ok {
			*v = out
			return nil
		}
		out, err := vectorOfBytes(s)
		if err != nil {
			return err
		}
		*v = out
		return nil
	case string:
		text := strings.TrimSpace(s)
		if !strings.HasPrefix(text, "[") {
			return fmt.Errorf("db: invalid vector %.40q", text)
		}
		if !strings.HasSuffix(text, "]") {
			return fmt.Errorf("db: invalid vector %.40q", text)
		}
		text = strings.TrimSpace(text[1 : len(text)-1])
		if text == "" {
			*v = Vector{}
			return nil
		}
		parts := strings.Split(text, ",")
		out := make(Vector, len(parts))
		for i, p := range parts {
			f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
			if err != nil {
				return fmt.Errorf("db: invalid vector component %q: %w", p, err)
			}
			out[i] = float32(f)
		}
		*v = out
		return nil
	default:
		return fmt.Errorf("db: can't scan %T into a Vector", src)
	}
}

// parseVectorText parses "[1,2.5,-3e-2]" strictly: ASCII brackets,
// numbers and commas, with spaces.
func parseVectorText(s string) (Vector, bool) {
	text := strings.TrimSpace(s)
	if len(text) < 2 || text[0] != '[' || text[len(text)-1] != ']' {
		return nil, false
	}
	for i := 0; i < len(text); i++ {
		if !strings.ContainsRune("[]0123456789.,-+eE ", rune(text[i])) {
			return nil, false
		}
	}
	inner := strings.TrimSpace(text[1 : len(text)-1])
	if inner == "" {
		return Vector{}, true
	}
	parts := strings.Split(inner, ",")
	out := make(Vector, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil, false
		}
		out[i] = float32(f)
	}
	return out, true
}

// vectorBytes encodes v as little-endian float32s.
func vectorBytes(v Vector) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func vectorOfBytes(b []byte) (Vector, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("db: a binary vector of %d bytes isn't float32s", len(b))
	}
	out := make(Vector, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out, nil
}

// vectorText is pgvector's text form of v.
func vectorText(v Vector) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}

// vectorArg converts a Vector argument for the dialect.
func vectorArg(dialect string, v any) (any, bool) {
	var vec Vector
	switch x := v.(type) {
	case Vector:
		vec = x
	case *Vector:
		if x == nil {
			return nil, true
		}
		vec = *x
	default:
		return nil, false
	}
	if vec == nil {
		return nil, true
	}
	if dialect == "sqlite" {
		return vectorBytes(vec), true
	}
	// PostgreSQL (pgvector) and MariaDB (VEC_FromText) read text.
	return vectorText(vec), true
}

// checkVectorSize refuses, on MariaDB, a vector whose size isn't that of
// table's embeddings: its distance functions return NULL for them, where
// pgvector and SQLite's function fail. The size is kept for a minute,
// and read again sooner when it doesn't match (a migration may have
// changed it).
func (d *DB) checkVectorSize(ctx context.Context, c conn, table string, n int) error {
	if d.dialect.Name() != "mysql" {
		return nil
	}
	name := EmbeddingsTable(table)
	if kept, ok := d.vecDims.Load(name); ok && kept.(vectorSize).dims == n && time.Since(kept.(vectorSize).at) < time.Minute {
		return nil
	}
	typ, err := scalar[string](ctx, d, c, rawBuilder(d.dialect,
		"SELECT COALESCE(MAX(COLUMN_TYPE), '') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = 'embedding'", name))
	if err != nil {
		return err
	}
	var dims int
	if k, _ := fmt.Sscanf(strings.ToLower(typ), "vector(%d)", &dims); k != 1 {
		return nil // no such table, yet: the query says so
	}
	d.vecDims.Store(name, vectorSize{dims, time.Now()})
	if dims != n {
		return fmt.Errorf("db: a vector of %d dimensions for %s, whose vectors have %d: was it made by another embedding model?", n, name, dims)
	}
	return nil
}

// vectorSize is an embeddings table's vector size, and when it was read.
type vectorSize struct {
	dims int
	at   time.Time
}

// CosineDistance returns 1 minus the cosine of the angle between a and
// b: 0 for the same direction, 1 for unrelated, 2 for opposite. It is the
// distance [Q.Similar] orders by; SQLite's driver computes it with this
// function. Vectors of different lengths are an error; a zero vector is
// at distance 1 from every other.
func CosineDistance(a, b Vector) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("db: vectors of %d and %d dimensions can't be compared: were they made by different embedding models?", len(a), len(b))
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 1, nil
	}
	return 1 - dot/(math.Sqrt(na)*math.Sqrt(nb)), nil
}

// CosineDistanceBlobs is [CosineDistance] of two vectors stored as
// little-endian float32s, as SQLite stores them: the SQL function
// anetos_vec_distance_cosine, which the SQLite driver registers.
func CosineDistanceBlobs(a, b []byte) (float64, error) {
	va, err := vectorOfBytes(a)
	if err != nil {
		return 0, err
	}
	vb, err := vectorOfBytes(b)
	if err != nil {
		return 0, err
	}
	return CosineDistance(va, vb)
}

// SQLiteCosineFunction is the SQL function SQLite queries use for
// [CosineDistanceBlobs]; the SQLite driver registers it.
const SQLiteCosineFunction = "anetos_vec_distance_cosine"

// VectorSearch is the vector search capability: [Q.Similar], [Q.Hybrid] and
// embeddings tables. PostgreSQL has it with the pgvector extension,
// MariaDB from 11.7, and SQLite by comparing every vector (fine for tens
// of thousands of chunks); MySQL Community doesn't.
const VectorSearch Capability = "vector search"

// supportsVector reports whether the server can store and compare
// vectors, and if not, how to get it.
func (d *DB) supportsVector(ctx context.Context) (bool, string, error) {
	switch d.dialect.Name() {
	case "sqlite":
		return true, "", nil
	case "postgres":
		var n int64
		if err := d.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM pg_available_extensions WHERE name = 'vector'").Scan(&n); err != nil {
			return false, "", fmt.Errorf("db: check for %s: %w", VectorSearch, err)
		}
		if n == 0 {
			return false, "install the pgvector extension on the server (https://github.com/pgvector/pgvector); migrations creating embeddings tables then run CREATE EXTENSION vector", nil
		}
		return true, "", nil
	case "mysql":
		var version string
		if err := d.sql.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
			return false, "", fmt.Errorf("db: check for %s: %w", VectorSearch, err)
		}
		if !strings.Contains(strings.ToLower(version), "mariadb") {
			return false, "MySQL Community has no vector distance functions (they're HeatWave's); use MariaDB 11.7 or later, PostgreSQL with pgvector, or SQLite", nil
		}
		if !versionAtLeast(version, 11, 7) {
			return false, "MariaDB has vectors from 11.7 (this is " + version + "); upgrade it, or use PostgreSQL with pgvector, or SQLite", nil
		}
		return true, "", nil
	}
	return false, "use PostgreSQL with pgvector, MariaDB 11.7+ or SQLite", nil
}

// versionAtLeast reports whether a "11.8.3-MariaDB…" version is at least
// major.minor.
func versionAtLeast(version string, major, minor int) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	ma, err1 := strconv.Atoi(parts[0])
	mi, err2 := strconv.Atoi(strings.TrimLeftFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' }))
	if err1 != nil || err2 != nil {
		return false
	}
	return ma > major || ma == major && mi >= minor
}

// EmbeddingsTable returns the table holding table's embeddings:
// "<table>_embeddings" (migrate.Schema.CreateEmbeddings makes it).
func EmbeddingsTable(table string) string { return table + "_embeddings" }

// SimilarCandidates is how many of the nearest chunks [Q.Similar] and
// [Q.Hybrid] consider (and how many keyword matches Hybrid ranks):
// conditions of the query (Where) apply to the records they come from,
// so a query whose conditions exclude most records finds fewer.
const SimilarCandidates = 200

// rrfK is reciprocal rank fusion's constant: a rank r scores 1/(k+r).
const rrfK = 60

type similarSpec struct {
	table, pk string
	model     string
	vector    Vector
	keyword   *searchSpec // Hybrid
}

// Similar keeps the rows that have embeddings (in the table
// [EmbeddingsTable] names, made by migrate.Schema.CreateEmbeddings) and
// orders them by how close their nearest chunk is to v, nearest first:
// the cosine distance of vectors made by the embedding model named model
// (other models' vectors are ignored; "" compares every one). It
// considers the [SimilarCandidates] nearest chunks. Most apps call it
// through package ai, which embeds the query text:
//
//	posts, err := db.Query[Post](ctx).Where(published).Similar("text-embedding-3-small", queryVector).Limit(10).Get()
//
// The database needs the [VectorSearch] capability. Search and Hybrid
// replace it; Update, Delete and CursorPaginate refuse it.
func (q *Q[T]) Similar(model string, v Vector) *Q[T] {
	c := q.clone()
	c.dropRanking()
	if c.err != nil {
		return c
	}
	spec, err := c.newSimilar(model, v)
	if err != nil {
		c.err = err
		return c
	}
	c.similar = spec
	c.orders = append([]Order{{raw: similarRank{spec}}}, c.orders...)
	return c
}

// Hybrid ranks the rows by both full-text search of text ([Q.Search])
// and similarity to v ([Q.Similar]), merged by reciprocal rank fusion: a
// row's score adds 1/(60 + its rank) for each of the two lists it's in
// (the best [SimilarCandidates] of each), so rows that both find come
// first, without comparing their scores. It needs a search index and an
// embeddings table. Text without words makes it [Q.Similar].
func (q *Q[T]) Hybrid(text, model string, v Vector) *Q[T] {
	c := q.clone()
	c.dropRanking()
	if c.err != nil {
		return c
	}
	keyword, err := c.newSearch(text)
	if err != nil {
		c.err = err
		return c
	}
	spec, err := c.newSimilar(model, v)
	if err != nil {
		c.err = err
		return c
	}
	spec.keyword = keyword
	c.similar = spec
	c.orders = append([]Order{{raw: similarRank{spec}}}, c.orders...)
	return c
}

// dropRanking removes a search's or similarity's ranking and its spec.
func (q *Q[T]) dropRanking() {
	q.orders = slices.DeleteFunc(slices.Clone(q.orders), func(o Order) bool {
		switch o.raw.(type) {
		case searchRank, similarRank:
			return true
		}
		return false
	})
	q.search, q.similar = nil, nil
}

func (q *Q[T]) newSimilar(model string, v Vector) (*similarSpec, error) {
	if len(v) == 0 {
		return nil, errors.New("db: Similar needs a vector")
	}
	if q.m.pk < 0 {
		return nil, fmt.Errorf("db: Similar needs %s to have a primary key", q.m.typ)
	}
	if _, err := From(q.ctx); err != nil {
		return nil, err
	}
	return &similarSpec{table: q.m.table, pk: q.m.cols[q.m.pk].name, model: model, vector: slices.Clone(v)}, nil
}

// distance writes the distance of the embeddings table's column to the
// spec's vector.
func (s *similarSpec) distance(b *sqlBuilder) {
	col := b.d.QuoteIdent("embedding")
	switch b.d.Name() {
	case "postgres":
		b.write(col + " <=> CAST(")
		b.arg(s.vector)
		b.write(" AS vector)")
	case "mysql":
		b.write("VEC_DISTANCE_COSINE(" + col + ", ")
		b.arg(s.vector)
		b.write(")")
	default:
		b.write(SQLiteCosineFunction + "(" + col + ", ")
		b.arg(s.vector)
		b.write(")")
	}
}

// chunks writes the nearest chunks' records with their rank:
// anetos_id, anetos_distance (the nearest chunk's) and anetos_rank.
func (s *similarSpec) chunks(b *sqlBuilder) {
	b.write("SELECT record_id AS anetos_id, MIN(anetos_distance) AS anetos_distance, ROW_NUMBER() OVER (ORDER BY MIN(anetos_distance)) AS anetos_rank FROM (SELECT record_id, ")
	s.distance(b)
	b.write(" AS anetos_distance FROM ")
	emb := quoteName(b.d, EmbeddingsTable(s.table))
	b.write(emb)
	var conds []string
	if s.model != "" {
		conds = append(conds, "model = ?")
	}
	if b.d.Name() == "mysql" {
		// No foreign key on MariaDB: chunks another table's cascade left
		// mustn't take the candidates' places.
		table := quoteName(b.d, s.table)
		conds = append(conds, "EXISTS (SELECT 1 FROM "+table+" WHERE "+table+"."+b.d.QuoteIdent(s.pk)+" = "+emb+".record_id)")
	}
	if len(conds) > 0 {
		b.write(" WHERE ")
		var args []any
		if s.model != "" {
			args = append(args, s.model)
		}
		b.raw(strings.Join(conds, " AND "), args)
	}
	b.write(" ORDER BY ")
	s.distance(b) // the expression, not the alias: what a vector index serves
	b.write(" LIMIT " + strconv.Itoa(SimilarCandidates) + ") AS anetos_chunks GROUP BY record_id")
}

// join writes the JOIN of the candidates to the model's table.
func (s *similarSpec) join(b *sqlBuilder) {
	b.write("JOIN (")
	if s.keyword == nil {
		s.chunks(b)
	} else {
		table := quoteName(b.d, s.table)
		pk := table + "." + b.d.QuoteIdent(s.pk)
		// 1e0: a float, where MariaDB's 1.0 would be a 5-place decimal.
		b.write("SELECT anetos_id, SUM(1e0 / (" + strconv.Itoa(rrfK) + " + anetos_rank)) AS anetos_score FROM (SELECT anetos_id, anetos_rank FROM (")
		// The keyword side: the best matches, ranked.
		b.write("SELECT " + pk + " AS anetos_id, ROW_NUMBER() OVER (ORDER BY ")
		searchRank{s.keyword}.build(b)
		b.write(") AS anetos_rank FROM " + table)
		if b.d.Name() == "sqlite" {
			b.write(" ")
			searchJoin{s.keyword}.build(b)
		} else {
			b.write(" WHERE ")
			searchCond{s.keyword}.build(b)
		}
		b.write(" ORDER BY anetos_rank LIMIT " + strconv.Itoa(SimilarCandidates) + ") AS anetos_keyword UNION ALL SELECT anetos_id, anetos_rank FROM (")
		s.chunks(b)
		b.write(") AS anetos_vector) AS anetos_ranks GROUP BY anetos_id")
	}
	b.write(") AS anetos_similar ON anetos_similar.anetos_id = " + quoteName(b.d, s.table) + "." + b.d.QuoteIdent(s.pk))
}

// similarRank orders by similarity (or the hybrid score), best first.
type similarRank struct{ s *similarSpec }

func (e similarRank) build(b *sqlBuilder) {
	if e.s.keyword == nil {
		b.write("anetos_similar.anetos_distance")
	} else {
		b.write("anetos_similar.anetos_score DESC")
	}
}

// similarHint adds to err, from a query with Similar or Hybrid, what a
// missing embeddings table or vector support looks like.
func (q *Q[T]) similarHint(err error) error {
	if err == nil || q.similar == nil {
		return err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, EmbeddingsTable(q.similar.table)) && (strings.Contains(msg, "does not exist") || strings.Contains(msg, "doesn't exist") || strings.Contains(msg, "no such")):
		return fmt.Errorf("%w (does %s have embeddings? Create the table with s.CreateEmbeddings in a migration; vector search needs PostgreSQL with pgvector, MariaDB 11.7+ or SQLite)", err, q.similar.table)
	case strings.Contains(msg, "no such function: "+SQLiteCosineFunction):
		return fmt.Errorf("%w (vector search on SQLite needs the function the drivers/sqlite module registers)", err)
	}
	return err
}

// Chunk is a row of an embeddings table ([EmbeddingsTable]): one passage
// of a record, with its embedding. Package ai reads and writes them.
type Chunk struct {
	// RecordID is the record's primary key.
	RecordID int64 `db:"record_id"`
	// Position is the passage's position in the record, from 0.
	Position int `db:"chunk"`
	// Content is the passage's text.
	Content string `db:"content"`
	// ContentHash identifies the text, so unchanged text isn't embedded
	// again.
	ContentHash string `db:"content_hash"`
	// Model is the embedding model's name.
	Model string `db:"model"`
	// Embedding is the passage's vector.
	Embedding Vector `db:"embedding"`
}

// ChunkMatch is a chunk and its distance to a vector ([NearestChunks]).
type ChunkMatch struct {
	Chunk
	// Distance is the cosine distance to the vector: 0 is the same
	// direction.
	Distance float64 `db:"distance"`
}

// Chunks returns the chunks of T's records with the IDs (all records'
// when there are none), by record and position.
func Chunks[T any](ctx context.Context, recordIDs ...int64) ([]Chunk, error) {
	d, _, m, err := target[T](ctx)
	if err != nil {
		return nil, err
	}
	b := &sqlBuilder{d: d.dialect}
	b.write("SELECT record_id, chunk, content, content_hash, model, embedding FROM ")
	b.name(EmbeddingsTable(m.table))
	if len(recordIDs) > 0 {
		b.write(" WHERE ")
		Col[int64]("record_id").In(recordIDs...).build(b)
	}
	b.write(" ORDER BY record_id, chunk")
	return rawRows[Chunk](ctx, b)
}

// ReplaceChunks makes chunks the record's chunks, in one transaction: its
// other chunks are deleted. The chunks are stored for recordID, at their
// positions in the slice (their RecordID and Position are ignored).
func ReplaceChunks[T any](ctx context.Context, recordID int64, chunks []Chunk) error {
	d, _, m, err := target[T](ctx)
	if err != nil {
		return err
	}
	if m.pk < 0 {
		return fmt.Errorf("db: %s has no primary key", m.typ)
	}
	if !InTx(ctx) {
		// Two replacements of one record (two workers) wait for each
		// other on its row; a deadlock or serialization failure runs
		// again.
		return dbutil.Retry(ctx, func() error { return replaceChunks(ctx, d, m, recordID, chunks) })
	}
	return replaceChunks(ctx, d, m, recordID, chunks)
}

func replaceChunks(ctx context.Context, d *DB, m *meta, recordID int64, chunks []Chunk) error {
	table := EmbeddingsTable(m.table)
	return Tx(ctx, func(ctx context.Context) error {
		// Lock the record: replacements of its chunks take turns, and a
		// record deleted meanwhile gets none.
		lock := &sqlBuilder{d: d.dialect}
		lock.write("SELECT ")
		lock.name(m.cols[m.pk].name)
		lock.write(" AS anetos_id FROM ")
		lock.name(m.table)
		lock.write(" WHERE ")
		Col[int64](m.cols[m.pk].name).Eq(recordID).build(lock)
		if clause := d.dialect.LockClause(false); clause != "" {
			lock.write(" " + clause)
		}
		_, c, err := handle(ctx)
		if err != nil {
			return err
		}
		rows, err := d.query(ctx, c, lock.String(), lock.args)
		if err != nil {
			return err
		}
		found := rows.Next()
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if !found {
			chunks = nil // deleted: remove what's left
		}
		del := &sqlBuilder{d: d.dialect}
		del.write("DELETE FROM ")
		del.name(table)
		del.write(" WHERE ")
		Col[int64]("record_id").Eq(recordID).build(del)
		if err := execBuilder(ctx, del); err != nil {
			return err
		}
		at := now(ctx).UTC()
		for i := 0; i < len(chunks); i += 100 {
			batch := chunks[i:min(i+100, len(chunks))]
			ins := &sqlBuilder{d: d.dialect}
			ins.write("INSERT INTO ")
			ins.name(table)
			ins.write(" (record_id, chunk, content, content_hash, model, embedding, created_at, updated_at) VALUES ")
			for j, c := range batch {
				if j > 0 {
					ins.write(", ")
				}
				ins.write("(")
				for k, v := range []any{recordID, i + j, c.Content, c.ContentHash, c.Model, c.Embedding, at, at} {
					if k > 0 {
						ins.write(", ")
					}
					ins.arg(v)
				}
				ins.write(")")
			}
			if err := execBuilder(ctx, ins); err != nil {
				return err
			}
		}
		return nil
	})
}

// PruneChunks deletes the chunks of T's records that no longer exist,
// and returns how many: those a cascading delete left on MariaDB, where
// the embeddings table has no foreign key (see
// migrate.Schema.CreateEmbeddings).
func PruneChunks[T any](ctx context.Context) (int64, error) {
	d, c, m, err := target[T](ctx)
	if err != nil {
		return 0, err
	}
	if m.pk < 0 {
		return 0, fmt.Errorf("db: %s has no primary key", m.typ)
	}
	emb, table := quoteName(d.dialect, EmbeddingsTable(m.table)), quoteName(d.dialect, m.table)
	res, err := d.exec(ctx, c, "DELETE FROM "+emb+" WHERE NOT EXISTS (SELECT 1 FROM "+table+" WHERE "+table+"."+d.dialect.QuoteIdent(m.cols[m.pk].name)+" = "+emb+".record_id)", nil)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// NearestChunks returns the chunks of the records with the IDs (made by
// the embedding model named model; "" for any) by their distance to v,
// nearest first: the passages to show for the records [Q.Similar] or
// [Q.Hybrid] found.
func NearestChunks[T any](ctx context.Context, model string, v Vector, recordIDs ...int64) ([]ChunkMatch, error) {
	if len(recordIDs) == 0 {
		return []ChunkMatch{}, nil
	}
	d, c, m, err := target[T](ctx)
	if err != nil {
		return nil, err
	}
	if err := d.checkVectorSize(ctx, c, m.table, len(v)); err != nil {
		return nil, err
	}
	s := &similarSpec{table: m.table, vector: v}
	b := &sqlBuilder{d: d.dialect}
	b.write("SELECT record_id, chunk, content, content_hash, model, embedding, ")
	s.distance(b)
	b.write(" AS distance FROM ")
	b.name(EmbeddingsTable(m.table))
	b.write(" WHERE ")
	conds := []Expr{Col[int64]("record_id").In(recordIDs...)}
	if model != "" {
		conds = append(conds, Col[string]("model").Eq(model))
	}
	And(conds...).build(b)
	b.write(" ORDER BY distance, record_id, chunk")
	return rawRows[ChunkMatch](ctx, b)
}

// rawRows runs a built query and scans its rows.
func rawRows[R any](ctx context.Context, b *sqlBuilder) ([]R, error) {
	if b.err != nil {
		return nil, b.err
	}
	d, c, err := handle(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := d.query(ctx, c, b.String(), b.args)
	return collect[R](d, rows, err)
}

// execBuilder runs a built statement.
func execBuilder(ctx context.Context, b *sqlBuilder) error {
	if b.err != nil {
		return b.err
	}
	d, c, err := handle(ctx)
	if err != nil {
		return err
	}
	_, err = d.exec(ctx, c, b.String(), b.args)
	return err
}

// RecordID returns row's primary key as an int64, the key of its
// embeddings: an integer primary key that isn't zero.
func RecordID[T any](row T) (int64, error) {
	m, err := metaOf(reflect.TypeFor[T]())
	if err != nil {
		return 0, err
	}
	if m.pk < 0 {
		return 0, fmt.Errorf("db: %s has no primary key (tag a field db:\",pk\")", m.typ)
	}
	v := reflect.ValueOf(row)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0, fmt.Errorf("db: a nil %s", m.typ)
		}
		v = v.Elem()
	}
	f := fieldOf(v, m.cols[m.pk].index)
	if !f.IsValid() {
		return 0, fmt.Errorf("db: %s has a nil primary key", m.typ)
	}
	var id int64
	switch {
	case f.CanInt():
		id = f.Int()
	case f.CanUint() && f.Uint() <= math.MaxInt64:
		id = int64(f.Uint())
	default:
		return 0, fmt.Errorf("db: embeddings need an integer primary key; %s's is %s", m.typ, f.Type())
	}
	if id == 0 {
		return 0, fmt.Errorf("db: %s has a zero primary key; Create it first", m.typ)
	}
	return id, nil
}
