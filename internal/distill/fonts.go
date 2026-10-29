package distill

import (
	"crypto/sha256"
	"encoding/binary"
	"regexp"
	"sort"

	"github.com/miyako/pdfdistill-pdfcpu/internal/ttf"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

var subsetTagRE = regexp.MustCompile(`^[A-Z]{6}\+`)

type ffTarget struct {
	nr      int
	gids    map[uint16]bool
	cidSets []types.Dict // font descriptors holding a /CIDSet
	names   []nameRef    // entries to receive the subset tag
	ok      bool
	reason  string
}

type nameRef struct {
	dict types.Dict
	key  string
}

// subsetFonts subsets every FontFile2 that is only used by Type0/CIDFontType2
// fonts with Identity-H/V encoding whose usage is fully known from the
// scanned content streams.
func (d *doc) subsetFonts(s *scanner) {
	if s.incomplete {
		d.warnf("font subsetting skipped: content streams could not be fully analysed")
		return
	}
	targets := map[int]*ffTarget{}
	order := []int{}
	expected := map[int]int{}
	for k, v := range s.expected {
		expected[k] = v
	}
	seen := map[uintptr]bool{}
	checkNrs := map[int]bool{}
	// note accounts for the reference from container to child, counting each
	// container only once.
	note := func(container types.Object, child types.Object) {
		id := objID(container)
		if id != 0 && seen[id] {
			return
		}
		if id != 0 {
			seen[id] = true
		}
		if nr := refNr(child); nr != 0 {
			expected[nr]++
			checkNrs[nr] = true
		}
	}

	fontIDs := make([]uintptr, 0, len(s.fonts))
	for id := range s.fonts {
		fontIDs = append(fontIDs, id)
	}
	sort.Slice(fontIDs, func(i, j int) bool { return s.fonts[fontIDs[i]].nr < s.fonts[fontIDs[j]].nr })

	for _, id := range fontIDs {
		f := s.fonts[id]
		if !f.cid {
			continue
		}
		if f.nr != 0 {
			checkNrs[f.nr] = true
		}
		t0 := f.dict
		descObj := t0["DescendantFonts"]
		descArr := d.array(descObj)
		note(t0, descObj)
		if len(descArr) != 1 {
			continue
		}
		cidObj := descArr[0]
		cidFont := d.dict(cidObj)
		note(descArr, cidObj)
		if cidFont == nil {
			continue
		}
		descriptorObj := cidFont["FontDescriptor"]
		desc := d.dict(descriptorObj)
		note(cidFont, descriptorObj)
		if desc == nil {
			continue
		}
		ffObj := desc["FontFile2"]
		ffNr := refNr(ffObj)
		note(desc, ffObj)
		if ffNr == 0 {
			continue
		}
		t := targets[ffNr]
		if t == nil {
			t = &ffTarget{nr: ffNr, gids: map[uint16]bool{}, ok: true}
			targets[ffNr] = t
			order = append(order, ffNr)
		}
		if d.name(cidFont["Subtype"]) != "CIDFontType2" {
			t.ok, t.reason = false, "not CIDFontType2"
			continue
		}
		// CID -> GID mapping
		var gidMap []byte
		switch m, _ := d.resolve(cidFont["CIDToGIDMap"]); v := m.(type) {
		case nil:
		case types.Name:
			if v != "Identity" {
				t.ok, t.reason = false, "unsupported CIDToGIDMap"
			}
		case types.StreamDict:
			b, err := d.decode(&v)
			if err != nil || !d.decodable(&v) {
				t.ok, t.reason = false, "undecodable CIDToGIDMap"
			}
			gidMap = b
		default:
			t.ok, t.reason = false, "unsupported CIDToGIDMap"
		}
		for cid := range f.codes {
			gid := cid
			if gidMap != nil {
				if int(cid)*2+1 >= len(gidMap) {
					continue
				}
				gid = binary.BigEndian.Uint16(gidMap[int(cid)*2:])
			}
			t.gids[gid] = true
		}
		if desc["CIDSet"] != nil {
			t.cidSets = append(t.cidSets, desc)
		}
		t.names = append(t.names, nameRef{t0, "BaseFont"}, nameRef{cidFont, "BaseFont"}, nameRef{desc, "FontName"})
	}

	for nr := range checkNrs {
		if d.refCount[nr] != expected[nr] {
			// The object is referenced from somewhere we did not analyse
			// (e.g. AcroForm /DR); invalidate all targets reachable from it.
			for _, t := range targets {
				if d.reaches(nr, t.nr) {
					t.ok, t.reason = false, "font referenced outside analysed content"
				}
			}
		}
	}

	for _, ffNr := range order {
		t := targets[ffNr]
		if t.ok && d.refCount[ffNr] != expected[ffNr] {
			t.ok, t.reason = false, "font program referenced outside analysed fonts"
		}
		if !t.ok {
			d.logf("  font obj %d: not subset (%s)", ffNr, t.reason)
			continue
		}
		d.subsetOne(t)
	}
}

// reaches reports whether object to is reachable from object from via
// direct structure and indirect references.
func (d *doc) reaches(from, to int) bool {
	seen := map[int]bool{}
	var stack = []int{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == to {
			return true
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		if e, ok := d.xt.Table[n]; ok && e != nil && e.Object != nil {
			forEachRef(e.Object, func(r int) { stack = append(stack, r) })
		}
	}
	return false
}

func (d *doc) subsetOne(t *ffTarget) {
	sd, _ := d.stream(*types.NewIndirectRef(t.nr, 0))
	if sd == nil || !d.decodable(sd) {
		d.logf("  font obj %d: not subset (undecodable)", t.nr)
		return
	}
	data, err := d.decode(sd)
	if err != nil {
		d.logf("  font obj %d: not subset (%v)", t.nr, err)
		return
	}
	res, err := ttf.Subset(data, t.gids)
	if err != nil {
		d.warnf("font obj %d: %v", t.nr, err)
		return
	}
	before := len(sd.Raw)
	raw := flateBest(res.Data)
	d.setStream(t.nr, sd, raw, "FlateDecode", nil)
	sd.Dict.Update("Length1", types.Integer(len(res.Data)))
	d.setObject(t.nr, *sd)
	d.touched[t.nr] = true

	tag := subsetTag(t.gids, data)
	for _, n := range t.names {
		cur := d.name(n.dict[n.key])
		if cur == "" {
			continue
		}
		n.dict[n.key] = types.Name(tag + "+" + subsetTagRE.ReplaceAllString(cur, ""))
	}
	for _, desc := range t.cidSets {
		d.updateCIDSet(desc, t)
	}
	d.logf("  font obj %d: %d/%d glyphs kept, %d -> %d bytes (dropped tables: %v)",
		t.nr, res.KeptGlyphs, res.NumGlyphs, before, len(raw), res.Dropped)
}

// updateCIDSet rewrites a PDF/A CIDSet so that it reflects the subset. With an
// identity CID->GID mapping, the used CIDs are the used GIDs plus CID 0.
func (d *doc) updateCIDSet(desc types.Dict, t *ffTarget) {
	max := 0
	for g := range t.gids {
		if int(g) > max {
			max = int(g)
		}
	}
	bits := make([]byte, max/8+1)
	bits[0] |= 0x80
	for g := range t.gids {
		bits[g/8] |= 0x80 >> (g % 8)
	}
	sd, nr := d.stream(desc["CIDSet"])
	if sd == nil || nr == 0 {
		return
	}
	d.setStream(nr, sd, flateBest(bits), "FlateDecode", nil)
	d.touched[nr] = true
}

func subsetTag(gids map[uint16]bool, font []byte) string {
	h := sha256.New()
	keys := make([]int, 0, len(gids))
	for g := range gids {
		keys = append(keys, int(g))
	}
	sort.Ints(keys)
	for _, g := range keys {
		h.Write([]byte{byte(g >> 8), byte(g)})
	}
	sum := sha256.Sum256(font[:min(len(font), 4096)])
	h.Write(sum[:])
	s := h.Sum(nil)
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = 'A' + s[i]%26
	}
	return string(tag)
}
