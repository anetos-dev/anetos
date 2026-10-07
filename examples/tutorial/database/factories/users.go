package factories

import (
	"fmt"
	"sync"
	"time"

	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/db/factory"

	"tracker/app/models"
)

// UserPassword is the password of the users Users makes.
const UserPassword = "password"

// userHash is UserPassword's hash, made once: hashing is slow on purpose.
var userHash = sync.OnceValue(func() string {
	h, err := password.Hash(UserPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// Users makes verified users who sign in with UserPassword (anetos
// make:auth). In tests, sign one in without the login form:
//
//	ada := anetostest.Create(app, factories.Users)
//	anetostest.ActingAs(app, &ada).Get("/dashboard").AssertOK()
var Users = factory.New(func(n int) models.User {
	verified := time.Now().UTC()
	return models.User{
		Name:            fmt.Sprintf("User %d", n),
		Email:           fmt.Sprintf("user%d@example.com", n),
		Password:        userHash(),
		EmailVerifiedAt: &verified,
	}
})
