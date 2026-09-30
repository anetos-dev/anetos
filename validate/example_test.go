// SPDX-License-Identifier: Apache-2.0

package validate_test

import (
	"context"
	"errors"
	"fmt"

	"anetos.dev/anetos/validate"
)

type SignUp struct {
	Email    string `json:"email" validate:"required|email"`
	Password string `json:"password" validate:"required|min:12|confirmed"`
	Confirm  string `json:"password_confirmation"`
	Age      *int   `json:"age" validate:"min:18"` // optional: nil passes
}

func ExampleStruct() {
	err := validate.Struct(context.Background(), SignUp{Email: "ada@", Password: "short", Confirm: "shrt"})
	if errs, ok := errors.AsType[*validate.Errors](err); ok {
		for _, key := range errs.Keys() {
			fmt.Printf("%s: %s\n", key, errs.Get(key))
		}
	}
	// Output:
	// email: The email field must be a valid email address.
	// password: The password field must be at least 12 characters.
}
