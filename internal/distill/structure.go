package distill

import (
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// recompressStreams re-encodes every remaining stream with Flate at maximum
// compression (with PNG prediction for 8-bit images when beneficial), keeping
// the result only when it is smaller than the current encoding.
func (d *doc) recompressStreams() (int, int) {
	before, after := 0, 0
	for _, nr := range d.sortedReachable() {
		if d.touched[nr] {
			continue
		}
		sd, ok := d.xt.Table[nr].Object.(types.StreamDict)
		if !ok {
			continue
		}
		switch d.name(sd.Dict["Type"]) {
		case "XRef", "ObjStm", "Metadata":
			continue
		}
		if !d.decodable(&sd) {
			continue
		}
		isImage := d.name(sd.Dict["Subtype"]) == "Image"
		if isImage && d.opts.NoImages {
			continue
		}
		data, err := d.decode(&sd)
		if err != nil {
			continue
		}
		var raw []byte
		var parms types.Dict
		if isImage {
			info := d.imageInfo(&sd)
			if info.bpc == 8 && info.ncomp > 0 && len(data) >= info.w*info.h*info.ncomp {
				raw, parms = bestLossless(data[:info.w*info.h*info.ncomp], info.w, info.h, info.ncomp)
			}
		}
		if raw == nil {
			raw = flateBest(data)
		}
		if len(raw) < len(sd.Raw) {
			before += len(sd.Raw)
			after += len(raw)
			d.setStream(nr, &sd, raw, "FlateDecode", parms)
		}
	}
	return before, after
}
