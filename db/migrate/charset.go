// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"fmt"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
)

// mysqlTableCharset is what MySQL's CREATE TABLE takes after its columns
// when the database's default character set isn't utf8mb4.
const mysqlTableCharset = " DEFAULT CHARSET=utf8mb4"

// databaseCharset returns the default character set of d's database, as
// the server has it now (not a session's copy), through ctx's
// transaction if it has one on d.
func databaseCharset(ctx context.Context, d *db.DB) (string, error) {
	cs, err := db.RawFirst[string](db.WithDB(ctx, d),
		"SELECT DEFAULT_CHARACTER_SET_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = DATABASE()")
	if err != nil {
		return "", fmt.Errorf("reading the database's character set: %w", err)
	}
	return cs, nil
}

// tableCharset returns what a CREATE TABLE on d needs after its column
// list for the table's text to be utf8mb4 (design D254): on MySQL and
// MariaDB whose database defaults to another character set (latin1 on
// MariaDB before 11.6 without a distribution's settings), the clause
// above; otherwise nothing, so a utf8mb4 database's collation is kept.
func tableCharset(ctx context.Context, d *db.DB) (string, error) {
	if d.Dialect().Name() != "mysql" {
		return "", nil
	}
	cs, err := databaseCharset(ctx, d)
	if err != nil {
		return "", err
	}
	if cs == "utf8mb4" {
		return "", nil
	}
	return mysqlTableCharset, nil
}

// readCharset sets s.tableCharset, once. The statements that create
// tables (createSQL, recordSearchSQL) need it read first.
func (s *Schema) readCharset() error {
	if s.charsetRead {
		return nil
	}
	cs, err := tableCharset(s.ctx, s.d)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	s.tableCharset, s.charsetRead = cs, true
	return nil
}

// charsetFindings are the doctor's findings on MySQL tables (not views)
// in the app's database with text columns that aren't utf8mb4: they
// reject characters outside their set (error 1366), such as most
// non-Latin text in latin1. ascii columns are taken as chosen (UUIDs,
// codes) and binary ones hold bytes.
func charsetFindings(ctx context.Context, d *db.DB) []anetos.Finding {
	if d.Dialect().Name() != "mysql" {
		return nil
	}
	type row struct {
		Table string `db:"table_name"`
		Sets  string `db:"sets"`
	}
	rows, err := db.Raw[row](db.WithDB(ctx, d), `SELECT c.TABLE_NAME AS table_name,
	GROUP_CONCAT(DISTINCT c.CHARACTER_SET_NAME ORDER BY c.CHARACTER_SET_NAME) AS sets
FROM information_schema.COLUMNS c
JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME
WHERE c.TABLE_SCHEMA = DATABASE() AND t.TABLE_TYPE = 'BASE TABLE'
	AND c.CHARACTER_SET_NAME IS NOT NULL AND c.CHARACTER_SET_NAME NOT IN ('utf8mb4', 'ascii', 'binary')
GROUP BY c.TABLE_NAME ORDER BY c.TABLE_NAME`)
	if err != nil {
		return []anetos.Finding{{Severity: anetos.Warning, Message: "can't read the columns' character sets: " + err.Error()}}
	}
	if len(rows) == 0 {
		return nil
	}
	var tables []string
	for i, r := range rows {
		if i == 5 {
			tables = append(tables, "…")
			break
		}
		tables = append(tables, r.Table+" ("+r.Sets+")")
	}
	msg := fmt.Sprintf("%d table(s) have text columns that aren't utf8mb4: %s; they reject text outside their character set (error 1366). Convert each with ALTER TABLE <table> CONVERT TO CHARACTER SET utf8mb4 (tables linked by string foreign keys together, with FOREIGN_KEY_CHECKS=0)",
		len(rows), strings.Join(tables, ", "))
	if cs, err := databaseCharset(ctx, d); err == nil && cs != "utf8mb4" {
		msg += ", and the database with ALTER DATABASE <name> CHARACTER SET utf8mb4"
	}
	return []anetos.Finding{{Severity: anetos.Warning, Message: msg}}
}
