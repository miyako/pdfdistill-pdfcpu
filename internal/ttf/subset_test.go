package ttf

import (
	"encoding/binary"
	"testing"
)

func TestComponents(t *testing.T) {
	g := make([]byte, 10)
	binary.BigEndian.PutUint16(g, 0xFFFF) // numberOfContours = -1
	// component 1: words args + scale, more components
	g = binary.BigEndian.AppendUint16(g, 0x0001|0x0008|0x0020)
	g = binary.BigEndian.AppendUint16(g, 7)
	g = append(g, 0, 0, 0, 0, 0x40, 0)
	// component 2: byte args + 2x2, last
	g = binary.BigEndian.AppendUint16(g, 0x0080)
	g = binary.BigEndian.AppendUint16(g, 42)
	g = append(g, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	got := components(g)
	if len(got) != 2 || got[0] != 7 || got[1] != 42 {
		t.Fatalf("components = %v, want [7 42]", got)
	}
	if components(g[:10]) != nil {
		t.Error("expected no components for header-only glyph")
	}
}

// buildFont creates a tiny TrueType font with 4 glyphs where glyph 3 is a
// composite of glyph 1.
func buildFont() []byte {
	simple := []byte{0, 1, 0, 0, 0, 0, 0, 10, 0, 10, 0, 0, 0, 0, 1, 0, 0}
	comp := make([]byte, 10)
	binary.BigEndian.PutUint16(comp, 0xFFFF)
	comp = binary.BigEndian.AppendUint16(comp, 0)
	comp = binary.BigEndian.AppendUint16(comp, 1)
	comp = append(comp, 0, 0)
	glyphs := [][]byte{simple, simple, simple, comp}
	var glyf []byte
	loca := []byte{}
	for _, g := range glyphs {
		loca = binary.BigEndian.AppendUint32(loca, uint32(len(glyf)))
		glyf = append(glyf, g...)
		for len(glyf)%4 != 0 {
			glyf = append(glyf, 0)
		}
	}
	loca = binary.BigEndian.AppendUint32(loca, uint32(len(glyf)))
	head := make([]byte, 54)
	binary.BigEndian.PutUint32(head[12:], 0x5F0F3CF5)
	binary.BigEndian.PutUint16(head[18:], 1000)
	binary.BigEndian.PutUint16(head[50:], 1) // long loca
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint32(maxp, 0x00005000)
	binary.BigEndian.PutUint16(maxp[4:], 4)
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:], 4)
	hmtx := make([]byte, 16)
	for i := 0; i < 4; i++ {
		binary.BigEndian.PutUint16(hmtx[4*i:], 500)
	}
	return assemble(map[string][]byte{
		"head": head, "maxp": maxp, "glyf": glyf, "loca": loca,
		"hhea": hhea, "hmtx": hmtx, "GSUB": {0, 1, 0, 0}, "cmap": minimalCmap(),
	})
}

func TestSubsetRetainsGIDs(t *testing.T) {
	font := buildFont()
	res, err := Subset(font, map[uint16]bool{3: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.NumGlyphs != 4 || res.KeptGlyphs != 3 {
		t.Errorf("NumGlyphs=%d KeptGlyphs=%d, want 4 and 3", res.NumGlyphs, res.KeptGlyphs)
	}
	want := map[uint16]bool{0: true, 1: true, 2: false, 3: true}
	for g, w := range want {
		has, err := HasOutline(res.Data, g)
		if err != nil {
			t.Fatal(err)
		}
		if has != w {
			t.Errorf("glyph %d: outline=%v, want %v", g, has, w)
		}
	}
	if len(res.Dropped) != 1 || res.Dropped[0] != "GSUB" {
		t.Errorf("dropped = %v, want [GSUB]", res.Dropped)
	}
	if checksum(res.Data) != 0xB1B0AFBA {
		t.Errorf("bad font checksum %#x", checksum(res.Data))
	}
}

func TestSubsetRejectsCFF(t *testing.T) {
	if _, err := Subset([]byte("OTTO\x00\x00\x00\x00\x00\x00\x00\x00"), nil); err == nil {
		t.Error("expected error for CFF font")
	}
}
