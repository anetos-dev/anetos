// SPDX-License-Identifier: Apache-2.0

package docs_test

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"anetos.dev/anetos/docs"
)

func TestSite(t *testing.T) {
	n := 0
	err := fs.WalkDir(docs.Site, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".md") {
			t.Errorf("%s isn't a page", p)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Every page on disk is embedded: the embed patterns go three
	// folders deep.
	disk := 0
	err = fs.WalkDir(os.DirFS("site"), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			disk++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != disk || n < 100 {
		t.Errorf("%d pages embedded, %d in docs/site: a page deeper than the embed patterns?", n, disk)
	}
	for _, p := range []string{"README.md", "guides/queues.md", "getting-started/tutorial/README.md"} {
		if _, err := fs.Stat(docs.Site, p); err != nil {
			t.Error(err)
		}
	}
}
