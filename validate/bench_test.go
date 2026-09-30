// SPDX-License-Identifier: Apache-2.0

package validate

import (
	"context"
	"testing"
)

type benchInput struct {
	Name  string   `json:"name" validate:"required|max:100"`
	Email string   `json:"email" validate:"required|email"`
	Age   int      `json:"age" validate:"between:18,120"`
	Role  string   `json:"role" validate:"in:reader,editor,admin"`
	Tags  []string `json:"tags" validate:"max:5|distinct"`
}

func BenchmarkStructValid(b *testing.B) {
	in := &benchInput{Name: "Sam", Email: "sam@example.com", Age: 30, Role: "editor", Tags: []string{"a", "b"}}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if err := Struct(ctx, in); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStructInvalid(b *testing.B) {
	in := &benchInput{Email: "nope", Age: 3, Role: "owner"}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if err := Struct(ctx, in); err == nil {
			b.Fatal("expected errors")
		}
	}
}
