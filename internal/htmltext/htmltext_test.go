// SPDX-License-Identifier: Apache-2.0

package htmltext_test

import (
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/internal/htmltext"
)

func TestConvert(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"Hello, <b>world</b>!", "Hello, world!"},
		{"<!DOCTYPE html><html><head><title>T</title><style>p{color:red}</style></head><body><p>One</p><p>Two\n  lines</p></body></html>", "One\n\nTwo lines"},
		{"<h1>Receipt</h1><p>Thanks &amp; welcome&nbsp;back.</p>", "Receipt\n\nThanks & welcome back."},
		{"a<br>b<br/>c", "a\nb\nc"},
		{"<ul><li>One</li><li>Two<ul><li>Sub</li></ul></li></ul><ol><li>A</li><li>B</li></ol>", "- One\n- Two\n  - Sub\n\n1. A\n2. B"},
		{`<p>See <a href="https://example.com/o/1?a=1&amp;b=2">your order</a>.</p>`, "See your order (https://example.com/o/1?a=1&b=2)."},
		{`<a href="https://example.com">https://example.com</a> <a href="mailto:a@b.c">a@b.c</a> <a href="#top">top</a>`, "https://example.com a@b.c top"},
		{`<img src="x.png" alt="Logo"> <script>alert("<p>")</script>Hi`, "Logo Hi"},
		{"<table><tr><th>Item</th><th>Price</th></tr><tr><td>Lamp</td><td>$42.50</td></tr></table>", "Item Price\nLamp $42.50"},
		{"<pre>a\n  b</pre><p>c</p>", "a\n  b\n\nc"},
		{"<!-- hidden --><div>x</div><div>y</div>", "x\ny"},
		{"1 < 2 and 3 > 2", "1 < 2 and 3 > 2"},
		{`<p title="a>b">ok</p>`, "ok"},
		{"<p>unclosed", "unclosed"},
		{"<hr>after", "----\n\nafter"},
	} {
		if got := htmltext.Convert(tt.in); got != tt.want {
			t.Errorf("Convert(%q)\n got %q\nwant %q", tt.in, got, tt.want)
		}
	}
}

func TestConvertHostile(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		// Case folding that changes byte lengths doesn't shift the end
		// of skipped elements.
		{"<script>" + strings.Repeat("İ", 20) + "</SCRIPT>visible text", "visible text"},
		{"<style>\xff\xff\xff\xff</style>visible text", "visible text"},
		{"<script>a</scripts>b</script>c", "c"},
		// An unclosed tag swallows the rest, as in browsers.
		{`before <a href="x`, "before"},
		{"before <p class=x", "before"},
	} {
		if got := htmltext.Convert(tt.in); got != tt.want {
			t.Errorf("Convert(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// Deep lists don't blow the output up.
	deep := strings.Repeat("<ul>", 8000) + strings.Repeat("<li>x", 8000)
	if out := htmltext.Convert(deep); len(out) > 200_000 {
		t.Errorf("%d bytes of HTML gave %d bytes of text", len(deep), len(out))
	}
	// Linear time on input that used to be quadratic.
	for _, in := range []string{strings.Repeat("<a ", 200_000), strings.Repeat(`<a href="`, 100_000), strings.Repeat("<style>x</style>", 50_000), strings.Repeat("< ", 300_000)} {
		start := time.Now()
		htmltext.Convert(in)
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%d bytes took %s", len(in), d)
		}
	}
}

func FuzzConvert(f *testing.F) {
	f.Add("<p>Hi <a href='x'>there</a></p><ul><li>a</li></ul>")
	f.Fuzz(func(t *testing.T, s string) { htmltext.Convert(s) })
}
