package distill

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Types of dictionaries whose identity matters (or that are part of a tree
// structure) and that must therefore never be merged.
var noMergeTypes = map[string]bool{
	"Catalog": true, "Pages": true, "Page": true, "Annot": true,
	"StructTreeRoot": true, "StructElem": true, "MCR": true, "OBJR": true,
	"OCG": true, "OCMD": true, "Sig": true, "DocTimeStamp": true,
	"Outlines": true, "Template": true, "Thread": true, "Bead": true,
	"XRef": true, "ObjStm": true, "Metadata": true, "Encrypt": true,
}

// Keys that indicate an object participating in a tree/linked structure.
var noMergeKeys = []string{"Parent", "P", "Kids", "FT", "T", "First", "Last", "Next", "Prev"}

func (d *doc) dedupKey(nr int, o types.Object) (string, bool) {
	switch v := o.(type) {
	case types.StreamDict:
		if t := d.name(v.Dict["Type"]); noMergeTypes[t] {
			return "", false
		}
		ch, ok := d.contentHash[nr]
		if !ok {
			if d.decodable(&v) {
				data, err := d.decode(&v)
				if err != nil {
					ch = ""
				} else {
					s := sha256.Sum256(data)
					ch = "D" + hex.EncodeToString(s[:])
				}
			} else {
				s := sha256.Sum256(v.Raw)
				ch = "R" + hex.EncodeToString(s[:])
			}
			d.contentHash[nr] = ch
		}
		var dictKey string
		switch {
		case ch == "":
			return "", false
		case ch[0] == 'D':
			dictKey = canonicalDict(v.Dict, "Length", "Filter", "DecodeParms", "DL")
		default:
			dictKey = canonicalDict(v.Dict, "Length")
		}
		s := sha256.Sum256([]byte(dictKey))
		return "S" + ch + hex.EncodeToString(s[:]), true
	case types.Dict:
		if t := d.name(v["Type"]); noMergeTypes[t] {
			return "", false
		}
		for _, k := range noMergeKeys {
			if _, ok := v[k]; ok {
				return "", false
			}
		}
		s := sha256.Sum256([]byte(canonicalDict(v)))
		return "D" + hex.EncodeToString(s[:]), true
	case types.Array:
		s := sha256.Sum256([]byte(canonical(v)))
		return "A" + hex.EncodeToString(s[:]), true
	case types.Integer, types.Float, types.Name, types.StringLiteral, types.HexLiteral, types.Boolean:
		return "P" + canonical(v), true
	}
	return "", false
}

// dedup merges identical objects (streams compared by decoded content) and
// rewrites all references. It iterates to a fixpoint so that e.g. two images
// become identical once their SMasks were merged, or two font dictionaries
// once their FontFile2 streams and /W arrays were merged.
func (d *doc) dedup() int {
	total := 0
	d.contentHash = map[int]string{}
	protected := map[int]bool{}
	for _, nr := range d.trailerRefs() {
		protected[nr] = true
	}
	for iter := 0; iter < 10; iter++ {
		d.analyze()
		canon := map[string]int{}
		mapping := map[int]int{}
		for _, nr := range d.sortedReachable() {
			if protected[nr] {
				continue
			}
			key, ok := d.dedupKey(nr, d.xt.Table[nr].Object)
			if !ok {
				continue
			}
			c, exists := canon[key]
			if !exists {
				canon[key] = nr
				continue
			}
			mapping[nr] = c
			// Keep the smaller encoding of identical streams.
			if a, ok := d.xt.Table[c].Object.(types.StreamDict); ok {
				if b, ok := d.xt.Table[nr].Object.(types.StreamDict); ok && len(b.Raw) < len(a.Raw) {
					d.xt.Table[c].Object = b
					d.contentHash[c] = d.contentHash[nr]
				}
			}
		}
		if len(mapping) == 0 {
			break
		}
		total += len(mapping)
		for nr := range d.reachable {
			if e, ok := d.xt.Table[nr]; ok && e != nil && e.Object != nil {
				e.Object = rewriteRefs(e.Object, mapping)
			}
		}
		if d.xt.RootDict != nil {
			rewriteRefs(d.xt.RootDict, mapping)
		}
	}
	d.analyze()
	return total
}
