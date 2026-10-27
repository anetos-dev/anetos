// SPDX-License-Identifier: Apache-2.0

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
