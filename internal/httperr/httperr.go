// SPDX-License-Identifier: Apache-2.0

// Package httperr lets packages that package web imports (session) fail a
// request through the router's error handler: web sets Write to
// web.WriteError when it is initialized.
package httperr

import (
	"errors"
	"net/http"
)

// StatusError is an error with an HTTP status.
type StatusError struct {
	Status int
	Err    error
}

func (e *StatusError) Error() string { return e.Err.Error() }

// Unwrap returns the underlying error.
func (e *StatusError) Unwrap() error { return e.Err }

// HTTPStatus returns the status (web.StatusCoder).
func (e *StatusError) HTTPStatus() int { return e.Status }

// Write answers r with err. It writes a plain-text error until package web
// replaces it.
var Write = func(w http.ResponseWriter, _ *http.Request, err error) {
	status := http.StatusInternalServerError
	if se, ok := errors.AsType[*StatusError](err); ok {
		status = se.Status
	}
	http.Error(w, http.StatusText(status), status)
}
