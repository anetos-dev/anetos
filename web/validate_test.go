// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"strings"
	"testing"

	"anetos.dev/anetos/validate"
)

type register struct {
	Name  string `json:"name" validate:"required|max:10"`
	Email string `json:"email" validate:"required|email"`
	Page  int    `query:"page" validate:"min:1"`
}

var errTaken = errors.New("taken check failed")

func (r register) Validate(context.Context) error {
	switch r.Email {
	case "taken@example.com":
		var errs validate.Errors
		errs.Add("email", "This email address is already registered.")
		return errs.Err()
	case "boom@example.com":
		return errTaken
	}
	return nil
}

func TestHValidatesTags(t *testing.T) {
	r := newTestRouter()
	called := false
	r.Post("/register", H(func(c *Ctx, in register) (Responder, error) {
		called = true
		return NoContent(), nil
	}))
	post := func(target, body string) result {
		return do(t, r, "POST", target, strings.NewReader(body),
			"Content-Type", "application/json", "Accept", "application/json")
	}

	got := post("/register?page=0", `{"name":"far too long a name","email":"nope"}`)
	if got.status != 422 || called {
		t.Fatalf("status = %d called=%v: %s", got.status, called, got.body)
	}
	p := decode[problem](t, got.body)
	want := map[string]string{
		"name":  "The name field must not be greater than 10 characters.",
		"email": "The email field must be a valid email address.",
		"page":  "The page field must be at least 1.",
	}
	if p.Detail != "The given data was invalid." || len(p.Errors) != 3 {
		t.Errorf("problem = %+v", p)
	}
	for k, v := range want {
		if p.Errors[k] != v {
			t.Errorf("errors[%s] = %q, want %q", k, p.Errors[k], v)
		}
	}

	// Validate runs only after the tag rules pass, and can return
	// validate.Errors.
	got = post("/register?page=1", `{"name":"sam","email":"taken@example.com"}`)
	if got.status != 422 || !strings.Contains(got.body, "already registered") {
		t.Errorf("Validate errors: %d %s", got.status, got.body)
	}
	got = post("/register?page=1", `{"name":"sam","email":"boom@example.com"}`)
	if got.status != 500 || strings.Contains(got.body, "taken check failed") {
		t.Errorf("plain Validate error: %d %s", got.status, got.body)
	}
	got = post("/register?page=1", `{"name":"sam","email":"sam@example.com"}`)
	if got.status != 204 || !called {
		t.Errorf("valid: %d %s", got.status, got.body)
	}
}

func TestHSkipsValidateMethodWhenTagsFail(t *testing.T) {
	r := newTestRouter()
	type in struct {
		register // promotes the tags and the Validate method
	}
	r.Post("/x", H(func(c *Ctx, v in) (Responder, error) { return NoContent(), nil }))
	got := do(t, r, "POST", "/x?page=1", strings.NewReader(`{"email":"taken@example.com"}`),
		"Content-Type", "application/json", "Accept", "application/json")
	if got.status != 422 || strings.Contains(got.body, "already registered") || !strings.Contains(got.body, "name field is required") {
		t.Errorf("%d %s", got.status, got.body)
	}
}

func TestValidationErrorsInHTML(t *testing.T) {
	r := newTestRouter()
	r.Post("/register", H(func(c *Ctx, in register) (Responder, error) { return NoContent(), nil }))
	got := do(t, r, "POST", "/register?page=1", strings.NewReader(`{"name":"sam"}`),
		"Content-Type", "application/json", "Accept", "text/html")
	if got.status != 422 || !strings.Contains(got.body, "The email field is required.") {
		t.Errorf("%d %s", got.status, got.body)
	}
}

func TestHPanicsOnBadValidateTags(t *testing.T) {
	type bad struct {
		Email string `json:"email" validate:"required|emial"`
	}
	mustPanic(t, "unknown rule", func() { H(func(*Ctx, bad) (int, error) { return 0, nil }) })
}

func TestCustomRuleErrorIs500(t *testing.T) {
	validate.Register("web_test_unavailable", "", func(context.Context, validate.Field) (bool, error) {
		return false, errors.New("database unreachable")
	})
	type in struct {
		Slug string `json:"slug" validate:"web_test_unavailable"`
	}
	r := newTestRouter()
	r.Post("/x", H(func(c *Ctx, v in) (Responder, error) { return NoContent(), nil }))
	got := do(t, r, "POST", "/x", strings.NewReader(`{"slug":"a"}`),
		"Content-Type", "application/json", "Accept", "application/json")
	if got.status != 500 || strings.Contains(got.body, "unreachable") {
		t.Errorf("%d %s", got.status, got.body)
	}
}

func TestFieldErrorsInsideHTTPError(t *testing.T) {
	r := newTestRouter()
	r.Post("/x", func(c *Ctx) error {
		return &HTTPError{Status: 422, Message: "Check the form.", Err: validate.Fail("email", "Bad email.")}
	})
	got := do(t, r, "POST", "/x", nil, "Accept", "application/json")
	p := decode[problem](t, got.body)
	if got.status != 422 || p.Detail != "Check the form." || p.Errors["email"] != "Bad email." {
		t.Errorf("%d %+v", got.status, p)
	}
}
