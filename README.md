# pdfdistill-pdfcpu

A single-binary command-line tool that shrinks bloated PDFs — in particular the
output of **Microsoft Print to PDF**, which embeds complete TrueType fonts and
stores photos as lossless Flate images.

Written in pure Go (`CGO_ENABLED=0`) on top of [pdfcpu](https://github.com/pdfcpu/pdfcpu).
It is one of three sibling implementations (Rust/lopdf, C++/qpdf, Go/pdfcpu)
that share the same command-line interface so they can be benchmarked against
each other.

## Usage

```
pdfdistill-pdfcpu <INPUT|-> [-o <OUTPUT|->] [options]
  INPUT  path or '-' for stdin
  -o     output path or '-' for stdout (default: stdout if input is '-', else <input>.distilled.pdf)
  --quality <1-100>      JPEG quality for recompressed images (default 75)
  --max-dpi <n>          downsample images whose effective resolution exceeds n (default 150; 0 = never)
  --no-images            skip image recompression
  --no-subset            skip font subsetting
  --no-dedup             skip duplicate object/image/font removal
  --lossless             only lossless optimizations (implies no JPEG recompression / downsampling)
  -v, --verbose          print per-step size statistics to stderr
  -q, --quiet
  --version, --help
```

Examples:

```sh
pdfdistill-pdfcpu scan.pdf                     # writes scan.distilled.pdf
pdfdistill-pdfcpu scan.pdf -o small.pdf -v     # with per-step statistics
cat scan.pdf | pdfdistill-pdfcpu - > small.pdf # stdin -> stdout
pdfdistill-pdfcpu scan.pdf --lossless -o out.pdf
```

Behaviour:

* `<input>.distilled.pdf` replaces a trailing `.pdf` (case-insensitive), e.g.
  `report.pdf` → `report.distilled.pdf`; other names get the suffix appended.
* All diagnostics go to **stderr**; stdout only ever carries PDF bytes.
  By default one summary line is printed; `-q` silences it, `-v` adds per-step
  statistics.
* **The output is never larger than the input.** If the optimized result is not
  smaller, the original bytes are written unchanged.
* Exit codes: `0` success, `1` runtime error (unreadable/encrypted PDF, I/O),
  `2` usage error.
* `--no-images` leaves image streams byte-for-byte untouched (no JPEG, no
  downsampling, no Flate recompression of images).
* Encrypted PDFs are refused.
* Files are written atomically (temporary file + rename).

## What it does

The pipeline (`internal/distill`) runs these steps in order:

1. **Read** with pdfcpu (relaxed validation; falls back to an unvalidated read
   for slightly broken files). When dedup is enabled pdfcpu's own `optimize`
   pass runs too (merges duplicate fonts/images it recognises).
2. **Dedup** (`dedup.go`): a fix-point loop that hashes every object —
   streams by *decoded* content plus a canonical dictionary (ignoring
   `Length/Filter/DecodeParms/DL`), dictionaries, arrays — and merges identical
   ones, rewriting all references. Because it iterates, SMasks are merged first
   and then the images that point at them, FontFiles before descriptors, etc.
   Structural dictionaries (Page, Pages, Annot, StructElem, outline items,
   form fields, …) are never merged.
3. **Font subsetting** (`fonts.go`, `scan.go`, `internal/ttf`): a content-stream
   interpreter walks every page, Form XObject, tiling pattern, Type 3 glyph,
   soft-mask group and annotation appearance stream and collects the CIDs shown
   with each font (`Tj`, `TJ`, `'`, `"`). Type0/CIDFontType2 fonts with a
   `FontFile2` and `CIDToGIDMap` `/Identity` (or a stream map) are subset:
   * glyph IDs are **retained** — unused glyphs become zero-length in
     `glyf`/`loca`, so content streams and the CIDToGIDMap stay unchanged;
   * `.notdef` and all composite-glyph components are kept;
   * tables not needed for PDF rendering are dropped (`GSUB`, `GPOS`, `GDEF`,
     `BASE`, `JSTF`, `meta`, `DSIG`, `VDMX`, `hdmx`, `LTSH`, `kern`, …);
     `post` becomes format 3, `cmap` a minimal (3,1) table, unused
     `hmtx`/`vmtx` entries are zeroed, `loca` becomes short when possible;
   * `BaseFont`/`FontName` get a 6-letter subset tag (`ABCDEF+Name`), `Length1`
     and `CIDSet` are updated.

   Safety: a font is only subset when *every* reference to its objects comes
   from resource dictionaries the scanner actually interpreted. Fonts also
   reachable from elsewhere (e.g. AcroForm `/DR`, where a form filler might type
   new text) are left alone, as are all fonts if any content stream cannot be
   fully decoded.
4. **Images** (`images.go`): for 8-bit DeviceGray/DeviceRGB (also CalGray,
   CalRGB, ICCBased N=1/3) images with default `Decode` and no colour-key mask:
   * the effective resolution of each placement is computed from the CTM at
     draw time; if the highest DPI any placement needs exceeds `--max-dpi` the
     image is resampled (Catmull-Rom) — together with its SMask so dimensions
     stay consistent;
   * the result is encoded as JPEG (`DCTDecode`, `--quality`) and as Flate
     (max level, with and without PNG predictors); the smallest candidate wins,
     and the original is kept if nothing is smaller.
   * SMask / Mask images are never JPEG-encoded (kept as Flate, recompressed at
     max level). `/ImageMask`, Indexed, 1/2/4/16-bit, CMYK, Lab, unusual
     `Decode` arrays and `/Matte` masks are not converted to JPEG or resized.
   * Existing JPEGs are only re-encoded when they need downsampling.
5. **Structure** (`structure.go`): every decodable stream is re-compressed with
   Flate at `zlib.BestCompression` (kept only if smaller); the writer drops
   unreferenced objects and writes object streams + cross-reference streams.

Text in content streams is never rewritten.

### Font subsetting: choice of implementation

Go has no general-purpose permissive TrueType *writer*. Options considered:

| Option | License | Verdict |
|---|---|---|
| pdfcpu internal font code (`pkg/font`) | Apache-2.0 | Its subsetter works on pdfcpu's own installed-font repository (fonts identified by name, used for text stamping); it cannot take an arbitrary embedded `FontFile2` stream. |
| `golang.org/x/image/font/sfnt` | BSD-3 | Read-only parser; no serialisation. |
| `github.com/tdewolff/font` | MIT | Can subset, but renumbers glyphs (would require rewriting content streams / CIDToGIDMap) and pulls in a larger dependency. |
| `go-pdf/fpdf` UTF-8 font subsetter | MIT | Tightly coupled to fpdf's document model; renumbers glyphs. |
| **Own GID-retaining subsetter** (`internal/ttf`) | MIT (this repo) | **Chosen.** ~400 lines, no dependencies, keeps GIDs so the PDF needs no text rewriting. |

The GID-retaining approach keeps `loca`/`hmtx` sized for the full glyph count
(for the 24,411-glyph Yu Gothic UI this is ~100 KB raw), but those tables are
mostly zeros and compress to a few KB.

### JPEG encoder

JPEG encoding uses the standard library `image/jpeg` (baseline, 4:2:0 chroma
subsampling, standard Huffman tables). It is noticeably less efficient than
mozjpeg (which uses trellis quantisation, optimised Huffman tables and
progressive mode — typically 5–15 % smaller at equal visual quality) but keeps
the binary pure Go with no C dependency. Quality numbers are therefore not
exactly comparable with the libjpeg-turbo/mozjpeg-based siblings.

## Results

Fixture `testdata/ms-print-to-pdf-1.pdf` (Microsoft Print to PDF, 1 page,
Yu Gothic UI fully embedded, two 512×512 RGB Flate images with Gray SMasks):

| Options | Output bytes | Reduction |
|---|---:|---:|
| (input) | 720,651 | — |
| default | 116,145 | −83.9 % |
| `--lossless` | 450,122 | −37.5 % |
| `--no-images` | 584,064 | −19.0 % |
| `--no-subset` | 251,654 | −65.1 % |
| `--quality 30` | 88,320 | −87.7 % |
| `--max-dpi 50` | 85,554 | −88.1 % |

Default breakdown: font program 143,832 → 8,319 bytes (15 of 24,411 glyphs
kept); RGB images 296,825 → 36,303 and 227,054 → 24,076 (JPEG); SMasks
26,220 → 23,599 and 16,412 → 14,623 (Flate + PNG predictor). The images are
drawn at 72 dpi, so the default `--max-dpi 150` does not resample them.

Rendering was compared with `pdftoppm`: `--lossless` output is pixel-identical
to the input; default output differs only by JPEG artefacts. Extracted text
(`pdftotext`) is identical.

Outputs are not byte-for-byte reproducible between runs: pdfcpu updates
`/ModDate` and the trailer `/ID`.

## Building

Requires Go (version per `go.mod`; the toolchain is downloaded automatically).

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=v0.1.0" \
  -o pdfdistill-pdfcpu ./cmd/pdfdistill-pdfcpu
```

macOS universal binary:

```sh
for arch in arm64 amd64; do
  CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "-s -w" \
    -o pdfdistill-pdfcpu-$arch ./cmd/pdfdistill-pdfcpu
done
lipo -create -output pdfdistill-pdfcpu pdfdistill-pdfcpu-arm64 pdfdistill-pdfcpu-amd64
```

Windows (from any OS):

```sh
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" \
  -o pdfdistill-pdfcpu.exe ./cmd/pdfdistill-pdfcpu
```

CI (`.github/workflows/build.yml`) tests on macOS and Windows, builds a macOS
universal binary plus windows-amd64 and windows-arm64 executables, uploads them
as artifacts, and publishes a GitHub release when a `v*` tag is pushed.

## Testing

```sh
go test ./...
```

The suite covers stdin/stdout round-trips, default output naming, usage errors,
pdfcpu validation of every output, glyph outline and text preservation after
subsetting, image presence/encoding, lossless pixel equality, downsampling,
deduplication, the never-larger rule, and a size regression against the
fixture (default output must stay ≤ 140,000 bytes). A rendering comparison
test runs when `pdftoppm` (poppler) is on `PATH` and is skipped otherwise.

## Dependencies and licenses

| Module | License |
|---|---|
| github.com/pdfcpu/pdfcpu | Apache-2.0 |
| github.com/spf13/pflag | BSD-3-Clause |
| golang.org/x/image, x/crypto, x/text | BSD-3-Clause |
| github.com/hhrutter/tiff (via pdfcpu) | BSD-3-Clause |
| github.com/mattn/go-runewidth, github.com/clipperhouse/uax29 (via pdfcpu) | MIT |
| github.com/xdg-go/stringprep (via pdfcpu) | Apache-2.0 |
| go.yaml.in/yaml/v3 (via pdfcpu) | MIT / Apache-2.0 |

No GPL/LGPL/AGPL or commercial code; no cgo.

## License

MIT — see [LICENSE](LICENSE).
