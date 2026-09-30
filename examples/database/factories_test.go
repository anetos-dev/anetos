// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"anetos.dev/anetos/db/factory"
)

// region: factories
// Authors makes valid authors, each with its own email.
var Authors = factory.New(func(n int) Author {
	return Author{Name: fmt.Sprintf("Author %d", n), Email: fmt.Sprintf("author%d@example.com", n)}
})

// Posts makes drafts; set AuthorID with With.
var Posts = factory.New(func(n int) Post {
	return Post{Title: fmt.Sprintf("Post %d", n), Body: "Text.", Tags: []string{}}
})

// endregion
