// Package factories makes valid models for tests and seeders, one factory
// per model:
//
//	var Posts = factory.New(func(n int) models.Post {
//		return models.Post{Title: fmt.Sprintf("Post %d", n), Body: "Text."}
//	})
//
// Tests insert rows with anetostest.Create(app, factories.Posts);
// seeders with factories.Posts.CreateMany(ctx, 20).
package factories

// region: imports
import (
	"fmt"

	"anetos.dev/anetos/db/factory"

	"tracker/app/models"
)

// endregion

// region: users

// Users are users named User 1, User 2…, with addresses to match.
var Users = factory.New(func(n int) models.User {
	return models.User{Name: fmt.Sprintf("User %d", n), Email: fmt.Sprintf("user%d@example.com", n)}
})

// endregion

// region: issues

// Issues are open issues. Set AuthorID:
//
//	factories.Issues.With(func(i *models.Issue) { i.AuthorID = u.ID })
var Issues = factory.New(func(n int) models.Issue {
	return models.Issue{Title: fmt.Sprintf("Issue %d", n), Body: "Steps to reproduce.", Status: "open"}
})

// endregion
