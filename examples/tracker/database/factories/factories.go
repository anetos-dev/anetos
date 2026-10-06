// SPDX-License-Identifier: Apache-2.0

// Package factories makes valid models for tests and seeders, one factory
// per model. Tests insert rows with anetostest.Create(app,
// factories.Users); seeders with factories.Users.CreateMany(ctx, 20).
package factories

import (
	"fmt"
	"sync"
	"time"

	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/db/factory"

	"anetos.dev/anetos/examples/tracker/app/models"
)

// Password is every factory user's password.
const Password = "correct horse"

// passwordHash is Password's hash, made once: hashing is slow on purpose.
var passwordHash = sync.OnceValue(func() string {
	h, err := password.Hash(Password)
	if err != nil {
		panic(err)
	}
	return h
})

// Users are verified users who get the emails, user1@example.com…
var Users = factory.New(func(n int) models.User {
	verified := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return models.User{
		Name: fmt.Sprintf("User %d", n), Email: fmt.Sprintf("user%d@example.com", n),
		Password: passwordHash(), EmailVerifiedAt: &verified, Notify: true,
	}
})

// Projects are projects keyed P1, P2…, without members: give roles with
// rbac.Assign(ctx, userID, access.Project(p.Key), access.Owner).
var Projects = factory.New(func(n int) models.Project {
	return models.Project{Key: fmt.Sprintf("P%d", n), Name: fmt.Sprintf("Project %d", n)}
})

// Issues are open issues of normal priority. Set ProjectID and AuthorID:
//
//	factories.Issues.With(func(i *models.Issue) { i.ProjectID, i.AuthorID = p.ID, u.ID })
var Issues = factory.New(func(n int) models.Issue {
	return models.Issue{Title: fmt.Sprintf("Issue %d", n), Body: "Steps to reproduce.", Status: models.Open, Priority: "normal"}
})
