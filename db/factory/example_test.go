// SPDX-License-Identifier: Apache-2.0

package factory_test

import (
	"fmt"

	"anetos.dev/anetos/db/factory"
)

type User struct {
	Name  string
	Email string
	Admin bool
}

func ExampleFactory_With() {
	users := factory.New(func(n int) User {
		return User{Name: fmt.Sprintf("User %d", n), Email: fmt.Sprintf("user%d@example.com", n)}
	})
	admins := users.With(func(u *User) { u.Admin = true }) // a new factory; users is unchanged

	fmt.Printf("%+v\n", users.Make())
	fmt.Printf("%+v\n", admins.Make())
	// Output:
	// {Name:User 1 Email:user1@example.com Admin:false}
	// {Name:User 2 Email:user2@example.com Admin:true}
}
