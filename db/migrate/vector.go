// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"fmt"

	"anetos.dev/anetos/db"
)

// maxIndexedDims is the most dimensions pgvector's HNSW index takes for
// the vector type.
const maxIndexedDims = 2000

// CreateEmbeddings creates the table of table's embeddings
// (db.EmbeddingsTable: "<table>_embeddings"), which package ai fills: one
// row per chunk of a record, with record_id (table's id; the rows go with
// the record), chunk (its position), content (its text), content_hash,
// model (the embedding model's name), embedding (dims dimensions, as the
// model makes them) and timestamps, and an index for cosine distance
// where the database has one (PostgreSQL's HNSW up to 2000 dimensions;
// MariaDB's vector index; SQLite compares every vector). On MariaDB, the
// table has no foreign key: a record's chunks are deleted by a trigger on
// table ("<table>_embeddings_delete_trigger"), since cascading deletes
// would bypass the vector index. Creating it needs the TRIGGER privilege
// (and, with binary logging, SUPER or log_bin_trust_function_creators).
// A record deleted by another table's cascading foreign key doesn't fire
// it: its chunks stay until db.PruneChunks (ai:embed runs it); searches
// skip them. It needs the
// db.VectorSearch capability; on PostgreSQL it creates the vector
// extension if it isn't there, which takes a superuser (pgvector isn't a
// trusted extension): or have one run CREATE EXTENSION vector first.
//
//	return s.CreateEmbeddings("articles", 1536) // text-embedding-3-small
func (s *Schema) CreateEmbeddings(table string, dims int) error {
	if err := checkName(table); err != nil {
		return err
	}
	if dims < 1 || dims > 16000 {
		return fmt.Errorf("migrate: CreateEmbeddings(%q, %d): dimensions must be 1 to 16000", table, dims)
	}
	if err := s.d.CheckCapabilities(s.ctx, "the embeddings of "+table, db.VectorSearch); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if s.dialect == "postgres" {
		if err := s.Exec("CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
			return err
		}
	}
	name := db.EmbeddingsTable(table)
	if err := s.Create(name, func(t *Table) {
		t.ID()
		if s.dialect == "mysql" {
			t.ForeignID("record_id") // no foreign key: a trigger, below
		} else {
			t.ForeignID("record_id").References(table).CascadeOnDelete()
		}
		t.Integer("chunk")
		t.LongText("content")
		t.String("content_hash", 64)
		t.String("model", 100)
		t.Vector("embedding", dims)
		t.Timestamps()
		t.Unique("record_id", "chunk")
	}); err != nil {
		return err
	}
	index := s.q(indexName(name, []string{"embedding"}, "index"))
	switch {
	case s.dialect == "postgres" && dims <= maxIndexedDims:
		return s.Exec("CREATE INDEX " + index + " ON " + s.q(name) + " USING hnsw (" + s.q("embedding") + " vector_cosine_ops)")
	case s.dialect == "mysql":
		// InnoDB's cascading deletes bypass MariaDB's vector indexes, which
		// then miss rows: a trigger's deletes go through them. (MySQL's DDL
		// isn't transactional: on failure, drop what was made.)
		err := s.Exec("ALTER TABLE " + s.q(name) + " ADD VECTOR INDEX " + index + " (" + s.q("embedding") + ") DISTANCE=cosine")
		if err == nil {
			err = s.Exec("CREATE TRIGGER " + s.q(embeddingsTrigger(table)) + " AFTER DELETE ON " + s.q(table) +
				" FOR EACH ROW DELETE FROM " + s.q(name) + " WHERE " + s.q("record_id") + " = OLD." + s.q("id"))
		}
		if err != nil {
			_ = s.DropIfExists(name)
			return err
		}
	}
	return nil
}

// embeddingsTrigger names the MariaDB trigger that deletes a record's
// chunks.
func embeddingsTrigger(table string) string {
	return indexName(db.EmbeddingsTable(table), []string{"delete"}, "trigger")
}

// DropEmbeddings drops table's embeddings table, if it exists (and, on
// MariaDB, the trigger that deletes a record's chunks).
func (s *Schema) DropEmbeddings(table string) error {
	if s.dialect == "mysql" {
		if err := s.Exec("DROP TRIGGER IF EXISTS " + s.q(embeddingsTrigger(table))); err != nil {
			return err
		}
	}
	return s.DropIfExists(db.EmbeddingsTable(table))
}
