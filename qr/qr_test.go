// SPDX-License-Identifier: Apache-2.0

package qr

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
)

// The vectors are codes made by another encoder (python-qrcode), with
// the same mask: every module must match.
func TestVectors(t *testing.T) {
	b, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Text    string
		Level   Level
		Version int
		Mask    int
		Rows    []string // hex, padded to 4 bits
	}
	if err := json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		auto, err := Encode(v.Text, v.Level)
		if err != nil || auto.Version != v.Version || auto.Size != 17+4*v.Version {
			t.Errorf("%d bytes, level %d: version %d, %v; want %d", len(v.Text), v.Level, auto.Version, err, v.Version)
			continue
		}
		c := encode([]byte(v.Text), v.Level, v.Version, v.Mask)
		for y, row := range v.Rows {
			n, ok := new(big.Int).SetString(row, 16)
			if !ok {
				t.Fatalf("row %q", row)
			}
			pad := len(row)*4 - c.Size
			for x := range c.Size {
				if want := n.Bit(c.Size-1-x+pad) == 1; c.Dark(x, y) != want {
					t.Errorf("v%d level %d mask %d: module %d,%d is %v", v.Version, v.Level, v.Mask, x, y, !want)
					goto next
				}
			}
		}
	next:
	}
}

func TestEncodeErrors(t *testing.T) {
	if c, err := Encode(strings.Repeat("a", 2953), L); err != nil || c.Version != 40 {
		t.Errorf("2,953 bytes at L: %v", err)
	}
	if _, err := Encode(strings.Repeat("a", 2954), L); !errors.Is(err, ErrTooLong) {
		t.Errorf("2,954 bytes at L: %v", err)
	}
	if _, err := Encode(strings.Repeat("a", 1274), H); !errors.Is(err, ErrTooLong) {
		t.Errorf("1,274 bytes at H: %v", err)
	}
	if _, err := Encode("x", Level(4)); err == nil {
		t.Error("an unknown level")
	}
	if _, err := SVG(strings.Repeat("a", 5000), M, 0); !errors.Is(err, ErrTooLong) {
		t.Errorf("SVG: %v", err)
	}
}

func TestSVG(t *testing.T) {
	c, err := Encode("otpauth://totp/Anetos:ada?secret=JBSWY3DPEHPK3PXP", M)
	if err != nil {
		t.Fatal(err)
	}
	s := c.SVG(0)
	if !strings.HasPrefix(s, `<svg xmlns="http://www.w3.org/2000/svg" viewBox=`) || !strings.Contains(c.SVG(200), `width="200" height="200"`) {
		t.Error("the size")
	}
	var doc struct {
		ViewBox string `xml:"viewBox,attr"`
		Path    struct {
			D string `xml:"d,attr"`
		} `xml:"path"`
	}
	if err := xml.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatal(err)
	}
	if want := "0 0 41 41"; doc.ViewBox != want { // version 4: 33 modules, and the margin
		t.Errorf("viewBox %q", doc.ViewBox)
	}
	// One rectangle a run of dark modules, which together are the dark
	// modules.
	dark := 0
	for y := range c.Size {
		for x := range c.Size {
			if c.Dark(x, y) {
				dark++
			}
		}
	}
	covered := 0
	for seg := range strings.SplitSeq(strings.TrimSuffix(doc.Path.D, "z"), "z") {
		var x, y, w, w2 int
		if _, err := fmt.Sscanf(seg, "M%d %dh%dv1h-%d", &x, &y, &w, &w2); err != nil || w != w2 || !c.Dark(x-4, y-4) {
			t.Fatalf("segment %q: %v", seg, err)
		}
		covered += w
	}
	if covered != dark {
		t.Errorf("%d modules drawn, %d dark", covered, dark)
	}
	if c.Dark(-1, 0) || c.Dark(0, c.Size) {
		t.Error("outside the code is light")
	}
	if !c.Dark(0, 0) || c.Dark(7, 0) || !c.Dark(8, c.Size-8) {
		t.Error("the finder and the dark module")
	}
	if lines := strings.Count(c.String(), "\n"); lines != c.Size+2 {
		t.Errorf("String has %d lines", lines)
	}
}
