// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"strconv"

	"anetos.dev/anetos/admin"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/validate"
)

// region: form
// ProductForm is what the admin edits of a product: only these fields
// can be changed, checked by their validate tags.
type ProductForm struct {
	Name       string  `json:"name" validate:"required|max:255"`
	SKU        string  `json:"sku" label:"SKU" validate:"required|max:50|alpha_dash"`
	CategoryID int64   `json:"category_id" label:"Category" admin:"select" validate:"required"`
	Price      float64 `json:"price" validate:"required|min:0" admin:"help=In dollars."`
	Stock      int     `json:"stock" validate:"min:0"`
	Status     string  `json:"status" admin:"select=draft|active|archived" validate:"required|in:draft,active,archived"`
}

// endregion

// dollars formats a price in cents.
func dollars(cents int64) string { return fmt.Sprintf("$%d.%02d", cents/100, cents%100) }

// region: products
// products is the admin's resource for products, at /admin/products.
func products() admin.Resource[Product, ProductForm] {
	return admin.Resource[Product, ProductForm]{
		Name: "products",
		Columns: []admin.Column[Product]{
			admin.Field[Product]("Name", "name"),
			admin.Field[Product]("SKU", "sku"),
			{Title: "Price", Column: "price", Sortable: true, Value: func(p Product) any { return dollars(p.Price) }},
			admin.Field[Product]("Stock", "stock"),
			admin.Field[Product]("Status", "status"),
		},
		Search:  []string{"name", "sku"},
		Filters: []admin.Filter[Product]{admin.Equals[Product]("status", "Status", admin.Choices("draft", "active", "archived")...)},
		Label:   func(p Product) string { return p.Name },
		Edit: func(p Product) ProductForm {
			return ProductForm{Name: p.Name, SKU: p.SKU, CategoryID: p.CategoryID,
				Price: float64(p.Price) / 100, Stock: p.Stock, Status: p.Status}
		},
		Apply: func(ctx context.Context, in ProductForm, p *Product) error {
			// Checks that need the database are done here.
			taken, err := db.Query[Product](ctx).WithTrashed().
				Where(db.C("sku").Eq(in.SKU), db.C("id").Ne(p.ID)).Exists()
			if err != nil {
				return err
			}
			if taken {
				return validate.Fail("sku", "Another product has this SKU.")
			}
			p.Name, p.SKU, p.CategoryID = in.Name, in.SKU, in.CategoryID
			p.Price, p.Stock, p.Status = int64(in.Price*100+0.5), in.Stock, in.Status
			return nil
		},
		// The categories to choose from, from the database.
		Choices: map[string]func(ctx context.Context) ([]admin.Choice, error){
			"category_id": categoryChoices,
		},
		Actions: []admin.Action[Product]{{
			Name: "archive", Title: "Archive", Confirm: "Archive this product?",
			When: func(p Product) bool { return p.Status != "archived" },
			Run: func(ctx context.Context, p *Product) error {
				p.Status = "archived"
				return db.Update(ctx, p)
			},
		}},
		BulkActions: []admin.BulkAction[Product]{{
			Name: "activate", Title: "Activate",
			Run: func(_ context.Context, q *db.Q[Product]) (int64, error) {
				return q.Update(db.C("status").Set("active"))
			},
		}},
	}
}

// endregion

func categoryChoices(ctx context.Context) ([]admin.Choice, error) {
	cats, err := db.Query[Category](ctx).OrderBy(db.C("name").Asc()).Get()
	if err != nil {
		return nil, err
	}
	out := make([]admin.Choice, len(cats))
	for i, c := range cats {
		out[i] = admin.Choice{Value: strconv.FormatInt(c.ID, 10), Label: c.Name}
	}
	return out, nil
}

// CategoryForm is what the admin edits of a category.
type CategoryForm struct {
	Name string `json:"name" validate:"required|max:100"`
}

// categories is the admin's resource for categories.
func categories() admin.Resource[Category, CategoryForm] {
	return admin.Resource[Category, CategoryForm]{
		Name:    "categories",
		Columns: []admin.Column[Category]{admin.Field[Category]("Name", "name"), admin.Field[Category]("Created", "created_at")},
		Search:  []string{"name"},
		Sort:    "name",
		Label:   func(c Category) string { return c.Name },
		Edit:    func(c Category) CategoryForm { return CategoryForm{Name: c.Name} },
		Apply: func(_ context.Context, in CategoryForm, c *Category) error {
			c.Name = in.Name
			return nil
		},
	}
}
