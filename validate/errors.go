// SPDX-License-Identifier: Apache-2.0

package validate

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// Errors collects validation failures, at most one message per field. Keys
// are the field keys used in requests ("email", "address.city",
// "items.0.name"), so clients can show each message next to its input.
//
// *Errors implements the web package's status and field-error interfaces:
// returned from a handler, it becomes a 422 Unprocessable Entity response
// listing the messages. The methods that read accept a nil *Errors.
type Errors struct {
	keys []string
	msgs map[string]string
}

// Fail returns an error with one message for field, for checks done in
// code:
//
//	if taken {
//		return nil, validate.Fail("email", "This email address is already registered.")
//	}
func Fail(field, message string) error {
	var e Errors
	e.Add(field, message)
	return &e
}

// Add records message for field unless the field already has one.
func (e *Errors) Add(field, message string) {
	if e.msgs == nil {
		e.msgs = map[string]string{}
	}
	if _, ok := e.msgs[field]; ok {
		return
	}
	e.keys = append(e.keys, field)
	e.msgs[field] = message
}

// Has reports whether field has a message.
func (e *Errors) Has(field string) bool {
	if e == nil {
		return false
	}
	_, ok := e.msgs[field]
	return ok
}

// Get returns the message for field, or "".
func (e *Errors) Get(field string) string {
	if e == nil {
		return ""
	}
	return e.msgs[field]
}

// Len returns the number of fields with messages.
func (e *Errors) Len() int {
	if e == nil {
		return 0
	}
	return len(e.keys)
}

// Keys returns the fields with messages, in the order they were added.
func (e *Errors) Keys() []string {
	if e == nil {
		return nil
	}
	return slices.Clone(e.keys)
}

// FieldErrors returns a copy of the messages keyed by field.
func (e *Errors) FieldErrors() map[string]string {
	if e == nil {
		return nil
	}
	return maps.Clone(e.msgs)
}

// HTTPStatus returns 422 Unprocessable Entity.
func (e *Errors) HTTPStatus() int { return http.StatusUnprocessableEntity }

// Error lists the messages in order.
func (e *Errors) Error() string {
	var b strings.Builder
	b.WriteString("validation failed")
	for i, k := range e.Keys() {
		if i == 0 {
			b.WriteString(": ")
		} else {
			b.WriteString("; ")
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(e.msgs[k])
	}
	return b.String()
}

// MarshalJSON encodes the messages as an object keyed by field, in order.
func (e *Errors) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range e.Keys() {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		msg, err := json.Marshal(e.msgs[k])
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(msg)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Err returns a copy of e as an error, or nil if it has no messages. Later
// calls to Add on e don't change the returned error. Use it to return an
// *Errors you built by hand:
//
//	var errs validate.Errors
//	if taken {
//		errs.Add("email", "This email address is already registered.")
//	}
//	return errs.Err()
func (e *Errors) Err() error {
	if e.Len() == 0 {
		return nil
	}
	return &Errors{keys: slices.Clone(e.keys), msgs: maps.Clone(e.msgs)}
}
