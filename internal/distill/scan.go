package distill

import (
	"math"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

// mul returns m × n (PDF row-vector convention: apply m first, then n).
func (m matrix) mul(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func toMatrix(v []float64) (matrix, bool) {
	if len(v) != 6 {
		return identity, false
	}
	return matrix{v[0], v[1], v[2], v[3], v[4], v[5]}, true
}

// imageUse records the highest effective resolution at which an image is
// drawn. unknown is set when the image is drawn in a context whose
// transformation cannot be determined (patterns, Type3 glyphs, ...).
type imageUse struct {
	dpiX, dpiY float64
	unknown    bool
}

type fontUse struct {
	dict  types.Dict
	nr    int // object number if the font dict is indirect
	codes map[uint16]bool
	cid   bool // two-byte Identity-H/V encoding
}

type scanner struct {
	d *doc

	fonts    map[uintptr]*fontUse
	images   map[int]*imageUse
	expected map[int]int // references accounted for by scanned resource dicts

	seenRes    map[uintptr]bool
	seenType3  map[uintptr]bool
	budget     int
	incomplete bool
}

const scanBudget = 200000 // max operator-bearing content stream invocations

func (d *doc) scan() *scanner {
	s := &scanner{
		d:         d,
		fonts:     map[uintptr]*fontUse{},
		images:    map[int]*imageUse{},
		expected:  map[int]int{},
		seenRes:   map[uintptr]bool{},
		seenType3: map[uintptr]bool{},
		budget:    scanBudget,
	}
	root := d.dict(d.xt.Root)
	if root == nil {
		s.incomplete = true
		return s
	}
	visited := map[int]bool{}
	var walk func(o types.Object, res types.Dict, uu float64, depth int)
	walk = func(o types.Object, res types.Dict, uu float64, depth int) {
		if depth > 64 {
			s.incomplete = true
			return
		}
		node := d.dict(o)
		if node == nil {
			return
		}
		if nr := refNr(o); nr != 0 {
			if visited[nr] {
				return
			}
			visited[nr] = true
		}
		if r := d.dict(node["Resources"]); r != nil {
			res = r
		}
		if v, ok := d.number(node["UserUnit"]); ok && v > 0 {
			uu = v
		}
		if kids := d.array(node["Kids"]); kids != nil && d.name(node["Type"]) != "Page" {
			for _, k := range kids {
				walk(k, res, uu, depth+1)
			}
			return
		}
		s.page(node, res, uu)
	}
	walk(root["Pages"], nil, 1, 0)
	return s
}

func (s *scanner) page(page types.Dict, res types.Dict, uu float64) {
	d := s.d
	ctm := matrix{uu, 0, 0, uu, 0, 0}
	var content []byte
	cobj, _ := d.resolve(page["Contents"])
	switch v := cobj.(type) {
	case types.StreamDict:
		b, ok := s.decode(&v)
		if !ok {
			return
		}
		content = b
	case types.Array:
		for _, e := range v {
			sd, _ := d.stream(e)
			if sd == nil {
				continue
			}
			b, ok := s.decode(sd)
			if !ok {
				return
			}
			content = append(content, b...)
			content = append(content, '\n')
		}
	}
	s.run(content, res, ctm, false, 0)

	for _, a := range d.array(page["Annots"]) {
		annot := d.dict(a)
		if annot == nil {
			continue
		}
		rect := d.numbers(annot["Rect"])
		ap := d.dict(annot["AP"])
		for _, key := range []string{"N", "R", "D"} {
			apObj := ap[key]
			if apObj == nil {
				continue
			}
			if sd, _ := d.stream(apObj); sd != nil {
				s.appearance(sd, rect, ctm)
				continue
			}
			for _, st := range d.dict(apObj) {
				if sd, _ := d.stream(st); sd != nil {
					s.appearance(sd, rect, ctm)
				}
			}
		}
	}
}

// appearance processes an annotation appearance stream, computing the
// transformation that maps the form's BBox (after /Matrix) onto /Rect.
func (s *scanner) appearance(form *types.StreamDict, rect []float64, base matrix) {
	d := s.d
	m, ok := toMatrix(d.numbers(form.Dict["Matrix"]))
	if !ok {
		m = identity
	}
	bbox := d.numbers(form.Dict["BBox"])
	unknown := true
	ctm := base
	if len(bbox) == 4 && len(rect) == 4 {
		xs := []float64{}
		ys := []float64{}
		for _, p := range [][2]float64{{bbox[0], bbox[1]}, {bbox[2], bbox[1]}, {bbox[0], bbox[3]}, {bbox[2], bbox[3]}} {
			xs = append(xs, p[0]*m[0]+p[1]*m[2]+m[4])
			ys = append(ys, p[0]*m[1]+p[1]*m[3]+m[5])
		}
		x0, x1 := minmax(xs)
		y0, y1 := minmax(ys)
		rx0, rx1 := minmax([]float64{rect[0], rect[2]})
		ry0, ry1 := minmax([]float64{rect[1], rect[3]})
		if x1-x0 > 1e-9 && y1-y0 > 1e-9 {
			sx := (rx1 - rx0) / (x1 - x0)
			sy := (ry1 - ry0) / (y1 - y0)
			a := matrix{sx, 0, 0, sy, rx0 - x0*sx, ry0 - y0*sy}
			ctm = m.mul(a).mul(base)
			unknown = false
		}
	}
	res := d.dict(form.Dict["Resources"])
	b, ok := s.decode(form)
	if !ok {
		return
	}
	s.run(b, res, ctm, unknown, 1)
}

func minmax(v []float64) (float64, float64) {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, x := range v {
		lo = math.Min(lo, x)
		hi = math.Max(hi, x)
	}
	return lo, hi
}

func (s *scanner) decode(sd *types.StreamDict) ([]byte, bool) {
	if !s.d.decodable(sd) {
		s.incomplete = true
		return nil, false
	}
	b, err := s.d.decode(sd)
	if err != nil {
		s.incomplete = true
		return nil, false
	}
	return b, true
}

// noteResources accounts for the references held by a resource dictionary
// and processes resources that carry their own content (tiling patterns).
func (s *scanner) noteResources(res types.Dict, depth int) {
	if res == nil {
		return
	}
	id := mapID(res)
	if s.seenRes[id] {
		return
	}
	s.seenRes[id] = true
	d := s.d
	for _, cat := range []string{"Font", "XObject"} {
		sub := d.dict(res[cat])
		if sub == nil || s.seenRes[mapID(sub)] {
			continue
		}
		s.seenRes[mapID(sub)] = true
		for _, v := range sub {
			if nr := refNr(v); nr != 0 {
				s.expected[nr]++
			}
			if cat == "Font" {
				s.registerFont(v, res, depth)
			}
		}
	}
	for _, p := range d.dict(res["Pattern"]) {
		sd, _ := d.stream(p)
		if sd == nil || d.intOf(sd.Dict["PatternType"]) != 1 {
			continue
		}
		b, ok := s.decode(sd)
		if !ok {
			continue
		}
		s.run(b, d.dict(sd.Dict["Resources"]), identity, true, depth+1)
	}
}

type gstate struct {
	ctm  matrix
	font *fontUse
}

func (s *scanner) fontFor(res types.Dict, name string, depth int) *fontUse {
	d := s.d
	fonts := d.dict(res["Font"])
	if fonts == nil {
		return nil
	}
	return s.registerFont(fonts[name], res, depth)
}

// registerFont records a font found in a scanned resource dictionary.
func (s *scanner) registerFont(ref types.Object, res types.Dict, depth int) *fontUse {
	d := s.d
	fd := d.dict(ref)
	if fd == nil {
		return nil
	}
	id := mapID(fd)
	if f, ok := s.fonts[id]; ok {
		return f
	}
	f := &fontUse{dict: fd, nr: refNr(ref), codes: map[uint16]bool{}}
	switch d.name(fd["Subtype"]) {
	case "Type0":
		enc := d.name(fd["Encoding"])
		f.cid = enc == "Identity-H" || enc == "Identity-V"
	case "Type3":
		if !s.seenType3[id] {
			s.seenType3[id] = true
			r := d.dict(fd["Resources"])
			if r == nil {
				r = res
			}
			for _, cp := range d.dict(fd["CharProcs"]) {
				if sd, _ := d.stream(cp); sd != nil {
					if b, ok := s.decode(sd); ok {
						s.run(b, r, identity, true, depth+1)
					}
				}
			}
		}
	}
	s.fonts[id] = f
	return f
}

func (s *scanner) showText(f *fontUse, str []byte) {
	if f == nil || !f.cid {
		return
	}
	for i := 0; i+1 < len(str); i += 2 {
		f.codes[uint16(str[i])<<8|uint16(str[i+1])] = true
	}
}

func (s *scanner) recordImage(nr int, w, h float64, ctm matrix, unknown bool) {
	u := s.images[nr]
	if u == nil {
		u = &imageUse{}
		s.images[nr] = u
	}
	wpt := math.Hypot(ctm[0], ctm[1])
	hpt := math.Hypot(ctm[2], ctm[3])
	if unknown || wpt < 1e-6 || hpt < 1e-6 {
		u.unknown = true
		return
	}
	u.dpiX = math.Max(u.dpiX, w*72/wpt)
	u.dpiY = math.Max(u.dpiY, h*72/hpt)
}

func (s *scanner) run(content []byte, res types.Dict, ctm matrix, unknown bool, depth int) {
	s.budget--
	if s.budget < 0 || depth > 24 {
		s.incomplete = true
		return
	}
	d := s.d
	s.noteResources(res, depth)
	gs := gstate{ctm: ctm}
	var stack []gstate
	parseContent(content, func(op string, args []operand) {
		switch op {
		case "q":
			stack = append(stack, gs)
		case "Q":
			if n := len(stack); n > 0 {
				gs = stack[n-1]
				stack = stack[:n-1]
			}
		case "cm":
			if len(args) == 6 {
				var m matrix
				for i := range m {
					m[i] = args[i].num
				}
				gs.ctm = m.mul(gs.ctm)
			}
		case "Tf":
			if len(args) >= 1 && args[0].kind == kName {
				gs.font = s.fontFor(res, args[0].name, depth)
			}
		case "Tj", "'", "\"":
			if n := len(args); n > 0 && args[n-1].kind == kStr {
				s.showText(gs.font, args[n-1].str)
			}
		case "TJ":
			if n := len(args); n > 0 && args[n-1].kind == kArr {
				for _, e := range args[n-1].arr {
					if e.kind == kStr {
						s.showText(gs.font, e.str)
					}
				}
			}
		case "Do":
			if len(args) < 1 || args[0].kind != kName {
				return
			}
			xo := d.dict(res["XObject"])
			if xo == nil {
				return
			}
			ref := xo[args[0].name]
			sd, nr := d.stream(ref)
			if sd == nil {
				return
			}
			switch d.name(sd.Dict["Subtype"]) {
			case "Image":
				w, _ := d.number(sd.Dict["Width"])
				h, _ := d.number(sd.Dict["Height"])
				if nr != 0 {
					s.recordImage(nr, w, h, gs.ctm, unknown)
				}
			case "Form":
				m, ok := toMatrix(d.numbers(sd.Dict["Matrix"]))
				if !ok {
					m = identity
				}
				r := d.dict(sd.Dict["Resources"])
				if r == nil {
					r = res
				}
				if b, ok := s.decode(sd); ok {
					s.run(b, r, m.mul(gs.ctm), unknown, depth+1)
				}
			}
		case "gs":
			if len(args) < 1 || args[0].kind != kName {
				return
			}
			egs := d.dict(d.dict(res["ExtGState"])[args[0].name])
			if egs == nil {
				return
			}
			if sm := d.dict(egs["SMask"]); sm != nil {
				if g, _ := d.stream(sm["G"]); g != nil {
					r := d.dict(g.Dict["Resources"])
					if r == nil {
						r = res
					}
					m, ok := toMatrix(d.numbers(g.Dict["Matrix"]))
					if !ok {
						m = identity
					}
					if b, ok := s.decode(g); ok {
						s.run(b, r, m.mul(gs.ctm), unknown, depth+1)
					}
				}
			}
		}
	})
}
