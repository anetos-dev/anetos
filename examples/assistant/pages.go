// SPDX-License-Identifier: Apache-2.0

package main

import (
	"html/template"
	"net/http"

	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
)

// render shows the named page with data, plus what every page needs: the
// CSRF token, the form's errors and old input, and the assets.
func render(c *web.Ctx, name string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	data["CSRF"] = view.CSRFToken(c)
	data["Errors"] = view.Errors(c)
	data["Old"] = func(field string) string { return view.Old(c, field) }
	data["Asset"] = assets.URL
	return c.Render(http.StatusOK, view.Template(pages, name, data))
}

// page is render as a Responder, for typed handlers.
func page(name string, data any) web.Responder {
	return web.ResponderFunc(func(c *web.Ctx) error {
		if m, ok := data.(map[string]any); ok {
			return render(c, name, m)
		}
		return c.Render(http.StatusOK, view.Template(pages, name, data))
	})
}

var pages = template.Must(template.New("").Parse(`
{{define "top"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{{.}} · Tidy help</title>
<style>body{font:16px/1.5 system-ui;max-width:42rem;margin:2rem auto;padding:0 1rem}
.msg{padding:.5rem .75rem;border-radius:.5rem;margin:.5rem 0}.user{background:#e8f0fe}.assistant{background:#f1f3f4}
.tools,.tool{color:#5f6368;font-size:.85rem}.error{color:#b3261e}textarea{width:100%}</style></head>{{end}}

{{define "login"}}{{template "top" "Log in"}}<body>
<h1>Log in</h1>
<form method="post" action="/login">
<input type="hidden" name="_token" value="{{.CSRF}}">
<label>Email <input name="email" type="email" value="{{call .Old "email"}}"></label>
{{if .Errors.Has "email"}}<p class="error">{{.Errors.Get "email"}}</p>{{end}}
<label>Password <input name="password" type="password"></label>
<button>Log in</button>
</form></body></html>{{end}}

{{define "index"}}{{template "top" "Help"}}<body>
<h1>Ask about Tidy</h1>
<form method="post" action="/chat">
<input type="hidden" name="_token" value="{{.CSRF}}">
<textarea name="prompt" rows="3" placeholder="How do I export my lists?"></textarea>
{{if .Errors.Has "prompt"}}<p class="error">{{.Errors.Get "prompt"}}</p>{{end}}
<button>Ask</button>
</form>
<h2>Your conversations</h2>
<ul>{{range .Conversations}}<li><a href="/chat/{{.ID}}">{{.Title}}</a></li>{{else}}<li>None yet.</li>{{end}}</ul>
<p class="tools">Today: {{.Tokens}} tokens.</p>
<form method="post" action="/logout"><input type="hidden" name="_token" value="{{.CSRF}}"><button>Log out</button></form>
</body></html>{{end}}

{{define "chat"}}{{template "top" .Conversation.Title}}
<body hx-headers='{"X-CSRF-Token": "{{.CSRF}}"}'>
<script src="{{call .Asset "htmx.min.js"}}"></script>
<script src="{{call .Asset "htmx-ext-sse.min.js"}}"></script>
<p><a href="/">All conversations</a></p>
<div id="messages">{{template "bubbles" .Messages}}{{if .Pending}}{{if eq .Conversation.Status "queued"}}{{template "waiting" .}}{{else}}{{template "answer" .}}{{end}}{{end}}</div>
<form id="ask" hx-post="/chat/{{.Conversation.ID}}" hx-target="#messages" hx-swap="beforeend" hx-on::after-request="this.reset()">
<textarea name="prompt" rows="2"></textarea>
<button>Ask</button>
<button hx-post="/chat/{{.Conversation.ID}}/later">Ask in the background</button>
</form>
<script>
// One question at a time: a question asked while an answer is written
// would change the conversation under it, and the answer would be lost.
document.addEventListener("htmx:sseOpen", () => { document.getElementById("ask").inert = true });
document.addEventListener("htmx:sseClose", () => { document.getElementById("ask").inert = false });
</script></body></html>{{end}}

{{define "bubbles"}}{{range .}}<div class="msg {{.Role}}">{{if .Tools}}<p class="tools">Used: {{range $i, $t := .Tools}}{{if $i}}, {{end}}{{$t}}{{end}}</p>{{end}}<p>{{.Text}}</p></div>{{end}}{{end}}

{{define "exchange"}}<div class="msg user"><p>{{.Prompt}}</p></div>{{template "answer" .}}{{end}}

{{/* The answer, streamed: htmx's SSE extension appends each "text" event, shows the "tool" and "error" ones, and closes the stream on "done". */}}
{{define "answer"}}<div class="msg assistant" hx-ext="sse" sse-connect="/chat/{{.Conversation.ID}}/reply" sse-close="done">
<p class="tool" sse-swap="tool"></p><p sse-swap="text" hx-swap="beforeend"></p><p class="error" sse-swap="error"></p></div>{{end}}

{{define "queued"}}<div class="msg user"><p>{{.Prompt}}</p></div>{{template "waiting" .}}{{end}}

{{define "waiting"}}<div class="msg assistant" hx-get="/chat/{{.Conversation.ID}}/status" hx-trigger="every 2s" hx-swap="outerHTML"><p class="tools">Working on it…</p></div>{{end}}

{{define "failed"}}<div class="msg assistant"><p class="error">{{.Error}}</p></div>{{end}}
`))
