// SPDX-License-Identifier: Apache-2.0

// Command validation shows Anetos's validate package: tag rules, labels and
// messages, a custom rule, a Validate method for checks that need other
// data, and the 422 response a web.H handler returns.
//
//	go run ./examples/validation
//
// It validates a sample input, prints the messages, then sends one
// request through a router and prints the JSON response.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"

	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"
)

// region: input
// SignUp is the input for POST /signup.
type SignUp struct {
	Name     string `json:"name" validate:"required|max:100"`
	Email    string `json:"email" validate:"required|email"`
	Username string `json:"username" validate:"required|between:3,20|slug"`
	Password string `json:"password" validate:"required|min:12|confirmed"`
	// PasswordConfirmation is compared by the "confirmed" rule on Password.
	PasswordConfirmation string `json:"password_confirmation"`

	Plan    string   `json:"plan" validate:"required|in:free,pro,team"`
	Company string   `json:"company" validate:"required_if:plan,team"`
	Seats   *int     `json:"seats" validate:"min:1|max:500"`
	Website string   `json:"website" label:"web site" validate:"url:https"`
	Tags    []string `json:"tags" validate:"max:5|distinct"`
	Terms   bool     `json:"terms" validate:"accepted"`
}

// endregion

// region: messages
// ValidationMessages replaces default messages for SignUp. Keys are
// "field.rule" or just "rule".
func (SignUp) ValidationMessages() map[string]string {
	return map[string]string{
		"terms.accepted": "Please accept the terms to continue.",
		"password.min":   "Use at least {0} characters for your password.",
	}
}

// endregion

// region: custom-rule
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func init() {
	validate.Register("slug", "The {label} field may only contain lowercase letters, numbers and single dashes.",
		func(ctx context.Context, f validate.Field) (bool, error) {
			s, _ := f.Value.(string)
			return slugPattern.MatchString(s), nil
		})
}

// endregion

// region: handler-check
// Users is a stand-in for a user repository.
type Users interface {
	UsernameTaken(ctx context.Context, username string) (bool, error)
}

// signUpHandler checks what tags can't: whether the username is free.
func signUpHandler(users Users) func(c *web.Ctx, in SignUp) (web.Responder, error) {
	return func(c *web.Ctx, in SignUp) (web.Responder, error) {
		taken, err := users.UsernameTaken(c, in.Username)
		if err != nil {
			return nil, err // a 500: the check itself failed
		}
		if taken {
			return nil, validate.Fail("username", "This username is already taken.") // a 422, like the tag rules
		}
		return web.Created(map[string]string{"username": in.Username}), nil
	}
}

// endregion

type memoryUsers []string

func (m memoryUsers) UsernameTaken(_ context.Context, u string) (bool, error) {
	return slices.Contains(m, u), nil
}

func main() {
	// region: standalone
	in := SignUp{
		Name:     "Sam",
		Email:    "sam@example",
		Username: "Sam_H",
		Password: "short",
		Plan:     "team",
	}
	err := validate.Struct(context.Background(), &in)
	if errs, ok := errors.AsType[*validate.Errors](err); ok {
		for _, key := range errs.Keys() {
			fmt.Printf("%-10s %s\n", key, errs.Get(key))
		}
	}
	// endregion

	fmt.Println()
	r := web.NewRouter()
	r.Post("/signup", web.H(signUpHandler(memoryUsers{"taken"})))
	body := `{"name":"Sam","email":"sam@example.com","username":"taken","password":"correct horse battery",
		"password_confirmation":"correct horse battery","plan":"free","terms":true}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/signup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	fmt.Println(rec.Code, strings.TrimSpace(rec.Body.String()))
	if rec.Code != http.StatusUnprocessableEntity {
		os.Exit(1)
	}
}
