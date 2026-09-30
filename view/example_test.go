// SPDX-License-Identifier: Apache-2.0

package view_test

import (
	"context"
	"fmt"
	"html/template"
	"io"

	"anetos.dev/anetos/view"
)

func ExampleString() {
	greeting := func(name string) view.Component {
		return view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			_, err := fmt.Fprintf(w, "<p>Hello, %s!</p>", template.HTMLEscapeString(name))
			return err
		})
	}
	html, err := view.String(context.Background(), greeting("<Ada>"))
	fmt.Println(html, err)
	// Output: <p>Hello, &lt;Ada&gt;!</p> <nil>
}
