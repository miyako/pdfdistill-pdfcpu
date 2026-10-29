package distill

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miyako/pdfdistill-pdfcpu/internal/ttf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"golang.org/x/image/font/sfnt"
)

const fixture = "../../testdata/ms-print-to-pdf-1.pdf"

func readFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func run(t *testing.T, in []byte, mod func(*Options)) ([]byte, Stats) {
	t.Helper()
	o := DefaultOptions()
	if mod != nil {
		mod(&o)
	}
	out, st, err := Distill(in, o)
	if err != nil {
		t.Fatalf("Distill: %v", err)
	}
	if len(out) > len(in) {
		t.Fatalf("output (%d) larger than input (%d)", len(out), len(in))
	}
	return out, st
}

// load parses and validates a PDF with pdfcpu (strict mode) and returns a doc
// for inspection.
func load(t *testing.T, b []byte) *doc {
	t.Helper()
	conf := model.NewStatelessConfiguration()
	conf.ValidationMode = model.ValidationStrict
	ctx, err := api.ReadContext(context.Background(), bytes.NewReader(b), conf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := api.ValidateContext(context.Background(), ctx); err != nil {
		t.Fatalf("pdfcpu validate: %v", err)
	}
	d := &doc{ctx: ctx, xt: ctx.XRefTable, touched: map[int]bool{}}
	d.analyze()
	return d
}

type imgSummary struct {
	nr     int
	w, h   int
	filter string
	pixels []byte // decoded samples for non-DCT images
	smask  bool
}

func images(t *testing.T, d *doc) []imgSummary {
	var out []imgSummary
	masks := map[int]bool{}
	for _, nr := range d.sortedReachable() {
		if sd, ok := d.xt.Table[nr].Object.(types.StreamDict); ok {
			if r := refNr(sd.Dict["SMask"]); r != 0 {
				masks[r] = true
			}
		}
	}
	for _, nr := range d.sortedReachable() {
		sd, ok := d.xt.Table[nr].Object.(types.StreamDict)
		if !ok || d.name(sd.Dict["Subtype"]) != "Image" {
			continue
		}
		s := imgSummary{nr: nr, w: d.intOf(sd.Dict["Width"]), h: d.intOf(sd.Dict["Height"]),
			filter: strings.Join(d.filterNames(&sd), ","), smask: masks[nr]}
		if d.decodable(&sd) {
			p, err := d.decode(&sd)
			if err != nil {
				t.Fatalf("decode image %d: %v", nr, err)
			}
			s.pixels = p
		}
		out = append(out, s)
	}
	return out
}

// fontInfo returns decoded FontFile2 programs and the BaseFont names of all
// Type0 fonts plus the set of used GIDs from scanning the content.
func fonts(t *testing.T, d *doc) (programs [][]byte, names []string, used map[uint16]bool, content []byte) {
	sc := d.scan()
	used = map[uint16]bool{}
	for _, f := range sc.fonts {
		for c := range f.codes {
			used[c] = true
		}
		names = append(names, d.name(f.dict["BaseFont"]))
		cid := d.dict(d.array(f.dict["DescendantFonts"])[0])
		desc := d.dict(cid["FontDescriptor"])
		sd, _ := d.stream(desc["FontFile2"])
		b, err := d.decode(sd)
		if err != nil {
			t.Fatal(err)
		}
		if l := d.intOf(sd.Dict["Length1"]); l != len(b) {
			t.Errorf("Length1 = %d, want %d", l, len(b))
		}
		programs = append(programs, b)
	}
	page := d.dict(d.array(d.dict(d.dict(d.xt.Root)["Pages"])["Kids"])[0])
	for _, c := range d.array(page["Contents"]) {
		sd, _ := d.stream(c)
		b, _ := d.decode(sd)
		content = append(content, b...)
	}
	if sd, _ := d.stream(page["Contents"]); sd != nil {
		content, _ = d.decode(sd)
	}
	return
}

func TestFixtureDefault(t *testing.T) {
	in := readFixture(t)
	out, st := run(t, in, nil)
	t.Logf("fixture: %d -> %d bytes (%.1f%%)", len(in), len(out), 100*float64(len(out))/float64(len(in)))

	// Size regression: target ≈120-140 KB.
	if len(out) > 140_000 {
		t.Errorf("output too large: %d bytes (want <= 140000)", len(out))
	}
	if st.UsedOriginal {
		t.Error("original returned")
	}

	src, dst := load(t, in), load(t, out)

	// Images: two RGB JPEGs with Flate SMasks, dimensions unchanged.
	imgs := images(t, dst)
	var jpegs, masks int
	for _, im := range imgs {
		switch {
		case im.smask:
			masks++
			if im.filter != "FlateDecode" {
				t.Errorf("SMask %d filter = %s, want FlateDecode", im.nr, im.filter)
			}
		case im.filter == "DCTDecode":
			jpegs++
		}
		if im.w != 512 || im.h != 512 {
			t.Errorf("image %d resized to %dx%d", im.nr, im.w, im.h)
		}
	}
	if jpegs != 2 || masks != 2 {
		t.Errorf("got %d JPEG images and %d SMasks, want 2 and 2", jpegs, masks)
	}

	// SMask pixels must be preserved exactly.
	srcMasks := map[string]bool{}
	for _, im := range images(t, src) {
		if im.smask {
			srcMasks[string(im.pixels)] = true
		}
	}
	for _, im := range imgs {
		if im.smask && !srcMasks[string(im.pixels)] {
			t.Errorf("SMask %d pixels changed", im.nr)
		}
	}

	// Fonts: subset tagged, all used glyphs retained, text unchanged.
	sp, _, sUsed, sContent := fonts(t, src)
	dp, dNames, dUsed, dContent := fonts(t, dst)
	if !bytes.Equal(sContent, dContent) {
		t.Error("content stream changed")
	}
	if len(sUsed) == 0 || len(sUsed) != len(dUsed) {
		t.Errorf("used glyphs: src %d, dst %d", len(sUsed), len(dUsed))
	}
	if len(dp) != 1 {
		t.Fatalf("got %d fonts", len(dp))
	}
	if !subsetTagRE.MatchString(dNames[0]) {
		t.Errorf("BaseFont %q lacks subset tag", dNames[0])
	}
	if a, b := len(flateBest(sp[0])), len(flateBest(dp[0])); b >= a/10 {
		t.Errorf("font not subset: %d -> %d compressed bytes", a, b)
	}
	n, _ := ttf.GlyphCount(dp[0])
	n0, _ := ttf.GlyphCount(sp[0])
	if n != n0 {
		t.Errorf("glyph count changed %d -> %d", n0, n)
	}
	f, err := sfnt.Parse(dp[0])
	if err != nil {
		t.Fatalf("subset font does not parse: %v", err)
	}
	var buf sfnt.Buffer
	kept := 0
	for g := range sUsed {
		had, _ := ttf.HasOutline(sp[0], g)
		has, _ := ttf.HasOutline(dp[0], g)
		if had != has {
			t.Errorf("glyph %d outline presence changed (%v -> %v)", g, had, has)
		}
		if has {
			kept++
			segs, err := f.LoadGlyph(&buf, sfnt.GlyphIndex(g), 1000, nil)
			if err != nil || len(segs) == 0 {
				t.Errorf("glyph %d not loadable from subset: %v", g, err)
			}
		}
	}
	if kept == 0 {
		t.Error("no used glyph has an outline")
	}
}

func TestFixtureLossless(t *testing.T) {
	in := readFixture(t)
	out, _ := run(t, in, func(o *Options) { o.Lossless = true })
	src, dst := load(t, in), load(t, out)
	want := map[string]bool{}
	for _, im := range images(t, src) {
		want[string(im.pixels)] = true
	}
	imgs := images(t, dst)
	if len(imgs) != 4 {
		t.Fatalf("got %d images, want 4", len(imgs))
	}
	for _, im := range imgs {
		if im.filter != "FlateDecode" {
			t.Errorf("image %d filter %s in lossless mode", im.nr, im.filter)
		}
		if !want[string(im.pixels)] {
			t.Errorf("image %d pixels changed in lossless mode", im.nr)
		}
	}
}

func TestFixtureNoImages(t *testing.T) {
	in := readFixture(t)
	out, _ := run(t, in, func(o *Options) { o.NoImages = true })
	src, dst := load(t, in), load(t, out)
	raw := map[string]bool{}
	for _, nr := range src.sortedReachable() {
		if sd, ok := src.xt.Table[nr].Object.(types.StreamDict); ok {
			raw[string(sd.Raw)] = true
		}
	}
	for _, im := range images(t, dst) {
		sd := dst.xt.Table[im.nr].Object.(types.StreamDict)
		if !raw[string(sd.Raw)] {
			t.Errorf("image %d was modified with NoImages", im.nr)
		}
	}
}

func TestFixtureNoSubset(t *testing.T) {
	in := readFixture(t)
	out, _ := run(t, in, func(o *Options) { o.NoSubset = true })
	sp, _, _, _ := fonts(t, load(t, in))
	dp, names, _, _ := fonts(t, load(t, out))
	if !bytes.Equal(sp[0], dp[0]) || subsetTagRE.MatchString(names[0]) {
		t.Error("font modified with NoSubset")
	}
}

func TestFixtureMaxDPI(t *testing.T) {
	in := readFixture(t)
	// The fixture images are drawn at 72 dpi: --max-dpi 36 must halve them.
	out, _ := run(t, in, func(o *Options) { o.MaxDPI = 36 })
	for _, im := range images(t, load(t, out)) {
		if im.w != 256 || im.h != 256 {
			t.Errorf("image %d: %dx%d, want 256x256", im.nr, im.w, im.h)
		}
	}
}

// --- synthetic PDFs ----------------------------------------------------------

type pdfObj struct {
	dict   string
	stream []byte
}

func buildPDF(objs []pdfObj) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n", i+1)
		if o.stream != nil {
			d := strings.TrimSuffix(o.dict, ">>")
			fmt.Fprintf(&b, "%s/Length %d>>\nstream\n", d, len(o.stream))
			b.Write(o.stream)
			b.WriteString("\nendstream")
		} else {
			b.WriteString(o.dict)
		}
		b.WriteString("\nendobj\n")
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func zlibBytes(b []byte, level int) []byte {
	var buf bytes.Buffer
	w, _ := zlib.NewWriterLevel(&buf, level)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func gradient(w, h int) []byte {
	p := make([]byte, 0, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p = append(p, byte(x*255/w), byte(y*255/h), byte((x+y)%256))
		}
	}
	return p
}

func TestDedupAndDownsample(t *testing.T) {
	const w, h = 300, 300
	pix := gradient(w, h)
	imgDict := fmt.Sprintf("<</Type/XObject/Subtype/Image/Width %d/Height %d/ColorSpace/DeviceRGB/BitsPerComponent 8/Filter/FlateDecode>>", w, h)
	content := []byte("q 72 0 0 72 0 0 cm /Im1 Do Q q 72 0 0 72 100 0 cm /Im2 Do Q")
	in := buildPDF([]pdfObj{
		{dict: "<</Type/Catalog/Pages 2 0 R>>"},
		{dict: "<</Type/Pages/Kids[3 0 R]/Count 1>>"},
		{dict: "<</Type/Page/Parent 2 0 R/MediaBox[0 0 300 300]/Resources<</XObject<</Im1 4 0 R/Im2 5 0 R>>>>/Contents 6 0 R>>"},
		{dict: imgDict, stream: zlibBytes(pix, 1)},
		{dict: imgDict, stream: zlibBytes(pix, 9)},
		{dict: "<<>>", stream: content},
	})
	// Drawn at 1 inch: 300 dpi. Downsampling to 150 dpi halves the size.
	out, _ := run(t, in, nil)
	imgs := images(t, load(t, out))
	if len(imgs) != 1 {
		t.Fatalf("got %d images after dedup, want 1", len(imgs))
	}
	if imgs[0].w != 150 || imgs[0].h != 150 {
		t.Errorf("downsampled to %dx%d, want 150x150", imgs[0].w, imgs[0].h)
	}

	// --no-dedup keeps both images; --max-dpi 0 keeps dimensions.
	out, _ = run(t, in, func(o *Options) { o.NoDedup = true; o.MaxDPI = 0 })
	imgs = images(t, load(t, out))
	if len(imgs) != 2 {
		t.Fatalf("got %d images with NoDedup, want 2", len(imgs))
	}
	for _, im := range imgs {
		if im.w != w || im.h != h {
			t.Errorf("image resized with --max-dpi 0: %dx%d", im.w, im.h)
		}
	}
}

func TestNeverLarger(t *testing.T) {
	in := buildPDF([]pdfObj{
		{dict: "<</Type/Catalog/Pages 2 0 R>>"},
		{dict: "<</Type/Pages/Kids[3 0 R]/Count 1>>"},
		{dict: "<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]>>"},
	})
	out, st := run(t, in, nil)
	if !st.UsedOriginal || !bytes.Equal(in, out) {
		t.Errorf("expected original bytes back (UsedOriginal=%v, %d -> %d)", st.UsedOriginal, len(in), len(out))
	}
}

func TestGarbageInput(t *testing.T) {
	if _, _, err := Distill([]byte("not a pdf"), DefaultOptions()); err == nil {
		t.Error("expected error for invalid input")
	}
}

// TestRendering compares rasterisations before/after with poppler's
// pdftoppm when it is installed (skipped otherwise).
func TestRendering(t *testing.T) {
	pdftoppm, err := exec.LookPath("pdftoppm")
	if err != nil {
		t.Skip("pdftoppm not installed")
	}
	in := readFixture(t)
	dir := t.TempDir()
	for name, mod := range map[string]func(*Options){
		"lossless": func(o *Options) { o.Lossless = true },
		"default":  nil,
	} {
		out, _ := run(t, in, mod)
		a := render(t, pdftoppm, dir, "in", in)
		b := render(t, pdftoppm, dir, name, out)
		if len(a) != len(b) {
			t.Fatalf("%s: raster size differs", name)
		}
		var diff, maxd int
		for i := range a {
			dv := int(a[i]) - int(b[i])
			if dv < 0 {
				dv = -dv
			}
			diff += dv
			maxd = max(maxd, dv)
		}
		mean := float64(diff) / float64(len(a))
		t.Logf("%s: mean abs diff %.4f, max %d", name, mean, maxd)
		if name == "lossless" && maxd != 0 {
			t.Errorf("lossless rendering differs (max %d)", maxd)
		}
		if mean > 1.0 {
			t.Errorf("%s: rendering differs too much (mean %.3f)", name, mean)
		}
	}
}

func render(t *testing.T, tool, dir, name string, pdf []byte) []byte {
	t.Helper()
	p := filepath.Join(dir, name+".pdf")
	if err := os.WriteFile(p, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(dir, name)
	if out, err := exec.Command(tool, "-r", "72", "-gray", "-singlefile", p, prefix).CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm: %v %s", err, out)
	}
	b, err := os.ReadFile(prefix + ".pgm")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestFontInAcroFormNotSubset appends an incremental update that references
// the fixture's font from the AcroForm default resources. Form field
// appearances may be regenerated with any glyph of that font, so it must not
// be subset.
func TestFontInAcroFormNotSubset(t *testing.T) {
	in := readFixture(t)
	idx := bytes.LastIndex(in, []byte("startxref"))
	var prev int
	fmt.Sscanf(string(in[idx+len("startxref"):]), "%d", &prev)
	var b bytes.Buffer
	b.Write(in)
	off := b.Len()
	b.WriteString("1 0 obj\n<</Type/Catalog/Pages 2 0 R/AcroForm<</Fields[19 0 R]/DA(/F1 12 Tf 0 g)/DR<</Font<</F1 11 0 R>>>>>>>>\nendobj\n")
	off19 := b.Len()
	b.WriteString("19 0 obj\n<</Type/Annot/Subtype/Widget/Rect[0 0 0 0]/FT/Tx/T(a)/V(x)/DA(/F1 12 Tf 0 g)>>\nendobj\n")
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n1 1\n%010d 00000 n \n19 1\n%010d 00000 n \ntrailer\n<</Size 20/Root 1 0 R/Info 18 0 R/Prev %d>>\nstartxref\n%d\n%%%%EOF\n", off, off19, prev, xref)
	mod := b.Bytes()

	out, _ := run(t, mod, nil)
	sp, _, _, _ := fonts(t, load(t, mod))
	dp, names, _, _ := fonts(t, load(t, out))
	if !bytes.Equal(sp[0], dp[0]) || subsetTagRE.MatchString(names[0]) {
		t.Error("font referenced from AcroForm /DR was subset")
	}
}
