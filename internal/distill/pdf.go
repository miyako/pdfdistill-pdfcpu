package distill

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

type doc struct {
	ctx  *model.Context
	xt   *model.XRefTable
	opts Options
	st   *Stats

	reachable map[int]bool
	refCount  map[int]int
	touched   map[int]bool // streams already re-encoded by a dedicated step

	contentHash map[int]string // dedup: hash of decoded stream content
}

func (d *doc) logf(format string, args ...any) {
	if d.opts.Verbose && d.opts.Log != nil {
		fmt.Fprintf(d.opts.Log, format+"\n", args...)
	}
}

func (d *doc) warnf(format string, args ...any) {
	if !d.opts.Quiet && d.opts.Log != nil {
		fmt.Fprintf(d.opts.Log, "warning: "+format+"\n", args...)
	}
}

// resolve follows indirect references and returns the resolved object plus
// the object number of the last indirection (0 for direct objects).
func (d *doc) resolve(o types.Object) (types.Object, int) {
	nr := 0
	for i := 0; i < 32; i++ {
		ir, ok := o.(types.IndirectRef)
		if !ok {
			if p, ok := o.(*types.IndirectRef); ok && p != nil {
				ir = *p
			} else {
				return o, nr
			}
		}
		nr = ir.ObjectNumber.Value()
		e, ok := d.xt.Table[nr]
		if !ok || e == nil || e.Free {
			return nil, nr
		}
		o = e.Object
	}
	return nil, nr
}

func (d *doc) dict(o types.Object) types.Dict {
	r, _ := d.resolve(o)
	switch v := r.(type) {
	case types.Dict:
		return v
	case types.StreamDict:
		return v.Dict
	}
	return nil
}

func (d *doc) array(o types.Object) types.Array {
	r, _ := d.resolve(o)
	a, _ := r.(types.Array)
	return a
}

func (d *doc) stream(o types.Object) (*types.StreamDict, int) {
	r, nr := d.resolve(o)
	sd, ok := r.(types.StreamDict)
	if !ok {
		return nil, 0
	}
	return &sd, nr
}

func (d *doc) name(o types.Object) string {
	r, _ := d.resolve(o)
	if n, ok := r.(types.Name); ok {
		return string(n)
	}
	return ""
}

func (d *doc) number(o types.Object) (float64, bool) {
	r, _ := d.resolve(o)
	switch v := r.(type) {
	case types.Integer:
		return float64(v), true
	case types.Float:
		return float64(v), true
	}
	return 0, false
}

func (d *doc) intOf(o types.Object) int {
	f, _ := d.number(o)
	return int(f)
}

func (d *doc) numbers(o types.Object) []float64 {
	a := d.array(o)
	out := make([]float64, 0, len(a))
	for _, e := range a {
		f, ok := d.number(e)
		if !ok {
			return nil
		}
		out = append(out, f)
	}
	return out
}

func refNr(o types.Object) int {
	switch v := o.(type) {
	case types.IndirectRef:
		return v.ObjectNumber.Value()
	case *types.IndirectRef:
		if v != nil {
			return v.ObjectNumber.Value()
		}
	}
	return 0
}

func mapID(m types.Dict) uintptr {
	if m == nil {
		return 0
	}
	return reflect.ValueOf(m).Pointer()
}

// objID returns an identity for direct dictionaries and arrays.
func objID(o types.Object) uintptr {
	switch v := o.(type) {
	case types.Dict:
		return mapID(v)
	case types.Array:
		if len(v) > 0 {
			return reflect.ValueOf(v).Pointer()
		}
	}
	return 0
}

// forEachRef calls fn for every indirect reference directly contained in o
// (without following references).
func forEachRef(o types.Object, fn func(nr int)) {
	switch v := o.(type) {
	case types.IndirectRef:
		fn(v.ObjectNumber.Value())
	case *types.IndirectRef:
		if v != nil {
			fn(v.ObjectNumber.Value())
		}
	case types.Dict:
		for _, e := range v {
			forEachRef(e, fn)
		}
	case types.StreamDict:
		forEachRef(v.Dict, fn)
	case types.Array:
		for _, e := range v {
			forEachRef(e, fn)
		}
	}
}

// rewriteRefs replaces references according to mapping. Dicts and arrays are
// modified in place; the (possibly new) object is returned.
func rewriteRefs(o types.Object, mapping map[int]int) types.Object {
	switch v := o.(type) {
	case types.IndirectRef:
		if n, ok := mapping[v.ObjectNumber.Value()]; ok {
			return *types.NewIndirectRef(n, 0)
		}
	case *types.IndirectRef:
		if v != nil {
			if n, ok := mapping[v.ObjectNumber.Value()]; ok {
				return types.NewIndirectRef(n, 0)
			}
		}
	case types.Dict:
		for k, e := range v {
			v[k] = rewriteRefs(e, mapping)
		}
	case types.StreamDict:
		rewriteRefs(v.Dict, mapping)
	case types.Array:
		for i, e := range v {
			v[i] = rewriteRefs(e, mapping)
		}
	}
	return o
}

func (d *doc) trailerRefs() []int {
	var out []int
	for _, r := range []*types.IndirectRef{d.xt.Root, d.xt.Info, d.xt.Encrypt} {
		if r != nil {
			out = append(out, r.ObjectNumber.Value())
		}
	}
	return out
}

// analyze computes the set of objects reachable from the trailer and the
// number of references to each object from reachable objects. Direct
// dictionaries that are shared between containers are only counted once.
func (d *doc) analyze() {
	d.reachable = map[int]bool{}
	d.refCount = map[int]int{}
	seenMaps := map[uintptr]bool{}
	queue := d.trailerRefs()
	for _, nr := range queue {
		d.reachable[nr] = true
	}
	var walk func(o types.Object)
	walk = func(o types.Object) {
		switch v := o.(type) {
		case types.IndirectRef, *types.IndirectRef:
			nr := refNr(v)
			d.refCount[nr]++
			if !d.reachable[nr] {
				d.reachable[nr] = true
				queue = append(queue, nr)
			}
		case types.Dict:
			id := mapID(v)
			if seenMaps[id] {
				return
			}
			seenMaps[id] = true
			for _, e := range v {
				walk(e)
			}
		case types.StreamDict:
			walk(v.Dict)
		case types.Array:
			for _, e := range v {
				walk(e)
			}
		}
	}
	for i := 0; i < len(queue); i++ {
		e, ok := d.xt.Table[queue[i]]
		if !ok || e == nil || e.Free || e.Object == nil {
			continue
		}
		walk(e.Object)
	}
}

func (d *doc) sortedReachable() []int {
	out := make([]int, 0, len(d.reachable))
	for nr := range d.reachable {
		if e, ok := d.xt.Table[nr]; ok && e != nil && !e.Free && e.Object != nil {
			out = append(out, nr)
		}
	}
	sort.Ints(out)
	return out
}

func (d *doc) setObject(nr int, o types.Object) {
	if e, ok := d.xt.Table[nr]; ok && e != nil {
		e.Object = o
	}
}

// --- streams ---------------------------------------------------------------

var decodableFilters = map[string]bool{
	"FlateDecode": true, "LZWDecode": true, "ASCII85Decode": true,
	"ASCIIHexDecode": true, "RunLengthDecode": true,
}

func (d *doc) filterNames(sd *types.StreamDict) []string {
	f, ok := sd.Dict.Find("Filter")
	if !ok {
		return nil
	}
	r, _ := d.resolve(f)
	switch v := r.(type) {
	case types.Name:
		return []string{string(v)}
	case types.Array:
		var out []string
		for _, e := range v {
			out = append(out, d.name(e))
		}
		return out
	}
	return []string{"?"}
}

func (d *doc) decodable(sd *types.StreamDict) bool {
	for _, f := range d.filterNames(sd) {
		if !decodableFilters[f] {
			return false
		}
	}
	return true
}

// decode returns the fully decoded stream data. Only call for streams whose
// filters are all decodable.
func (d *doc) decode(sd *types.StreamDict) ([]byte, error) {
	if len(d.filterNames(sd)) == 0 {
		return sd.Raw, nil
	}
	c := *sd
	c.Content = nil
	if err := c.Decode(); err != nil {
		return nil, err
	}
	if c.Content == nil {
		return nil, fmt.Errorf("stream not decoded")
	}
	return c.Content, nil
}

func flateBest(b []byte) []byte {
	var buf bytes.Buffer
	w, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

// setStream replaces stream data. filter "" means no filter.
func (d *doc) setStream(nr int, sd *types.StreamDict, raw []byte, filter string, parms types.Dict) {
	sd.Raw = raw
	sd.Content = nil
	sd.Dict.Delete("DecodeParms")
	sd.Dict.Delete("DL")
	sd.FilterPipeline = nil
	if filter == "" {
		sd.Dict.Delete("Filter")
	} else {
		sd.Dict.Update("Filter", types.Name(filter))
		sd.FilterPipeline = []types.PDFFilter{{Name: filter, DecodeParms: parms}}
		if parms != nil {
			sd.Dict.Update("DecodeParms", parms)
		}
	}
	l := int64(len(raw))
	sd.StreamLength = &l
	sd.StreamLengthObjNr = nil
	sd.Dict.Update("Length", types.Integer(l))
	d.setObject(nr, *sd)
}

// canonicalDict renders a dictionary with sorted keys, skipping the given
// keys. Used to build deduplication keys.
func canonicalDict(dict types.Dict, skip ...string) string {
	keys := make([]string, 0, len(dict))
outer:
	for k := range dict {
		for _, s := range skip {
			if k == s {
				continue outer
			}
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("<<")
	for _, k := range keys {
		sb.WriteString("/")
		sb.WriteString(k)
		sb.WriteString(" ")
		sb.WriteString(canonical(dict[k]))
	}
	sb.WriteString(">>")
	return sb.String()
}

func canonical(o types.Object) string {
	switch v := o.(type) {
	case nil:
		return "null"
	case types.Dict:
		return canonicalDict(v)
	case types.Array:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = canonical(e)
		}
		return "[" + strings.Join(parts, " ") + "]"
	case types.IndirectRef:
		return fmt.Sprintf("%dR", v.ObjectNumber.Value())
	case *types.IndirectRef:
		return fmt.Sprintf("%dR", v.ObjectNumber.Value())
	case types.Integer:
		return fmt.Sprintf("%d", int(v))
	case types.Float:
		return fmt.Sprintf("%g", float64(v))
	}
	return fmt.Sprintf("%T:%s", o, o.PDFString())
}
