// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/db"
)

// region: embeddings
// embeddingDims is the size of the articles' vectors: the embeddings
// table's (the migration), and what the embedding model makes. 1536 suits
// OpenAI's text-embedding-3-small and Gemini's gemini-embedding-001; for
// another model, change both (and set FixedSize if it can't shorten its
// vectors).
const embeddingDims = 1536

// articleEmbeddings is how the articles are embedded: their title and
// body, split into chunks.
var articleEmbeddings = ai.EmbeddingsConfig[Article]{
	Text:       func(a Article) string { return a.Title + "\n\n" + a.Body },
	Title:      func(a Article) string { return a.Title },
	Dimensions: embeddingDims,
}

// endregion

// region: agent
// newHelpdesk returns the agent that answers questions about Tidy from
// the help center, with two tools over the articles: a hybrid search
// (their meaning and their words), and reading one.
func newHelpdesk(articles *ai.Embeddings[Article]) ai.Agent {
	return ai.Agent{
		Name: "helpdesk",
		Instructions: "You answer questions about Tidy, a to-do app, from its help-center articles. " +
			"Search the articles, read the best match if its passage isn't enough, and answer briefly, naming the article. " +
			"If the articles don't say, say you don't know.",
		Tools: []ai.Tool{
			articles.Tool("search_articles", "Search the help center by meaning and words; returns the best articles' IDs, titles and passages", 3),
			readArticle,
		},
		MaxSteps: 6,
	}
}

// ReadInput is what the model sends to read_article.
type ReadInput struct {
	ID int64 `json:"id" description:"The article's ID, from search_articles" validate:"required"`
}

var readArticle = ai.Func("read_article", "Read a help-center article",
	func(ctx context.Context, in ReadInput) (Article, error) {
		return db.Find[Article](ctx, in.ID) // not found: 404, told to the model
	})

// endregion
