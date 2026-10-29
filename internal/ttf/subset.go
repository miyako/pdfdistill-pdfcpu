// Package ttf implements a minimal, dependency-free TrueType (glyf-based sfnt)
// subsetter tailored for fonts embedded in PDF files as FontFile2.
//
// The subsetter retains glyph IDs: unused glyph outlines are removed by
// emitting zero-length entries in the loca table, so glyph indices referenced
// by PDF content streams (via CIDToGIDMap /Identity) stay valid and no text
// needs to be rewritten. Tables that PDF renderers do not need are dropped.
package ttf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// ErrUnsupported is returned for font programs this package cannot subset
// (CFF-flavoured OpenType, TrueType collections, malformed data).
var ErrUnsupported = errors.New("ttf: unsupported font program")

// Result describes the outcome of a subsetting operation.
type Result struct {
	Data       []byte
	NumGlyphs  int // glyph count of the font (unchanged)
	KeptGlyphs int // glyphs with outlines retained (incl. .notdef and composite components)
	Dropped    []string
}

// keepTables lists the tables that are retained. Everything else
// (GSUB, GPOS, GDEF, BASE, JSTF, MATH, meta, DSIG, kern, hdmx, LTSH, VDMX,
// PCLT, EBDT/EBLC/EBSC, COLR/CPAL, morx, ...) is dropped: PDF renderers take
// glyph selection from the content stream and metrics from /W, so layout and
// bitmap tables are dead weight.
var keepTables = map[string]bool{
	"head": true, "hhea": true, "hmtx": true, "maxp": true,
	"loca": true, "glyf": true,
	"cvt ": true, "fpgm": true, "prep": true, "gasp": true,
	"OS/2": true, "name": true, "post": true, "cmap": true,
	"vhea": true, "vmtx": true,
}

type font struct {
	tables map[string][]byte
}

func parse(data []byte) (*font, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("%w: too short", ErrUnsupported)
	}
	ver := binary.BigEndian.Uint32(data)
	switch ver {
	case 0x00010000, 0x74727565: // 1.0, 'true'
	default:
		return nil, fmt.Errorf("%w: sfnt version %#08x", ErrUnsupported, ver)
	}
	n := int(binary.BigEndian.Uint16(data[4:]))
	if len(data) < 12+16*n {
		return nil, fmt.Errorf("%w: truncated table directory", ErrUnsupported)
	}
	f := &font{tables: map[string][]byte{}}
	for i := 0; i < n; i++ {
		rec := data[12+16*i:]
		tag := string(rec[:4])
		off := int(binary.BigEndian.Uint32(rec[8:]))
		length := int(binary.BigEndian.Uint32(rec[12:]))
		if off < 0 || length < 0 || off > len(data) || length > len(data)-off {
			return nil, fmt.Errorf("%w: table %q out of bounds", ErrUnsupported, tag)
		}
		f.tables[tag] = data[off : off+length]
	}
	for _, t := range []string{"head", "maxp", "loca", "glyf"} {
		if f.tables[t] == nil {
			return nil, fmt.Errorf("%w: missing %q table", ErrUnsupported, t)
		}
	}
	if len(f.tables["head"]) < 54 || len(f.tables["maxp"]) < 6 {
		return nil, fmt.Errorf("%w: short head/maxp", ErrUnsupported)
	}
	return f, nil
}

func (f *font) numGlyphs() int {
	return int(binary.BigEndian.Uint16(f.tables["maxp"][4:]))
}

func (f *font) locaOffsets() ([]uint32, error) {
	n := f.numGlyphs()
	loca := f.tables["loca"]
	glyfLen := uint32(len(f.tables["glyf"]))
	offs := make([]uint32, n+1)
	short := binary.BigEndian.Uint16(f.tables["head"][50:]) == 0
	for i := 0; i <= n; i++ {
		if short {
			if 2*i+2 > len(loca) {
				return nil, fmt.Errorf("%w: truncated loca", ErrUnsupported)
			}
			offs[i] = 2 * uint32(binary.BigEndian.Uint16(loca[2*i:]))
		} else {
			if 4*i+4 > len(loca) {
				return nil, fmt.Errorf("%w: truncated loca", ErrUnsupported)
			}
			offs[i] = binary.BigEndian.Uint32(loca[4*i:])
		}
	}
	for i := 0; i < n; i++ {
		if offs[i] > offs[i+1] || offs[i+1] > glyfLen {
			return nil, fmt.Errorf("%w: invalid loca entry %d", ErrUnsupported, i)
		}
	}
	return offs, nil
}

// components returns the glyph indices referenced by a composite glyph.
func components(g []byte) []uint16 {
	if len(g) < 10 || int16(binary.BigEndian.Uint16(g)) >= 0 {
		return nil
	}
	const (
		argWords  = 0x0001
		haveScale = 0x0008
		moreComps = 0x0020
		xyScale   = 0x0040
		twoByTwo  = 0x0080
		maxComps  = 1024
	)
	var out []uint16
	p := 10
	for len(out) < maxComps && p+4 <= len(g) {
		flags := binary.BigEndian.Uint16(g[p:])
		out = append(out, binary.BigEndian.Uint16(g[p+2:]))
		p += 4
		if flags&argWords != 0 {
			p += 4
		} else {
			p += 2
		}
		switch {
		case flags&haveScale != 0:
			p += 2
		case flags&xyScale != 0:
			p += 4
		case flags&twoByTwo != 0:
			p += 8
		}
		if flags&moreComps == 0 {
			break
		}
	}
	return out
}

// Subset returns a copy of the TrueType font program data in which only the
// outlines of the glyphs in used (plus .notdef and all composite components)
// are retained. Glyph IDs are preserved.
func Subset(data []byte, used map[uint16]bool) (*Result, error) {
	f, err := parse(data)
	if err != nil {
		return nil, err
	}
	n := f.numGlyphs()
	offs, err := f.locaOffsets()
	if err != nil {
		return nil, err
	}
	glyf := f.tables["glyf"]

	keep := make([]bool, n)
	var queue []uint16
	push := func(g uint16) {
		if int(g) < n && !keep[g] {
			keep[g] = true
			queue = append(queue, g)
		}
	}
	push(0)
	for g := range used {
		push(g)
	}
	for len(queue) > 0 {
		g := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, c := range components(glyf[offs[g]:offs[g+1]]) {
			push(c)
		}
	}

	newGlyf := make([]byte, 0, 1024)
	newOffs := make([]uint32, n+1)
	kept := 0
	for g := 0; g < n; g++ {
		newOffs[g] = uint32(len(newGlyf))
		if keep[g] {
			kept++
			newGlyf = append(newGlyf, glyf[offs[g]:offs[g+1]]...)
			for len(newGlyf)%4 != 0 {
				newGlyf = append(newGlyf, 0)
			}
		}
	}
	newOffs[n] = uint32(len(newGlyf))

	var newLoca []byte
	head := clone(f.tables["head"])
	if newOffs[n]/2 <= 0xFFFF {
		newLoca = make([]byte, 2*(n+1))
		for i, o := range newOffs {
			binary.BigEndian.PutUint16(newLoca[2*i:], uint16(o/2))
		}
		binary.BigEndian.PutUint16(head[50:], 0)
	} else {
		newLoca = make([]byte, 4*(n+1))
		for i, o := range newOffs {
			binary.BigEndian.PutUint32(newLoca[4*i:], o)
		}
		binary.BigEndian.PutUint16(head[50:], 1)
	}

	out := map[string][]byte{
		"head": head,
		"glyf": newGlyf,
		"loca": newLoca,
	}
	var dropped []string
	for tag, t := range f.tables {
		if _, done := out[tag]; done {
			continue
		}
		if !keepTables[tag] {
			dropped = append(dropped, tag)
			continue
		}
		switch tag {
		case "hmtx":
			out[tag] = zeroMetrics(t, f.tables["hhea"], 34, n, keep)
		case "vmtx":
			out[tag] = zeroMetrics(t, f.tables["vhea"], 34, n, keep)
		case "post":
			out[tag] = postFormat3(t)
		case "cmap":
			out[tag] = minimalCmap()
		default:
			out[tag] = t
		}
	}
	sort.Strings(dropped)
	return &Result{Data: assemble(out), NumGlyphs: n, KeptGlyphs: kept, Dropped: dropped}, nil
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }

// zeroMetrics clears advance/side-bearing values of unused glyphs. The table
// layout is unchanged, but the long runs of zeros compress very well.
func zeroMetrics(mtx, hea []byte, numOff, n int, keep []bool) []byte {
	if len(hea) < numOff+2 {
		return mtx
	}
	numLong := int(binary.BigEndian.Uint16(hea[numOff:]))
	out := clone(mtx)
	for g := 0; g < n; g++ {
		if keep[g] {
			continue
		}
		if g < numLong {
			if 4*g+4 <= len(out) {
				copy(out[4*g:4*g+4], []byte{0, 0, 0, 0})
			}
		} else {
			p := 4*numLong + 2*(g-numLong)
			if p+2 <= len(out) {
				out[p], out[p+1] = 0, 0
			}
		}
	}
	return out
}

// postFormat3 converts the post table to version 3.0 (no glyph names).
func postFormat3(t []byte) []byte {
	if len(t) < 32 {
		return t
	}
	out := clone(t[:32])
	binary.BigEndian.PutUint32(out, 0x00030000)
	return out
}

// minimalCmap returns a valid cmap with a single (3,1) format 4 subtable that
// maps nothing. CIDFontType2 fonts with /CIDToGIDMap select glyphs by GID, so
// the font's own (often huge, for CJK fonts) cmap is never consulted.
func minimalCmap() []byte {
	b := make([]byte, 0, 36)
	u16 := func(v uint16) { b = binary.BigEndian.AppendUint16(b, v) }
	u16(0) // version
	u16(1) // numTables
	u16(3) // platform
	u16(1) // encoding
	b = binary.BigEndian.AppendUint32(b, 12)
	u16(4)  // format
	u16(24) // length
	u16(0)  // language
	u16(2)  // segCountX2
	u16(2)  // searchRange
	u16(0)  // entrySelector
	u16(0)  // rangeShift
	u16(0xFFFF)
	u16(0) // reservedPad
	u16(0xFFFF)
	u16(1) // idDelta
	u16(0) // idRangeOffset
	return b
}

func checksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i < len(b); i += 4 {
		var w [4]byte
		copy(w[:], b[i:])
		sum += binary.BigEndian.Uint32(w[:])
	}
	return sum
}

func assemble(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for t := range tables {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	n := len(tags)
	entrySelector := 0
	for 1<<(entrySelector+1) <= n {
		entrySelector++
	}
	searchRange := (1 << entrySelector) * 16

	var buf []byte
	buf = binary.BigEndian.AppendUint32(buf, 0x00010000)
	buf = binary.BigEndian.AppendUint16(buf, uint16(n))
	buf = binary.BigEndian.AppendUint16(buf, uint16(searchRange))
	buf = binary.BigEndian.AppendUint16(buf, uint16(entrySelector))
	buf = binary.BigEndian.AppendUint16(buf, uint16(n*16-searchRange))

	dirStart := len(buf)
	buf = append(buf, make([]byte, 16*n)...)
	headOff := -1
	for i, tag := range tags {
		t := tables[tag]
		if tag == "head" {
			t = clone(t)
			binary.BigEndian.PutUint32(t[8:], 0) // checkSumAdjustment
			headOff = len(buf)
		}
		rec := buf[dirStart+16*i:]
		copy(rec, tag)
		binary.BigEndian.PutUint32(rec[4:], checksum(t))
		binary.BigEndian.PutUint32(rec[8:], uint32(len(buf)))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(t)))
		buf = append(buf, t...)
		for len(buf)%4 != 0 {
			buf = append(buf, 0)
		}
	}
	if headOff >= 0 {
		binary.BigEndian.PutUint32(buf[headOff+8:], 0xB1B0AFBA-checksum(buf))
	}
	return buf
}

// GlyphCount returns the number of glyphs declared in the font's maxp table.
func GlyphCount(data []byte) (int, error) {
	f, err := parse(data)
	if err != nil {
		return 0, err
	}
	return f.numGlyphs(), nil
}

// HasOutline reports whether glyph g has a non-empty outline.
func HasOutline(data []byte, g uint16) (bool, error) {
	f, err := parse(data)
	if err != nil {
		return false, err
	}
	offs, err := f.locaOffsets()
	if err != nil {
		return false, err
	}
	if int(g) >= f.numGlyphs() {
		return false, nil
	}
	return offs[g+1] > offs[g], nil
}
