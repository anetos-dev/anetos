// SPDX-License-Identifier: Apache-2.0

// Package conflict declares package-level names that the generated
// file's imports must avoid.
package conflict

import "anetos.dev/anetos/db"

// time is a package-level name, so the generated file imports the time
// package under another name.
var time = "noon"

// Note gets created_at and updated_at (time.Time) from db.Model.
type Note struct {
	db.Model
	Body string
}

var _ = time
