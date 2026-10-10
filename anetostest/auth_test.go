// SPDX-License-Identifier: Apache-2.0

package anetostest_test

import (
	"strings"
	"testing"

	"anetos.dev/anetos/anetostest"
)

type stranger struct{}

func (stranger) AuthID() string       { return "x" }
func (stranger) AuthPassword() string { return "" }

func TestActingAs(t *testing.T) {
	c := &club{members: map[string]*member{"1": {ID: "1", Name: "Ada"}, "2": {ID: "2", Name: "Grace"}}}
	ft := &fakeT{TB: t}
	app := anetostest.New(ft, c.setup)
	app.Get("/me").AssertSee("guest")
	anetostest.ActingAs(app, c.members["1"]).Get("/me").AssertSee("1 Ada")
	anetostest.ActingAs(app, c.members["2"]).Get("/me").AssertSee("2 Grace") // replaces Ada

	// Users of another type than auth.New's fail the test.
	if msg := fatalOf(func() { anetostest.ActingAs(app, stranger{}) }); !strings.Contains(msg, "auth.New in setup, with users of type anetostest_test.stranger") {
		t.Errorf("another type: %q", msg)
	}
	if len(ft.errs) > 0 {
		t.Errorf("errors:\n%s", strings.Join(ft.errs, "\n"))
	}
}
