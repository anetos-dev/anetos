// SPDX-License-Identifier: Apache-2.0

package db

import (
	"errors"
	"net/http"
)

// ErrNotFound is returned by First, Find and similar methods when no row
// matches. It reports status 404 to the web package, so a handler can
// return it directly:
//
//	post, err := db.Find[Post](c, in.ID)
//	if err != nil {
//		return nil, err // 404 if the post doesn't exist
//	}
var ErrNotFound error = notFound{}

type notFound struct{}

func (notFound) Error() string   { return "db: record not found" }
func (notFound) HTTPStatus() int { return http.StatusNotFound }

// ErrNoDB is returned when a query's context has no database. Use
// [Connect] (which adds the database to every context the app creates) or
// [WithDB].
var ErrNoDB = errors.New("db: no database in context (use db.Connect, or db.WithDB in tests and scripts)")
