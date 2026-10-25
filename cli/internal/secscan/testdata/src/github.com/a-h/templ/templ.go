// SPDX-License-Identifier: Apache-2.0

// Package templ is a stub of templ's, for the analyzer's tests.
package templ

type Component interface{}

func Raw[T ~string](html T, errs ...error) Component { return nil }
