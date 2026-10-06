package main

// region: imports
import (
	"testing"

	"anetos.dev/anetos/anetostest"

	"tracker/app/models"
	"tracker/database/factories"
)

// endregion

// region: test
func TestSearch(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := anetostest.Create(app, factories.Users)
	issues := factories.Issues.With(func(i *models.Issue) { i.AuthorID = ada.ID })
	anetostest.Create(app, issues.With(func(i *models.Issue) { i.Title, i.Body = "Checkout fails", "The payment form spins." }))
	anetostest.Create(app, issues.With(func(i *models.Issue) { i.Title = "Typo in the footer" }))
	anetostest.ActingAs(app, &ada)

	app.Get("/search?q=payment").AssertOK().AssertSee("Checkout fails").AssertDontSee("Typo in the footer")
	app.Get("/search?q=zebra").AssertSee("No issues match “zebra”.")
}

// endregion
