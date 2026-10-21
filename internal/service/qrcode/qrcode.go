// Package qrcode encodes short byte strings (an otpauth:// URI) as QR codes:
// byte mode, error correction level M, versions 1–10. Standard library only;
// the structure follows ISO/IEC 18004 and Project Nayuki's reference
// implementation.
package qrcode

import (
	"errors"
	"fmt"
	"strings"
)

// Code is a square grid of modules; true is dark.
type Code struct {
	Size    int
	Modules [][]bool
}

const maxVersion = 10

// Level M tables, indexed by version.
var (
	eccPerBlock = [...]int{0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26}
	numBlocks   = [...]int{0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5}
)

// ErrTooLong means the data does not fit in version 10.
var ErrTooLong = errors.New("qrcode: data too long")

func rawCodewords(ver int) int {
	r := (16*ver+128)*ver + 64
	if ver >= 2 {
		n := ver/7 + 2
		r -= (25*n-10)*n - 55
		if ver >= 7 {
			r -= 36
		}
	}
	return r / 8
}

func dataCodewords(ver int) int { return rawCodewords(ver) - eccPerBlock[ver]*numBlocks[ver] }

// Encode builds the smallest code that holds data.
func Encode(data []byte) (*Code, error) {
	ver := 0
	for v := 1; v <= maxVersion; v++ {
		countBits := 8
		if v >= 10 {
			countBits = 16
		}
		if 4+countBits+8*len(data) <= dataCodewords(v)*8 {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, ErrTooLong
	}

	// Bit stream: mode, count, bytes, terminator, padding.
	var bits []bool
	put := func(v, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (v>>i)&1 == 1)
		}
	}
	put(0b0100, 4)
	if ver >= 10 {
		put(len(data), 16)
	} else {
		put(len(data), 8)
	}
	for _, b := range data {
		put(int(b), 8)
	}
	capacity := dataCodewords(ver) * 8
	put(0, min(4, capacity-len(bits)))
	put(0, (8-len(bits)%8)%8)
	for pad := 0xEC; len(bits) < capacity; pad ^= 0xEC ^ 0x11 {
		put(pad, 8)
	}
	codewords := make([]byte, len(bits)/8)
	for i, b := range bits {
		if b {
			codewords[i>>3] |= 1 << (7 - i&7)
		}
	}

	c := newCode(ver)
	c.drawFunctionPatterns()
	c.drawCodewords(addECC(codewords, ver))
	best, bestPenalty := 0, -1
	for mask := 0; mask < 8; mask++ {
		c.applyMask(mask)
		c.drawFormat(mask)
		if p := c.penalty(); bestPenalty < 0 || p < bestPenalty {
			best, bestPenalty = mask, p
		}
		c.applyMask(mask) // XOR again to undo
	}
	c.applyMask(best)
	c.drawFormat(best)
	return &Code{Size: c.size, Modules: c.modules}, nil
}

type builder struct {
	ver      int
	size     int
	modules  [][]bool
	function [][]bool
}

func newCode(ver int) *builder {
	size := ver*4 + 17
	b := &builder{ver: ver, size: size}
	b.modules = make([][]bool, size)
	b.function = make([][]bool, size)
	for i := range b.modules {
		b.modules[i] = make([]bool, size)
		b.function[i] = make([]bool, size)
	}
	return b
}

// set marks a function module at column x, row y.
func (b *builder) set(x, y int, dark bool) {
	b.modules[y][x] = dark
	b.function[y][x] = true
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func alignmentPositions(ver int) []int {
	if ver == 1 {
		return nil
	}
	n := ver/7 + 2
	step := (ver*8 + n*3 + 5) / (n*4 - 4) * 2
	out := make([]int, n)
	out[0] = 6
	for i, pos := n-1, ver*4+17-7; i >= 1; i, pos = i-1, pos-step {
		out[i] = pos
	}
	return out
}

func (b *builder) drawFunctionPatterns() {
	for i := 0; i < b.size; i++ {
		b.set(6, i, i%2 == 0)
		b.set(i, 6, i%2 == 0)
	}
	finder := func(cx, cy int) {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := cx+dx, cy+dy
				if x < 0 || y < 0 || x >= b.size || y >= b.size {
					continue
				}
				d := max(abs(dx), abs(dy))
				b.set(x, y, d != 2 && d != 4)
			}
		}
	}
	finder(3, 3)
	finder(b.size-4, 3)
	finder(3, b.size-4)
	pos := alignmentPositions(b.ver)
	for i := range pos {
		for j := range pos {
			last := len(pos) - 1
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					b.set(pos[i]+dx, pos[j]+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	b.drawFormat(0) // reserves the format areas; redrawn with the real mask
	if b.ver >= 7 {
		rem := b.ver
		for i := 0; i < 12; i++ {
			rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
		}
		bits := b.ver<<12 | rem
		for i := 0; i < 18; i++ {
			dark := (bits>>i)&1 == 1
			a, c := b.size-11+i%3, i/3
			b.set(a, c, dark)
			b.set(c, a, dark)
		}
	}
}

// drawFormat writes the 15 format bits (level M = 00) for mask, twice.
func (b *builder) drawFormat(mask int) {
	data := 0<<3 | mask
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		b.set(8, i, bit(i))
	}
	b.set(8, 7, bit(6))
	b.set(8, 8, bit(7))
	b.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		b.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		b.set(b.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		b.set(8, b.size-15+i, bit(i))
	}
	b.set(8, b.size-8, true) // the dark module
}

// GF(256) arithmetic with the QR polynomial x^8+x^4+x^3+x^2+1.
func gfMul(x, y byte) byte {
	var z byte
	for i := 7; i >= 0; i-- {
		hi := z >> 7
		z = (z << 1) ^ (hi * 0x1D)
		z ^= ((y >> i) & 1) * x
	}
	return z
}

func rsDivisor(degree int) []byte {
	out := make([]byte, degree)
	out[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range out {
			out[j] = gfMul(out[j], root)
			if j+1 < len(out) {
				out[j] ^= out[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return out
}

func rsRemainder(data, divisor []byte) []byte {
	out := make([]byte, len(divisor))
	for _, b := range data {
		factor := b ^ out[0]
		copy(out, out[1:])
		out[len(out)-1] = 0
		for i, d := range divisor {
			out[i] ^= gfMul(d, factor)
		}
	}
	return out
}

// addECC splits data into blocks, appends Reed–Solomon codewords and
// interleaves them.
func addECC(data []byte, ver int) []byte {
	nb, ecc, raw := numBlocks[ver], eccPerBlock[ver], rawCodewords(ver)
	short := nb - raw%nb
	shortLen := raw / nb
	div := rsDivisor(ecc)
	var blocks [][]byte
	k := 0
	for i := 0; i < nb; i++ {
		n := shortLen - ecc
		if i >= short {
			n++
		}
		d := data[k : k+n]
		k += n
		blocks = append(blocks, append(append([]byte{}, d...), rsRemainder(d, div)...))
	}
	var out []byte
	longest := shortLen - ecc + 1
	for i := 0; i < longest; i++ {
		for _, blk := range blocks {
			if i < len(blk)-ecc {
				out = append(out, blk[i])
			}
		}
	}
	for i := 0; i < ecc; i++ {
		for _, blk := range blocks {
			out = append(out, blk[len(blk)-ecc+i])
		}
	}
	return out
}

func (b *builder) drawCodewords(data []byte) {
	i := 0
	for right := b.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < b.size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				upward := (right+1)&2 == 0
				y := vert
				if upward {
					y = b.size - 1 - vert
				}
				if !b.function[y][x] && i < len(data)*8 {
					b.modules[y][x] = (data[i>>3]>>(7-i&7))&1 == 1
					i++
				}
			}
		}
	}
}

func (b *builder) applyMask(mask int) {
	for y := 0; y < b.size; y++ {
		for x := 0; x < b.size; x++ {
			if b.function[y][x] {
				continue
			}
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
			case 7:
				flip = ((x+y)%2+x*y%3)%2 == 0
			}
			if flip {
				b.modules[y][x] = !b.modules[y][x]
			}
		}
	}
}

// penalty scores a masked symbol (lower is easier to scan).
func (b *builder) penalty() int {
	p := 0
	line := func(get func(i int) bool) string {
		var sb strings.Builder
		for i := 0; i < b.size; i++ {
			if get(i) {
				sb.WriteByte('1')
			} else {
				sb.WriteByte('0')
			}
		}
		return sb.String()
	}
	var lines []string
	for y := 0; y < b.size; y++ {
		lines = append(lines, line(func(i int) bool { return b.modules[y][i] }))
	}
	for x := 0; x < b.size; x++ {
		lines = append(lines, line(func(i int) bool { return b.modules[i][x] }))
	}
	for _, l := range lines {
		run := 1
		for i := 1; i <= len(l); i++ {
			if i < len(l) && l[i] == l[i-1] {
				run++
				continue
			}
			if run >= 5 {
				p += 3 + run - 5
			}
			run = 1
		}
		p += 40 * (strings.Count("0000"+l+"0000", "00001011101") + strings.Count("0000"+l+"0000", "10111010000"))
	}
	dark := 0
	for y := 0; y < b.size; y++ {
		for x := 0; x < b.size; x++ {
			if b.modules[y][x] {
				dark++
			}
			if x+1 < b.size && y+1 < b.size {
				c := b.modules[y][x]
				if b.modules[y][x+1] == c && b.modules[y+1][x] == c && b.modules[y+1][x+1] == c {
					p += 3
				}
			}
		}
	}
	total := b.size * b.size
	k := (abs(dark*20-total*10)+total-1)/total - 1
	return p + max(0, k)*10
}

// SVG renders the code with a four-module quiet zone, scaled to px.
func (c *Code) SVG(px int) string {
	n := c.Size + 8
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges">`, n, n, px, px)
	fmt.Fprintf(&sb, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Modules[y][x] {
				fmt.Fprintf(&sb, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	sb.WriteString(`"/></svg>`)
	return sb.String()
}
