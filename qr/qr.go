// SPDX-License-Identifier: Apache-2.0

// Package qr draws QR codes (ISO/IEC 18004): text as a square of dark and
// light modules, as SVG for a page. It encodes text as bytes (UTF-8), in
// the smallest version (1 to 40) that holds it at the level of error
// correction asked for, with the mask that reads best.
//
//	svg, err := qr.SVG(setup.URI, qr.M, 200) // an otpauth:// URI, 200px wide
//
// See docs/site/guides/two-factor.md.
package qr

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Level is how much of a code can be damaged (or covered) and still be
// read: about 7% (L), 15% (M), 25% (Q) or 30% (H). Higher levels make
// bigger codes.
type Level int

// The levels of error correction.
const (
	L Level = iota
	M
	Q
	H
)

// ErrTooLong is returned for text that doesn't fit in a code of version
// 40 at the level asked for (2,953 bytes at L).
var ErrTooLong = errors.New("qr: the text is too long for a QR code")

// Code is a QR code: a square of Size by Size modules.
type Code struct {
	// Size is the number of modules on a side: 21 to 177.
	Size int
	// Version is the code's version, 1 to 40: its size is 17 + 4×Version.
	Version int
	// Level is its level of error correction.
	Level Level

	dark []bool // row by row
	fn   []bool // function modules, while drawing
}

// Encode returns the QR code of text at level.
func Encode(text string, level Level) (*Code, error) {
	if level < L || level > H {
		return nil, fmt.Errorf("qr: unknown level %d", level)
	}
	data := []byte(text)
	for v := 1; v <= 40; v++ {
		if bitsNeeded(v, len(data)) <= dataCodewords(v, level)*8 {
			return encode(data, level, v, -1), nil
		}
	}
	return nil, ErrTooLong
}

// SVG returns the QR code of text at level as an SVG image px pixels
// wide (0: as wide as its container), with the four modules of light
// margin readers need around it.
func SVG(text string, level Level, px int) (string, error) {
	c, err := Encode(text, level)
	if err != nil {
		return "", err
	}
	return c.SVG(px), nil
}

// Dark reports whether the module at column x, row y (from the top left,
// from 0) is dark. Outside the code, it is light.
func (c *Code) Dark(x, y int) bool {
	return x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.dark[y*c.Size+x]
}

// SVG returns the code as an SVG image px pixels wide and high, or, for
// 0, as wide as its container: a module is a unit, in a view box with
// four units of light margin. Modules are black on white, whatever the
// page's colors, so readers see them.
func (c *Code) SVG(px int) string {
	n := c.Size + 8
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" `)
	if px > 0 {
		b.WriteString(`width="` + strconv.Itoa(px) + `" height="` + strconv.Itoa(px) + `" `)
	}
	b.WriteString(`viewBox="0 0 `)
	b.WriteString(strconv.Itoa(n) + " " + strconv.Itoa(n))
	b.WriteString(`" shape-rendering="crispEdges" role="img" aria-label="QR code"><rect width="100%" height="100%" fill="#fff"/><path fill="#000" d="`)
	for y := range c.Size {
		for x := 0; x < c.Size; {
			if !c.Dark(x, y) {
				x++
				continue
			}
			start := x
			for x < c.Size && c.Dark(x, y) {
				x++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", start+4, y+4, x-start, x-start)
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

// String draws the code as text, two characters a module, for a
// terminal or a test.
func (c *Code) String() string {
	var b strings.Builder
	for y := -1; y <= c.Size; y++ {
		for x := -1; x <= c.Size; x++ {
			if c.Dark(x, y) {
				b.WriteString("██")
			} else {
				b.WriteString("  ")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// bitsNeeded is the bits of n bytes in byte mode at version v.
func bitsNeeded(v, n int) int {
	count := 8
	if v >= 10 {
		count = 16
	}
	if n >= 1<<count {
		return math.MaxInt
	}
	return 4 + count + 8*n
}

// The codewords of error correction per block, and the blocks, by level
// and version (ISO/IEC 18004 table 9).
var (
	eccPerBlock = [4][41]int{
		{-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
		{-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28},
		{-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
		{-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
	}
	eccBlocks = [4][41]int{
		{-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25},
		{-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49},
		{-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68},
		{-1, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81},
	}
	// formatLevel is a level's bits in the format information.
	formatLevel = [4]int{1, 0, 3, 2}
)

// rawModules is the modules of version v that hold data and error
// correction, remainder bits included.
func rawModules(v int) int {
	n := (16*v+128)*v + 64
	if v >= 2 {
		align := v/7 + 2
		n -= (25*align-10)*align - 55
		if v >= 7 {
			n -= 36
		}
	}
	return n
}

// dataCodewords is the data codewords of version v at level.
func dataCodewords(v int, level Level) int {
	return rawModules(v)/8 - eccPerBlock[level][v]*eccBlocks[level][v]
}

// encode draws data at level in version v, with mask (0 to 7), or the
// best mask for -1.
func encode(data []byte, level Level, v, mask int) *Code {
	// The bits: byte mode, the count, the bytes, a terminator, padding.
	var bits bitBuffer
	bits.add(4, 4)
	count := 8
	if v >= 10 {
		count = 16
	}
	bits.add(len(data), count)
	for _, b := range data {
		bits.add(int(b), 8)
	}
	capacity := dataCodewords(v, level) * 8
	bits.add(0, min(4, capacity-len(bits)))
	bits.add(0, (8-len(bits)%8)%8)
	for pad := 0xEC; len(bits) < capacity; pad ^= 0xEC ^ 0x11 {
		bits.add(pad, 8)
	}
	codewords := make([]byte, len(bits)/8)
	for i, bit := range bits {
		if bit {
			codewords[i>>3] |= 1 << (7 - i&7)
		}
	}

	c := &Code{Size: 17 + 4*v, Version: v, Level: level}
	c.dark = make([]bool, c.Size*c.Size)
	c.fn = make([]bool, c.Size*c.Size)
	c.drawFunctions()
	c.drawCodewords(withECC(codewords, v, level))
	if mask < 0 {
		best := math.MaxInt
		for m := range 8 {
			c.applyMask(m)
			c.drawFormat(m)
			if p := c.penalty(); p < best {
				mask, best = m, p
			}
			c.applyMask(m) // undone: XOR
		}
	}
	c.applyMask(mask)
	c.drawFormat(mask)
	c.fn = nil
	return c
}

type bitBuffer []bool

func (b *bitBuffer) add(v, n int) {
	for i := n - 1; i >= 0; i-- {
		*b = append(*b, v>>i&1 == 1)
	}
}

// withECC splits data into blocks, adds each one's error correction, and
// interleaves them.
func withECC(data []byte, v int, level Level) []byte {
	blocks, eccLen := eccBlocks[level][v], eccPerBlock[level][v]
	raw := rawModules(v) / 8
	short := blocks - raw%blocks // blocks of shortLen; the others have a byte more
	shortLen := raw / blocks
	div := rsDivisor(eccLen)
	out := make([][]byte, blocks)
	k := 0
	for i := range blocks {
		n := shortLen - eccLen
		if i >= short {
			n++
		}
		d := data[k : k+n]
		k += n
		b := append([]byte(nil), d...)
		if i < short {
			b = append(b, 0) // a hole, skipped when interleaving
		}
		out[i] = append(b, rsRemainder(d, div)...)
	}
	result := make([]byte, 0, raw)
	for i := range out[0] {
		for j, b := range out {
			if i != shortLen-eccLen || j >= short {
				result = append(result, b[i])
			}
		}
	}
	return result
}

// gfMul multiplies in GF(2^8) modulo x^8 + x^4 + x^3 + x^2 + 1.
func gfMul(x, y byte) byte {
	var z byte
	for i := 7; i >= 0; i-- {
		z = z<<1 ^ (z>>7)*0x1D
		z ^= (y >> i & 1) * x
	}
	return z
}

// rsDivisor is the Reed-Solomon generator polynomial of degree n,
// highest coefficient (1) left out.
func rsDivisor(n int) []byte {
	result := make([]byte, n)
	result[n-1] = 1
	root := byte(1)
	for range n {
		for j := range result {
			result[j] = gfMul(result[j], root)
			if j+1 < n {
				result[j] ^= result[j+1]
			}
		}
		root = gfMul(root, 2)
	}
	return result
}

func rsRemainder(data, div []byte) []byte {
	result := make([]byte, len(div))
	for _, b := range data {
		factor := b ^ result[0]
		copy(result, result[1:])
		result[len(result)-1] = 0
		for i, coef := range div {
			result[i] ^= gfMul(coef, factor)
		}
	}
	return result
}

func (c *Code) set(x, y int, dark bool) {
	c.dark[y*c.Size+x] = dark
	c.fn[y*c.Size+x] = true
}

// drawFunctions draws the finder, timing and alignment patterns, and
// reserves the format and version information's modules.
func (c *Code) drawFunctions() {
	n := c.Size
	for i := range n {
		c.set(6, i, i%2 == 0)
		c.set(i, 6, i%2 == 0)
	}
	for _, p := range [][2]int{{3, 3}, {n - 4, 3}, {3, n - 4}} {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := p[0]+dx, p[1]+dy
				if x >= 0 && x < n && y >= 0 && y < n {
					d := max(abs(dx), abs(dy))
					c.set(x, y, d != 2 && d != 4)
				}
			}
		}
	}
	pos := c.alignment()
	last := len(pos) - 1
	for i, x := range pos {
		for j, y := range pos {
			if i == 0 && j == 0 || i == 0 && j == last || i == last && j == 0 {
				continue // the finders
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					c.set(x+dx, y+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	c.drawFormat(0) // reserved; drawn again with the mask
	if v := c.Version; v >= 7 {
		rem := v
		for range 12 {
			rem = rem<<1 ^ (rem>>11)*0x1F25
		}
		bits := v<<12 | rem
		for i := range 18 {
			dark := bits>>i&1 == 1
			a, b := n-11+i%3, i/3
			c.set(a, b, dark)
			c.set(b, a, dark)
		}
	}
}

// alignment returns the alignment patterns' centers on each axis.
func (c *Code) alignment() []int {
	v := c.Version
	if v == 1 {
		return nil
	}
	count := v/7 + 2
	step := (v*8 + count*3 + 5) / (count*4 - 4) * 2
	pos := make([]int, count)
	pos[0] = 6
	for i, p := count-1, c.Size-7; i >= 1; i, p = i-1, p-step {
		pos[i] = p
	}
	return pos
}

// drawFormat draws the format information: the level and mask.
func (c *Code) drawFormat(mask int) {
	data := formatLevel[c.Level]<<3 | mask
	rem := data
	for range 10 {
		rem = rem<<1 ^ (rem>>9)*0x537
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return bits>>i&1 == 1 }
	n := c.Size
	for i := range 6 {
		c.set(8, i, bit(i))
	}
	c.set(8, 7, bit(6))
	c.set(8, 8, bit(7))
	c.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		c.set(14-i, 8, bit(i))
	}
	for i := range 8 {
		c.set(n-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		c.set(8, n-15+i, bit(i))
	}
	c.set(8, n-8, true) // always dark
}

// drawCodewords places the codewords in zigzag pairs of columns, from the
// bottom right.
func (c *Code) drawCodewords(data []byte) {
	n := c.Size
	i := 0
	for right := n - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5 // skip the vertical timing pattern
		}
		for vert := range n {
			for j := range 2 {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = n - 1 - vert // upwards
				}
				if !c.fn[y*n+x] && i < len(data)*8 {
					c.dark[y*n+x] = data[i>>3]>>(7-i&7)&1 == 1
					i++
				}
			}
		}
	}
}

// applyMask inverts the data modules that mask selects (twice undoes it).
func (c *Code) applyMask(mask int) {
	n := c.Size
	for y := range n {
		for x := range n {
			var flip bool
			switch mask {
			case 0:
				flip = (x+y)%2 == 0
			case 1:
				flip = y%2 == 0
			case 2:
				flip = x%3 == 0
			case 3:
				flip = (x+y)%3 == 0
			case 4:
				flip = (x/3+y/2)%2 == 0
			case 5:
				flip = x*y%2+x*y%3 == 0
			case 6:
				flip = (x*y%2+x*y%3)%2 == 0
			default:
				flip = ((x+y)%2+x*y%3)%2 == 0
			}
			if flip && !c.fn[y*n+x] {
				c.dark[y*n+x] = !c.dark[y*n+x]
			}
		}
	}
}

// penalty scores how hard the code is to read (ISO/IEC 18004 7.8.3):
// the lower, the better.
func (c *Code) penalty() int {
	n := c.Size
	result := 0
	line := func(at func(i int) bool) {
		color, run := false, 0
		var hist [7]int
		for i := range n {
			if at(i) == color {
				run++
				if run == 5 {
					result += 3
				} else if run > 5 {
					result++
				}
				continue
			}
			c.addHistory(run, &hist)
			if !color {
				result += finderLike(&hist) * 40
			}
			color, run = at(i), 1
		}
		if color {
			c.addHistory(run, &hist)
			run = 0
		}
		c.addHistory(run+n, &hist)
		result += finderLike(&hist) * 40
	}
	for y := range n {
		line(func(x int) bool { return c.dark[y*n+x] })
	}
	for x := range n {
		line(func(y int) bool { return c.dark[y*n+x] })
	}
	dark := 0
	for y := range n {
		for x := range n {
			d := c.dark[y*n+x]
			if d {
				dark++
			}
			if x < n-1 && y < n-1 && d == c.dark[y*n+x+1] && d == c.dark[(y+1)*n+x] && d == c.dark[(y+1)*n+x+1] {
				result += 3
			}
		}
	}
	total := n * n
	k := (abs(dark*20-total*10)+total-1)/total - 1
	return result + k*10
}

// addHistory pushes a run's length; the first run gets the light margin.
func (c *Code) addHistory(run int, hist *[7]int) {
	if hist[0] == 0 {
		run += c.Size
	}
	copy(hist[1:], hist[:6])
	hist[0] = run
}

// finderLike counts the 1:1:3:1:1 patterns, with light on a side, that
// the run history ends with.
func finderLike(h *[7]int) int {
	n := h[1]
	core := n > 0 && h[2] == n && h[3] == n*3 && h[4] == n && h[5] == n
	count := 0
	if core && h[0] >= n*4 && h[6] >= n {
		count++
	}
	if core && h[6] >= n*4 && h[0] >= n {
		count++
	}
	return count
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
