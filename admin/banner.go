// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"html/template"
	"io"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
)

// Banner shows, while someone acts as another user ("Act as user" on a
// user's page in the admin), whom they act as, with a button that stops
// it. Put it at the top of the app's layout, inside <body> (make:admin
// does): it renders nothing the rest of the time.
//
//	<body>
//		@admin.Banner()
func Banner() view.Component {
	return view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		html, err := bannerHTML(ctx)
		if err != nil || html == "" {
			return err
		}
		_, err = io.WriteString(w, string(html))
		return err
	})
}

// bannerHTML renders the banner, "" when no one acts as another user.
func bannerHTML(ctx context.Context) (template.HTML, error) {
	if _, ok := auth.Impersonator(ctx); !ok {
		return "", nil
	}
	s := session.From(ctx)
	data := struct{ As, By, Stop, CSRF string }{
		As: s.String(keyActingAs), By: s.String(keyActingBy), Stop: s.String(keyActingStop), CSRF: s.Token(),
	}
	if data.As == "" {
		data.As = "another user" // acting started without the admin
	}
	return part("banner", data)
}
