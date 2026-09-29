// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"errors"
	"fmt"
	"reflect"
)

// ErrNotProvided is wrapped by [Resolve] when no service of the requested
// type has been provided.
var ErrNotProvided = errors.New("service not provided")

// Provide registers v as the application's service of type T, replacing any
// previous value of that type. Providers typically call it in Register:
//
//	anetos.Provide[*sql.DB](app, db)
//	anetos.Provide[mail.Mailer](app, smtpMailer) // provide by interface type
//
// The container is keyed by the static type T, so Provide[Mailer] and
// Provide[*SMTPMailer] are different entries. Prefer passing dependencies
// through constructors; use the container for wiring between providers.
// Provide is safe for concurrent use.
func Provide[T any](a *App, v T) {
	a.svcMu.Lock()
	defer a.svcMu.Unlock()
	a.services[reflect.TypeFor[T]()] = v
}

// Lookup returns the service of type T and whether it was provided.
func Lookup[T any](a *App) (T, bool) {
	a.svcMu.RLock()
	defer a.svcMu.RUnlock()
	v, ok := a.services[reflect.TypeFor[T]()]
	if !ok {
		var zero T
		return zero, false
	}
	t, _ := v.(T) // a nil interface value was provided: return T's zero value
	return t, true
}

// Resolve returns the service of type T, or an error wrapping
// [ErrNotProvided] that names the missing type.
func Resolve[T any](a *App) (T, error) {
	v, ok := Lookup[T](a)
	if !ok {
		return v, fmt.Errorf("anetos: %w: %s", ErrNotProvided, reflect.TypeFor[T]())
	}
	return v, nil
}

// MustResolve is like [Resolve] but panics if the service is missing. Use it
// only during Boot, where a missing service is a programming error.
func MustResolve[T any](a *App) T {
	v, err := Resolve[T](a)
	if err != nil {
		panic(err)
	}
	return v
}
