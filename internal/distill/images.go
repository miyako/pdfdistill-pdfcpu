package distill

import (
	"bytes"
	"image"
	"image/jpeg"
	"math"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"golang.org/x/image/draw"
)

// imgInfo describes an image XObject in terms relevant for re-encoding.
type imgInfo struct {
	w, h   int
	bpc    int
	ncomp  int  // 0 if the color space is not supported for transcoding
	plain  bool // no Decode array / color key mask that would forbid lossy changes
	isMask bool // used as /SMask or /Mask of another image
}

func (d *doc) colorComponents(cs types.Object) int {
	r, _ := d.resolve(cs)
	switch v := r.(type) {
	case types.Name:
		switch v {
		case "DeviceRGB":
			return 3
		case "DeviceGray":
			return 1
		}
	case types.Array:
		if len(v) < 1 {
			return 0
		}
		switch d.name(v[0]) {
		case "CalRGB":
			return 3
		case "CalGray":
			return 1
		case "ICCBased":
			if len(v) < 2 {
				return 0
			}
			if sd, _ := d.stream(v[1]); sd != nil {
				n := d.intOf(sd.Dict["N"])
				if n == 1 || n == 3 {
					return n
				}
			}
		}
	}
	return 0
}

func (d *doc) imageInfo(sd *types.StreamDict) imgInfo {
	info := imgInfo{
		w:   d.intOf(sd.Dict["Width"]),
		h:   d.intOf(sd.Dict["Height"]),
		bpc: d.intOf(sd.Dict["BitsPerComponent"]),
	}
	if b, _ := d.resolve(sd.Dict["ImageMask"]); b == types.Boolean(true) {
		return info
	}
	info.ncomp = d.colorComponents(sd.Dict["ColorSpace"])
	info.plain = true
	if dec := d.numbers(sd.Dict["Decode"]); sd.Dict["Decode"] != nil {
		if len(dec) != 2*info.ncomp {
			info.plain = false
		}
		for i := 0; i+1 < len(dec); i += 2 {
			if dec[i] != 0 || dec[i+1] != 1 {
				info.plain = false
			}
		}
	}
	if d.array(sd.Dict["Mask"]) != nil {
		info.plain = false // color key masking requires exact sample values
	}
	return info
}

func targetSize(w, h int, u *imageUse, maxDPI int) (int, int) {
	if u == nil || u.unknown || maxDPI <= 0 || u.dpiX == 0 || u.dpiY == 0 {
		return w, h
	}
	tw, th := w, h
	if u.dpiX > float64(maxDPI) {
		tw = int(math.Ceil(float64(w) * float64(maxDPI) / u.dpiX))
	}
	if u.dpiY > float64(maxDPI) {
		th = int(math.Ceil(float64(h) * float64(maxDPI) / u.dpiY))
	}
	return max(tw, 1), max(th, 1)
}

func resample(pix []byte, w, h, n, tw, th int) []byte {
	var src, dst draw.Image
	switch n {
	case 1:
		src = &image.Gray{Pix: pix, Stride: w, Rect: image.Rect(0, 0, w, h)}
		dst = image.NewGray(image.Rect(0, 0, tw, th))
	default:
		rgba := image.NewRGBA(image.Rect(0, 0, w, h))
		for i, j := 0, 0; i < w*h; i, j = i+1, j+3 {
			copy(rgba.Pix[4*i:], pix[j:j+3])
			rgba.Pix[4*i+3] = 0xff
		}
		src = rgba
		dst = image.NewRGBA(image.Rect(0, 0, tw, th))
	}
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	if n == 1 {
		return dst.(*image.Gray).Pix
	}
	out := make([]byte, tw*th*3)
	p := dst.(*image.RGBA).Pix
	for i, j := 0, 0; i < tw*th; i, j = i+1, j+3 {
		copy(out[j:j+3], p[4*i:4*i+3])
	}
	return out
}

func toImage(pix []byte, w, h, n int) image.Image {
	if n == 1 {
		return &image.Gray{Pix: pix, Stride: w, Rect: image.Rect(0, 0, w, h)}
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, j := 0, 0; i < w*h; i, j = i+1, j+3 {
		copy(rgba.Pix[4*i:], pix[j:j+3])
		rgba.Pix[4*i+3] = 0xff
	}
	return rgba
}

func encodeJPEG(pix []byte, w, h, n, quality int) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, toImage(pix, w, h, n), &jpeg.Options{Quality: quality}); err != nil {
		return nil
	}
	return buf.Bytes()
}

// pngPredict applies PNG row filters (adaptive per row, minimum sum of
// absolute differences heuristic), as used by /Predictor 15.
func pngPredict(pix []byte, w, h, bpp int) []byte {
	stride := w * bpp
	out := make([]byte, 0, h*(stride+1))
	prev := make([]byte, stride)
	cand := make([][]byte, 5)
	for i := range cand {
		cand[i] = make([]byte, stride)
	}
	for y := 0; y < h; y++ {
		row := pix[y*stride : (y+1)*stride]
		for x := 0; x < stride; x++ {
			var a, c byte
			if x >= bpp {
				a = row[x-bpp]
				c = prev[x-bpp]
			}
			b := prev[x]
			cand[0][x] = row[x]
			cand[1][x] = row[x] - a
			cand[2][x] = row[x] - b
			cand[3][x] = row[x] - byte((int(a)+int(b))/2)
			cand[4][x] = row[x] - paeth(a, b, c)
		}
		best, bestSum := 0, math.MaxInt
		for f := 0; f < 5; f++ {
			sum := 0
			for _, v := range cand[f] {
				sum += int(math.Abs(float64(int8(v))))
			}
			if sum < bestSum {
				best, bestSum = f, sum
			}
		}
		out = append(out, byte(best))
		out = append(out, cand[best]...)
		prev = row
	}
	return out
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// bestLossless returns the smallest Flate encoding of 8-bit image samples,
// with or without PNG prediction.
func bestLossless(pix []byte, w, h, n int) ([]byte, types.Dict) {
	plain := flateBest(pix)
	if w <= 0 || h <= 0 || len(pix) < w*h*n {
		return plain, nil
	}
	pred := flateBest(pngPredict(pix[:w*h*n], w, h, n))
	if len(pred) < len(plain) {
		parms := types.Dict{
			"Predictor":        types.Integer(15),
			"Colors":           types.Integer(n),
			"BitsPerComponent": types.Integer(8),
			"Columns":          types.Integer(w),
		}
		return pred, parms
	}
	return plain, nil
}

// processImages downsamples and/or transcodes image XObjects.
func (d *doc) processImages(s *scanner) {
	o := d.opts
	lossy := !o.Lossless
	// Determine masks and propagate usage to them.
	masks := map[int]bool{}
	maskUse := map[int]*imageUse{}
	expected := map[int]int{}
	for k, v := range s.expected {
		expected[k] = v
	}
	nrs := d.sortedReachable()
	for _, nr := range nrs {
		sd, ok := d.xt.Table[nr].Object.(types.StreamDict)
		if !ok || d.name(sd.Dict["Subtype"]) != "Image" {
			continue
		}
		for _, key := range []string{"SMask", "Mask"} {
			mnr := refNr(sd.Dict[key])
			if mnr == 0 {
				continue
			}
			if msd, _ := d.stream(sd.Dict[key]); msd == nil {
				continue
			}
			masks[mnr] = true
			expected[mnr]++
			mu := maskUse[mnr]
			if mu == nil {
				mu = &imageUse{}
				maskUse[mnr] = mu
			}
			pu := s.images[nr]
			msd, _ := d.stream(sd.Dict[key])
			if msd.Dict["Matte"] != nil {
				// pre-multiplied masks must keep the parent's dimensions
				mu.unknown = true
				if pu != nil {
					pu.unknown = true
				}
				continue
			}
			pw := d.intOf(sd.Dict["Width"])
			ph := d.intOf(sd.Dict["Height"])
			if pu == nil || pu.unknown || pw == 0 || ph == 0 || d.refCount[nr] != s.expected[nr] {
				mu.unknown = true
				continue
			}
			mw, mh := float64(d.intOf(msd.Dict["Width"])), float64(d.intOf(msd.Dict["Height"]))
			mu.dpiX = math.Max(mu.dpiX, pu.dpiX*mw/float64(pw))
			mu.dpiY = math.Max(mu.dpiY, pu.dpiY*mh/float64(ph))
		}
	}
	for nr, mu := range maskUse {
		if u := s.images[nr]; u != nil {
			// also drawn directly
			if u.unknown {
				mu.unknown = true
			}
			mu.dpiX = math.Max(mu.dpiX, u.dpiX)
			mu.dpiY = math.Max(mu.dpiY, u.dpiY)
		}
	}

	for _, nr := range nrs {
		sd, ok := d.xt.Table[nr].Object.(types.StreamDict)
		if !ok || d.name(sd.Dict["Subtype"]) != "Image" {
			continue
		}
		info := d.imageInfo(&sd)
		info.isMask = masks[nr]
		if info.ncomp == 0 || info.bpc != 8 || !info.plain || info.w <= 0 || info.h <= 0 {
			continue
		}
		use := s.images[nr]
		if info.isMask {
			use = maskUse[nr]
		}
		safe := !s.incomplete && use != nil && d.refCount[nr] == expected[nr]
		tw, th := info.w, info.h
		if lossy && safe {
			tw, th = targetSize(info.w, info.h, use, o.MaxDPI)
		}
		resize := tw < info.w || th < info.h
		if !resize {
			tw, th = info.w, info.h
		}

		filters := d.filterNames(&sd)
		var pix []byte
		isDCT := len(filters) == 1 && filters[0] == "DCTDecode"
		switch {
		case isDCT:
			if !resize || info.isMask {
				continue
			}
			img, err := jpeg.Decode(bytes.NewReader(sd.Raw))
			if err != nil {
				continue
			}
			pix = imagePixels(img, info.ncomp)
			if pix == nil || img.Bounds().Dx() != info.w || img.Bounds().Dy() != info.h {
				continue
			}
		case d.decodable(&sd):
			b, err := d.decode(&sd)
			if err != nil || len(b) < info.w*info.h*info.ncomp {
				continue
			}
			pix = b[:info.w*info.h*info.ncomp]
		default:
			continue
		}

		before := len(sd.Raw)
		if resize {
			pix = resample(pix, info.w, info.h, info.ncomp, tw, th)
		}
		raw, parms := bestLossless(pix, tw, th, info.ncomp)
		filter := "FlateDecode"
		if lossy && !info.isMask {
			if j := encodeJPEG(pix, tw, th, info.ncomp, o.Quality); j != nil && len(j) < len(raw) {
				raw, parms, filter = j, nil, "DCTDecode"
			}
		}
		if isDCT && filter != "DCTDecode" && len(raw) >= before {
			continue
		}
		d.touched[nr] = true
		if !resize && len(raw) >= before {
			continue
		}
		d.setStream(nr, &sd, raw, filter, parms)
		if resize {
			sd.Dict.Update("Width", types.Integer(tw))
			sd.Dict.Update("Height", types.Integer(th))
			d.setObject(nr, sd)
		}
		dpi := ""
		if resize {
			dpi = " (downsampled)"
		}
		d.logf("  image obj %d: %dx%d -> %dx%d %s%s, %d -> %d bytes",
			nr, info.w, info.h, tw, th, filter, dpi, before, len(raw))
	}
}

func imagePixels(img image.Image, n int) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	switch n {
	case 1:
		g, ok := img.(*image.Gray)
		if !ok {
			return nil
		}
		out := make([]byte, 0, w*h)
		for y := 0; y < h; y++ {
			out = append(out, g.Pix[y*g.Stride:y*g.Stride+w]...)
		}
		return out
	case 3:
		if _, ok := img.(*image.YCbCr); !ok {
			if _, ok := img.(*image.RGBA); !ok {
				return nil
			}
		}
		out := make([]byte, 0, w*h*3)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bb, _ := img.At(x, y).RGBA()
				out = append(out, byte(r>>8), byte(g>>8), byte(bb>>8))
			}
		}
		return out
	}
	return nil
}
