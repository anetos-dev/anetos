// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/db"
)

// region: agent
// helpdesk answers questions about Tidy from the help center, with two
// tools over the articles: full-text search, and reading one.
var helpdesk = ai.Agent{
	Name: "helpdesk",
	Instructions: "You answer questions about Tidy, a to-do app, from its help-center articles. " +
		"Search the articles, read the best match, and answer briefly, naming the article. " +
		"If the articles don't say, say you don't know.",
	Tools:    []ai.Tool{searchArticles, readArticle},
	MaxSteps: 6,
}

// SearchInput is what the model sends to search_articles.
type SearchInput struct {
	Query string `json:"query" description:"Words to search the help center for" validate:"required|max:200"`
}

// ArticleHit is a search result for the model.
type ArticleHit struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

var searchArticles = ai.Func("search_articles", "Search the help center; returns the matching articles' IDs and titles, best first",
	func(ctx context.Context, in SearchInput) ([]ArticleHit, error) {
		found, err := db.Query[Article](ctx).Search(in.Query).Limit(5).Get()
		if err != nil {
			return nil, err
		}
		hits := make([]ArticleHit, len(found))
		for i, a := range found {
			hits[i] = ArticleHit{ID: a.ID, Title: a.Title}
		}
		return hits, nil
	})

// ReadInput is what the model sends to read_article.
type ReadInput struct {
	ID int64 `json:"id" description:"The article's ID, from search_articles" validate:"required"`
}

var readArticle = ai.Func("read_article", "Read a help-center article",
	func(ctx context.Context, in ReadInput) (Article, error) {
		return db.Find[Article](ctx, in.ID) // not found: 404, told to the model
	})

// endregion
