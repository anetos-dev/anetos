package handlers

// region: imports
import (
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"

	"tracker/app/models"
	"tracker/views"
)

// endregion

// region: search

// SearchInput is the search box: GET /search?q=words.
type SearchInput struct {
	Q string `query:"q" validate:"max:200"`
}

// Search finds the issues whose title or description has the words,
// best matches first.
func (Issues) Search(c *web.Ctx, in SearchInput) (web.Responder, error) {
	var found []models.Issue
	if in.Q != "" {
		var err error
		found, err = db.Query[models.Issue](c).With(models.IssueRels.Author).Search(in.Q).Limit(50).Get()
		if err != nil {
			return nil, err
		}
	}
	return web.View(views.SearchPage(in.Q, found)), nil
}

// endregion
