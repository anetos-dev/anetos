// SPDX-License-Identifier: Apache-2.0

package validate

import (
	"bytes"
	"mime/multipart"
	"reflect"
	"testing"
)

var (
	pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00")
	svgBytes = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	pdfBytes = []byte("%PDF-1.7\n%âãÏÓ\n")
)

// uploads builds file headers the way net/http parses a multipart form.
func uploads(t *testing.T, files map[string][]byte) map[string]*multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, data := range files {
		fw, err := w.CreateFormFile(name, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	form, err := multipart.NewReader(&buf, w.Boundary()).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := form.RemoveAll(); err != nil {
			t.Error(err)
		}
	})
	out := map[string]*multipart.FileHeader{}
	for name, fhs := range form.File {
		out[name] = fhs[0]
	}
	return out
}

func TestFileRules(t *testing.T) {
	f := uploads(t, map[string][]byte{
		"a.png":    pngBytes,
		"b.svg":    svgBytes,
		"c.pdf":    pdfBytes,
		"fake.png": []byte("plain text pretending"),
		"big.bin":  bytes.Repeat([]byte{0}, 3000),
	})

	type in struct {
		Avatar *multipart.FileHeader   `form:"avatar" validate:"required|image|max_size:2KB"`
		Doc    *multipart.FileHeader   `form:"doc" validate:"mimetypes:application/pdf,image/*|extensions:pdf,.PNG"`
		Photos []*multipart.FileHeader `form:"photos" validate:"max:2|image|min_size:10B"`
	}
	wantValid(t, &in{Avatar: f["a.png"], Doc: f["c.pdf"], Photos: []*multipart.FileHeader{f["a.png"]}})
	wantValid(t, &in{Avatar: f["a.png"], Doc: f["a.png"]})

	cases := []struct {
		v        in
		key, msg string
	}{
		{in{}, "avatar", "The avatar field is required."},
		{in{Avatar: f["b.svg"]}, "avatar", "The avatar field must be an image."},
		{in{Avatar: f["fake.png"]}, "avatar", "The avatar field must be an image."},
		{in{Avatar: f["big.bin"]}, "avatar", "The avatar field must be an image."},
		{in{Avatar: f["a.png"], Doc: f["b.svg"]}, "doc", "The doc field must be a file of type: application/pdf, image/*."},
		{in{Avatar: f["a.png"], Doc: f["fake.png"]}, "doc", ""},
		{in{Avatar: f["a.png"], Photos: []*multipart.FileHeader{f["a.png"], f["b.svg"]}}, "photos", "The photos field must be an image."},
		{in{Avatar: f["a.png"], Photos: []*multipart.FileHeader{f["a.png"], f["a.png"], f["a.png"]}}, "photos", "The photos field must not have more than 2 items."},
	}
	for _, c := range cases {
		wantError(t, &c.v, c.key, c.msg)
	}

	type sized struct {
		F *multipart.FileHeader `form:"f" validate:"max_size:2KB"`
		G *multipart.FileHeader `form:"g" validate:"min_size:1KB"`
		H *multipart.FileHeader `form:"h" validate:"extensions:png"`
	}
	wantError(t, &sized{F: f["big.bin"]}, "f", "The f field must not be greater than 2KB.")
	wantError(t, &sized{G: f["a.png"]}, "g", "The g field must be at least 1KB.")
	wantError(t, &sized{H: f["c.pdf"]}, "h", "The h field must have one of the following extensions: png.")
	wantValid(t, &sized{H: f["fake.png"]}) // extensions trust the name; use mimetypes or image for content
}

func TestFileRuleCompileErrors(t *testing.T) {
	type badSize struct {
		F *multipart.FileHeader `validate:"max_size:lots"`
	}
	type badType struct {
		F *multipart.FileHeader `validate:"mimetypes:image"`
	}
	if _, err := Compile(reflect.TypeFor[badSize]()); err == nil {
		t.Error("bad size accepted")
	}
	if _, err := Compile(reflect.TypeFor[badType]()); err == nil {
		t.Error("bad media type accepted")
	}
}
