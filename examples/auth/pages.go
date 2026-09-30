// SPDX-License-Identifier: Apache-2.0

package main

import (
	"html/template"
	"net/http"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
)

// render shows the named page with data, plus what every page needs: the
// CSRF token, the flashed status, the form's errors and old input, and
// the signed-in user.
func render(c *web.Ctx, name string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	data["CSRF"] = view.CSRFToken(c)
	data["Status"] = view.Flash(c, "status")
	data["Errors"] = view.Errors(c)
	data["Old"] = func(field string) string { return view.Old(c, field) }
	if u, ok := auth.User[*User](c); ok {
		data["User"] = u
	}
	return c.Render(http.StatusOK, view.Template(pages, name, data))
}

// page is a handler showing a page with the query's token (reset links)
// and the sign-in providers.
func (h Accounts) page(name string) func(c *web.Ctx) error {
	return func(c *web.Ctx) error {
		return render(c, name, map[string]any{"Token": c.Request().URL.Query().Get("token"), "Providers": h.social.Providers()})
	}
}

// Dashboard shows the account and its API tokens.
func (h Accounts) Dashboard(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	tokens, err := h.auth.Tokens(c, u)
	if err != nil {
		return err
	}
	return render(c, "dashboard", map[string]any{"Tokens": tokens, "NewToken": view.Flash(c, "token")})
}

// field passes a page's data and a field name to the "error" template.
func field(data map[string]any, name string) map[string]any {
	return map[string]any{"Errors": data["Errors"], "Field": name}
}

var pages = template.Must(template.New("").Funcs(template.FuncMap{"field": field}).Parse(`
{{define "top"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{{.}} · Accounts</title></head><body>{{end}}
{{define "status"}}{{if .Status}}<p class="status">{{.Status}}</p>{{end}}{{end}}
{{define "error"}}{{if .Errors.Has .Field}}<p class="error">{{.Errors.Get .Field}}</p>{{end}}{{end}}

{{define "register"}}{{template "top" "Register"}}
<h1>Register</h1>
<form method="post" action="/register">
<input type="hidden" name="_token" value="{{.CSRF}}">
<label>Name <input name="name" value="{{call .Old "name"}}"></label>{{template "error" (field . "name")}}
<label>Email <input name="email" type="email" value="{{call .Old "email"}}"></label>{{template "error" (field . "email")}}
<label>Password <input name="password" type="password"></label>{{template "error" (field . "password")}}
<label>Confirm password <input name="password_confirmation" type="password"></label>
<button>Register</button>
</form>
<p><a href="/login">Log in</a></p></body></html>{{end}}

{{define "login"}}{{template "top" "Log in"}}{{template "status" .}}
<h1>Log in</h1>
<form method="post" action="/login">
<input type="hidden" name="_token" value="{{.CSRF}}">
<label>Email <input name="email" type="email" value="{{call .Old "email"}}"></label>{{template "error" (field . "email")}}
<label>Password <input name="password" type="password"></label>{{template "error" (field . "password")}}
<label><input type="checkbox" name="remember" value="1"> Remember me</label>
<button>Log in</button>
</form>
{{template "error" (field . "social")}}
{{range .Providers}}<p><a href="/auth/{{.}}/redirect">Sign in with {{.}}</a></p>{{end}}
<p><a href="/forgot-password">Forgot your password?</a> · <a href="/register">Register</a></p></body></html>{{end}}

{{define "forgot"}}{{template "top" "Forgot password"}}
<h1>Forgot your password?</h1>
<form method="post" action="/forgot-password">
<input type="hidden" name="_token" value="{{.CSRF}}">
<label>Email <input name="email" type="email" value="{{call .Old "email"}}"></label>{{template "error" (field . "email")}}
<button>Email me a reset link</button>
</form></body></html>{{end}}

{{define "reset"}}{{template "top" "Reset password"}}
<h1>Choose a new password</h1>
<form method="post" action="/reset-password">
<input type="hidden" name="_token" value="{{.CSRF}}">
<input type="hidden" name="token" value="{{if .Token}}{{.Token}}{{else}}{{call .Old "token"}}{{end}}">
<label>New password <input name="password" type="password"></label>{{template "error" (field . "password")}}
<label>Confirm password <input name="password_confirmation" type="password"></label>
<button>Reset password</button>
</form></body></html>{{end}}

{{define "dashboard"}}{{template "top" "Dashboard"}}{{template "status" .}}
<h1>Hello, {{.User.Name}}</h1>
{{if not .User.EmailVerifiedAt}}<p>Please verify your email address: we sent you a link.</p>{{end}}
<form method="post" action="/logout"><input type="hidden" name="_token" value="{{.CSRF}}"><button>Log out</button></form>
<h2>API tokens</h2>
{{if .NewToken}}<p>Your new token (copy it now, it won't be shown again): <code>{{.NewToken}}</code></p>{{end}}
<ul>{{range .Tokens}}<li>{{.Name}}
<form method="post" action="/tokens/{{.ID}}/delete"><input type="hidden" name="_token" value="{{$.CSRF}}"><button>Revoke</button></form></li>{{end}}</ul>
<form method="post" action="/tokens">
<input type="hidden" name="_token" value="{{.CSRF}}">
<label>Name <input name="name"></label>{{template "error" (field . "name")}}
<button>Create token</button>
</form></body></html>{{end}}

{{define "admin"}}{{template "top" "Users"}}
<h1>Users</h1>
<ul>{{range .Users}}<li>{{.Name}} &lt;{{.Email}}&gt;</li>{{end}}</ul></body></html>{{end}}
`))
