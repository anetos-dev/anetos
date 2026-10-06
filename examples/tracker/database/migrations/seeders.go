// SPDX-License-Identifier: Apache-2.0

package migrations

import (
	"context"
	"fmt"

	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/database/factories"
)

// Seeders fill the database with a demo (`go run . db:seed`): Ada
// (ada@example.com, an administrator) and Grace (grace@example.com),
// both with the password "correct horse", and a project of each with
// labels and issues.
var Seeders = []migrate.Seeder{
	{Name: "demo", Run: seedDemo},
}

func seedDemo(ctx context.Context) error {
	ada, err := factories.Users.With(func(u *models.User) { u.Name, u.Email = "Ada", "ada@example.com" }).Create(ctx)
	if err != nil {
		return err
	}
	grace, err := factories.Users.With(func(u *models.User) { u.Name, u.Email = "Grace", "grace@example.com" }).Create(ctx)
	if err != nil {
		return err
	}
	if err := rbac.Assign(ctx, ada.AuthID(), rbac.Global, "admin"); err != nil {
		return err
	}
	projects := []struct {
		key, name string
		owner     models.User
		member    models.User
		titles    []string
	}{
		{"WEB", "Website", ada, grace, []string{
			"The sign-up button does nothing on Safari", "Add a dark theme", "Pricing page typos",
			"Slow first load on mobile", "Search finds nothing for accented words",
		}},
		{"API", "Public API", grace, ada, []string{
			"Rate limit headers are missing", "Document pagination", "Return 404, not 500, for unknown projects",
		}},
	}
	for _, p := range projects {
		project := models.Project{Key: p.key, Name: p.name, Description: "A demo project."}
		if err := db.Create(ctx, &project); err != nil {
			return err
		}
		if err := rbac.Assign(ctx, p.owner.AuthID(), access.Project(p.key), access.Owner); err != nil {
			return err
		}
		if err := rbac.Assign(ctx, p.member.AuthID(), access.Project(p.key), access.Member); err != nil {
			return err
		}
		bug := models.Label{ProjectID: project.ID, Name: "bug", Color: "#dc2626"}
		idea := models.Label{ProjectID: project.ID, Name: "idea", Color: "#2563eb"}
		if err := db.CreateMany(ctx, []models.Label{bug, idea}); err != nil {
			return err
		}
		for i, title := range p.titles {
			issue := models.Issue{
				ProjectID: project.ID, Title: title, Body: fmt.Sprintf("Seen on %s.", p.name),
				Status: models.Open, Priority: models.Priorities[i%len(models.Priorities)], AuthorID: p.owner.ID,
			}
			if i%2 == 0 {
				issue.AssigneeID = &p.member.ID
			}
			if err := db.Create(ctx, &issue); err != nil {
				return err
			}
		}
	}
	return nil
}
