// SPDX-License-Identifier: Apache-2.0

package factory_test

import (
	"fmt"
	"sync"
	"testing"

	"anetos.dev/anetos/db/factory"
)

type user struct {
	Email string
	Admin bool
}

func TestMake(t *testing.T) {
	users := factory.New(func(n int) user { return user{Email: fmt.Sprintf("user%d@example.com", n)} })
	admins := users.With(func(u *user) { u.Admin = true })
	a, b := users.Make(), admins.Make()
	if a.Email != "user1@example.com" || a.Admin || b.Email != "user2@example.com" || !b.Admin {
		t.Errorf("%+v %+v", a, b)
	}
	named := admins.With(func(u *user) { u.Email = "boss@example.com" })
	if u := named.Make(); u.Email != "boss@example.com" || !u.Admin {
		t.Errorf("%+v", u)
	}
	if u := users.Make(); u.Admin {
		t.Error("With changed the base factory")
	}
	many := users.MakeMany(3)
	if len(many) != 3 || many[0].Email == many[1].Email {
		t.Errorf("%+v", many)
	}
	// Sequence numbers are unique under concurrency.
	seen := sync.Map{}
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if _, dup := seen.LoadOrStore(users.Make().Email, true); dup {
				t.Error("duplicate sequence number")
			}
		})
	}
	wg.Wait()
}
