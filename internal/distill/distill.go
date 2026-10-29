// Package distill implements the pdfdistill optimisation pipeline on top of
// pdfcpu: object deduplication, TrueType font subsetting, image
// downsampling/transcoding and structural recompression.
package distill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Options controls the optimisation pipeline.
type Options struct {
	Quality  int // JPEG quality 1-100
	MaxDPI   int // 0 = never downsample
	NoImages bool
	NoSubset bool
	NoDedup  bool
	Lossless bool
	Verbose  bool
	Quiet    bool
	Log      io.Writer // diagnostics (stderr)
}

// DefaultOptions returns the CLI defaults.
func DefaultOptions() Options {
	return Options{Quality: 75, MaxDPI: 150}
}

// Stats summarises the result.
type Stats struct {
	InputSize    int
	OutputSize   int
	UsedOriginal bool
}

type category int

const (
	catFont category = iota
	catImage
	catContent
	catOther
	numCats
)

var catNames = [...]string{"fonts", "images", "content", "other"}

func (d *doc) streamBytes() (sizes [numCats]int, objects int) {
	fontFiles := map[int]bool{}
	var find func(o types.Object, depth int)
	find = func(o types.Object, depth int) {
		if depth > 16 {
			return
		}
		switch v := o.(type) {
		case types.Dict:
			for k, e := range v {
				if k == "FontFile" || k == "FontFile2" || k == "FontFile3" {
					if r := refNr(e); r != 0 {
						fontFiles[r] = true
					}
				}
				find(e, depth+1)
			}
		case types.Array:
			for _, e := range v {
				find(e, depth+1)
			}
		}
	}
	for _, nr := range d.sortedReachable() {
		find(d.xt.Table[nr].Object, 0)
	}
	for _, nr := range d.sortedReachable() {
		objects++
		sd, ok := d.xt.Table[nr].Object.(types.StreamDict)
		if !ok {
			continue
		}
		c := catOther
		switch {
		case fontFiles[nr]:
			c = catFont
		case d.name(sd.Dict["Subtype"]) == "Image":
			c = catImage
		case d.name(sd.Dict["Subtype"]) == "Form" || sd.Dict["Type"] == nil && sd.Dict["Subtype"] == nil:
			c = catContent
		}
		sizes[c] += len(sd.Raw)
	}
	return
}

func (d *doc) report(step string, prev *[numCats]int) {
	if !d.opts.Verbose {
		return
	}
	d.analyze()
	cur, objs := d.streamBytes()
	total, ptotal := 0, 0
	line := fmt.Sprintf("%-10s objects=%d", step+":", objs)
	for c := category(0); c < numCats; c++ {
		total += cur[c]
		ptotal += prev[c]
		line += fmt.Sprintf(" %s=%d", catNames[c], cur[c])
	}
	line += fmt.Sprintf(" streams=%d (%+d)", total, total-ptotal)
	d.logf("%s", line)
	*prev = cur
}

// ErrEncrypted is returned for encrypted input.
var ErrEncrypted = errors.New("encrypted PDFs are not supported")

func readContext(in []byte, dedup bool) (*model.Context, error) {
	conf := model.NewStatelessConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	conf.WriteObjectStream = true
	conf.WriteXRefStream = true
	conf.Offline = true
	if dedup {
		conf.Cmd = model.OPTIMIZE
		conf.Optimize = true
		conf.OptimizeResourceDicts = true
	} else {
		conf.Cmd = model.VALIDATE
		conf.Optimize = false
	}
	bg := context.Background()
	ctx, err := api.ReadValidateAndOptimize(bg, bytes.NewReader(in), conf, nil)
	if err == nil {
		return ctx, nil
	}
	// Fall back to an unvalidated read for slightly broken files.
	ctx, err2 := api.ReadContext(bg, bytes.NewReader(in), conf)
	if err2 != nil {
		return nil, err
	}
	if err2 = ctx.EnsurePageCount(); err2 != nil {
		return nil, err
	}
	return ctx, nil
}

// Distill optimises the PDF in in and returns the optimised bytes. If the
// optimised result is not smaller, the original bytes are returned.
func Distill(in []byte, opts Options) (out []byte, st Stats, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error: %v", r)
		}
	}()
	st.InputSize = len(in)
	if opts.Lossless {
		opts.MaxDPI = 0
	}
	ctx, err := readContext(in, !opts.NoDedup)
	if err != nil {
		return nil, st, fmt.Errorf("reading PDF: %w", err)
	}
	if ctx.Encrypt != nil || ctx.E != nil {
		return nil, st, ErrEncrypted
	}
	d := &doc{ctx: ctx, xt: ctx.XRefTable, opts: opts, st: &st, touched: map[int]bool{}}
	d.logf("input:     %d bytes, %d page(s), PDF %s", len(in), ctx.PageCount, ctx.HeaderVersion)
	d.analyze()
	var prev [numCats]int
	prev, _ = d.streamBytes()
	d.report("read", &prev)

	if !opts.NoDedup {
		n := d.dedup()
		d.logf("dedup:     %d duplicate object(s) merged", n)
		d.report("dedup", &prev)
	}

	d.analyze()
	sc := d.scan()

	if !opts.NoSubset {
		d.subsetFonts(sc)
		d.report("subset", &prev)
	}

	if !opts.NoImages {
		d.analyze()
		d.processImages(sc)
		d.report("images", &prev)
	}

	d.analyze()
	d.recompressStreams()
	d.report("structure", &prev)

	var buf bytes.Buffer
	if err := api.WriteContext(context.Background(), ctx, &buf); err != nil {
		return nil, st, fmt.Errorf("writing PDF: %w", err)
	}
	out = buf.Bytes()
	if len(out) >= len(in) {
		d.logf("output:    %d bytes is not smaller than input; keeping original", len(out))
		st.UsedOriginal = true
		out = in
	}
	st.OutputSize = len(out)
	return out, st, nil
}
