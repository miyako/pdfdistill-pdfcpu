# pdfdistill-pdfcpu

Shrinks bloated PDFs — in particular the ones produced by **Microsoft Print to PDF**, which
embeds complete TrueType fonts and stores photos as lossless Flate images — by deduplicating
objects, subsetting fonts, recompressing/downsampling images and rewriting the file structure.

This repository contains:

* a **[4D component](#4d-component)** (`pdfdistill-pdfcpu/`) that lets 4D applications distill
  PDFs through the class `cs.pdfcpu.distill`;
* the **[command-line tool](#command-line-tool)** it wraps: a single, self-contained binary
  written in pure Go (`CGO_ENABLED=0`) on top of [pdfcpu](https://github.com/pdfcpu/pdfcpu),
  with only permissively licensed dependencies.

## 4D component

The component bundles the `pdfdistill-pdfcpu` executable — a signed and notarized universal
binary for macOS, and builds for Windows x64 and ARM64 — in
`Resources/bin/{macOS,Windows,WindowsARM}` (see [CI / releases](#ci--releases)) and runs it with
`4D.SystemWorker`. If the executable for the current platform is missing, `pdfdistill-pdfcpu`
is looked up on the `PATH`. No plug-in is involved. It targets 4D 20 and later.

### Installation

**Project Dependencies (4D 20 R6 and later):** add the component from the *Project
Dependencies* panel (GitHub repository `miyako/pdfdistill-pdfcpu`), or declare it in your
project's `Project/Sources/dependencies.json`:

```json
{
  "dependencies": {
    "pdfdistill-pdfcpu": {
      "github": "miyako/pdfdistill-pdfcpu",
      "version": "^20.0.0"
    }
  }
}
```

**Manual:** download `pdfdistill-pdfcpu.zip` from the
[latest release](https://github.com/miyako/pdfdistill-pdfcpu/releases/latest) and place the
compiled component in your project's `Components` folder.

The component's classes are exposed in the `cs.pdfcpu` class store.

### Quick start

```4d
var $distill : cs.pdfcpu.distill
$distill:=cs.pdfcpu.distill.new()

var $version : Text
$version:=$distill.version()  // commit hash of the CLI build, e.g. "cee530f"

var $in; $out : 4D.File
$in:=Folder(fk desktop folder).file("report.pdf")
$out:=Folder(fk desktop folder).file("report.distilled.pdf")

// file to file, synchronous; each task is a collection of CLI arguments
$distill.execute([$in; "-o"; $out; "--quality"; 60; "--max-dpi"; 150])

// several tasks in one call, e.g. lossless only
$distill.execute([\
  [$in; "-o"; $out; "--lossless"]; \
  [Folder(fk desktop folder).file("other.pdf"); "--no-images"]])  // -> other.distilled.pdf

// Blob in (via stdin), Blob out (via stdout)
var $pdf : 4D.Blob
$pdf:=$in.getContent()
var $result : Object
$result:=$distill.execute(["-"; "--quality"; 60; {file: $pdf}])[0]
// $result.response: 4D.Blob (the PDF), $result.responseError: Text, $result.exitCode: Integer
```

Task elements are mapped to the command line by type: `4D.File` → its path, Text → an argument
(flags such as `"--lossless"`), Number → its value, and an object `{data: …; file: …}` supplies
a callback context (`data`) and/or the stdin payload (`file`, used when the input is `"-"`).
Synchronous calls return one object per task: `{response: 4D.Blob; responseError: Text;
exitCode: Integer}` (stdout, stderr, and `0` success / `1` runtime error / `2` usage error).
For Blob in / Blob out, use `["-"; …options; {file: $blob}]`; the PDF is returned in `response`.

`version()` runs `pdfdistill-pdfcpu --version`, reads its **stderr** (the CLI never writes
`--version`/`--help` to stdout) and returns the hexadecimal commit hash from
`pdfdistill-pdfcpu <commit> (pdfcpu vX.Y.Z)` — see the
[version string contract](#building).

To run asynchronously, pass an `onResponse` callback; it receives the `4D.SystemWorker`
(`exitCode`, and the CLI's summary line in `responseError`) and the event object, whose
`context` is the task's `data`. The calling process must stay alive until the callback fires:

```4d
#DECLARE($params : Object)

If ($params=Null)
	CALL WORKER(1; Current method name; {})
Else 
	var $in; $out : 4D.File
	$in:=Folder(fk desktop folder).file("report.pdf")
	$out:=Folder(fk desktop folder).file("report.distilled.pdf")
	
	cs.pdfcpu.distill.new().execute([[$in; "-o"; $out; {data: $out}]]; \
		{onResponse: Formula(ALERT(String($1.exitCode)+": "+$1.responseError))})
End if 
```

### Class documentation

* [`distill`](pdfdistill-pdfcpu/Documentation/Classes/distill.md) — public API: `version()`,
  `execute()`, `terminate()`, options, callbacks and examples
* Internal: [`_CLI`](pdfdistill-pdfcpu/Documentation/Classes/_CLI.md),
  [`_CLI_Controller`](pdfdistill-pdfcpu/Documentation/Classes/_CLI_Controller.md),
  [`_distill_Controller`](pdfdistill-pdfcpu/Documentation/Classes/_distill_Controller.md)

## Command-line tool

The CLI is written in pure Go (`CGO_ENABLED=0`) on top of
[pdfcpu](https://github.com/pdfcpu/pdfcpu) and is one of three sibling implementations
(Rust/lopdf, C++/qpdf, Go/pdfcpu) that share the same command-line interface so they can be
benchmarked against each other.

### Usage

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
  This includes `--version` and `--help`, which print to stderr and exit `0`.
  By default one summary line is printed, e.g.
  `report.pdf -> report.distilled.pdf: 720651 -> 116143 bytes (-83.9%)`;
  `-q` silences it, `-v` adds per-step statistics. When `-` is used, messages
  name the streams `stdin` / `stdout` (no angle brackets), e.g.
  `stdin -> stdout: 720651 -> 116145 bytes (-83.9%)` or
  `pdfdistill-pdfcpu: stdin: <error>`.
* Lines end with a bare `\n` on every platform, including Windows.
* **The output is never larger than the input.** If the optimized result is not
  smaller, the original bytes are written unchanged.
* Exit codes: `0` success, `1` runtime error (unreadable PDF, I/O),
  `2` usage error.
* `--no-images` leaves image streams byte-for-byte untouched (no JPEG, no
  downsampling, no Flate recompression of images).
* Encrypted PDFs are passed through unchanged (original bytes written, exit 0).
* Files are written atomically (temporary file + rename).

### What it does

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

#### Font subsetting: choice of implementation

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

#### JPEG encoder

JPEG encoding uses the standard library `image/jpeg` (baseline, 4:2:0 chroma
subsampling, standard Huffman tables). It is noticeably less efficient than
mozjpeg (which uses trellis quantisation, optimised Huffman tables and
progressive mode — typically 5–15 % smaller at equal visual quality) but keeps
the binary pure Go with no C dependency. Quality numbers are therefore not
exactly comparable with the libjpeg-turbo/mozjpeg-based siblings.

### Results

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

### Building

Requires Go (version per `go.mod`; the toolchain is downloaded automatically).
Builds are pure Go (`CGO_ENABLED=0`), so every target can be cross-compiled
from any OS.

```sh
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=$(git rev-parse --short=7 HEAD)" \
  -o pdfdistill-pdfcpu ./cmd/pdfdistill-pdfcpu
```

> **Version string contract.** `--version` prints
> `pdfdistill-pdfcpu <commit> (pdfcpu vX.Y.Z)` to **stderr** (e.g.
> `pdfdistill-pdfcpu cee530f (pdfcpu v0.16.4)`). The 4D component's
> `distill.version()` reads stderr and extracts the version with the regular
> expression `\s+([a-f0-9]+)\s\(`, so `main.version` must be a lowercase
> hexadecimal string — the short commit SHA, as injected by CI. Without
> `-ldflags "-X main.version=…"` the binary reports `dev` and `version()`
> returns an empty string. Don't move `--version` to stdout or change the
> format without updating `distill.4dm`.

macOS universal binary:

```sh
V=$(git rev-parse --short=7 HEAD)
for arch in arm64 amd64; do
  CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$V" \
    -o pdfdistill-pdfcpu-$arch ./cmd/pdfdistill-pdfcpu
done
lipo -create -output pdfdistill-pdfcpu pdfdistill-pdfcpu-arm64 pdfdistill-pdfcpu-amd64
```

Windows x64 / ARM64 (from any OS):

```sh
V=$(git rev-parse --short=7 HEAD)
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=windows GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$V" \
    -o windows-$arch/pdfdistill-pdfcpu.exe ./cmd/pdfdistill-pdfcpu
done
```

#### CI / releases

`.github/workflows/build.yml` (Build) runs on GitHub-hosted runners:

| Job | Runner | Does |
|---|---|---|
| `macos-universal` | `macos-latest` | `go vet`, `go test`, builds darwin/arm64 + darwin/amd64 and merges them with `lipo`, checks the `--version` contract, smoke-tests the amd64 slice under Rosetta, uploads `pdfdistill-pdfcpu-macos-universal.zip` |
| `windows` (amd64) | `windows-latest` | `go vet`, `go test`, native build, `--version` check, uploads `pdfdistill-pdfcpu-windows-amd64.zip` |
| `windows` (arm64) | `windows-11-arm` | same, natively on Windows on ARM, uploads `pdfdistill-pdfcpu-windows-arm64.zip` |

Each archive contains the binary, this README and `LICENSE`. The version is
injected with `-ldflags "-X main.version=<short commit SHA>"` (see the version
string contract above). Pull requests only build and test.

On push to `main` (changes outside `pdfdistill-pdfcpu/`) or a manual run on
`main`, Build also:

1. code signs the macOS universal binary (Developer ID, hardened runtime,
   secure timestamp) and notarizes it with `notarytool`; the job fails unless
   notarization is `Accepted` (a bare Mach-O can't be stapled, so Gatekeeper
   fetches the ticket online). The temporary keychain is always deleted;
2. copies the binaries into the 4D component —
   `pdfdistill-pdfcpu/Resources/bin/macOS/pdfdistill-pdfcpu` (mode `100755`),
   `pdfdistill-pdfcpu/Resources/bin/Windows/pdfdistill-pdfcpu.exe`,
   `pdfdistill-pdfcpu/Resources/bin/WindowsARM/pdfdistill-pdfcpu.exe` — and
   commits them to `main` as `github-actions[bot]` if they changed (the
   `pdfdistill-pdfcpu/**` path filter keeps that commit from rebuilding the CLI);
3. dispatches the Publish workflow (`publish.yml`, patch bump), which builds,
   signs and releases the 4D component from that commit.

Signing uses the same repository secrets as Publish
(`APPLE_DEVELOPER_ID_CERTIFICATE`, `APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD`,
`KEYCHAIN_PASSWORD`, `CODESIGN_APPLE_ID`, `NOTARYTOOL_APPLE_ID`,
`NOTARYTOOL_TEAM_ID`, `NOTARYTOOL_PASSWORD`). Signing steps are skipped for pull
requests and when the secrets are absent; the ship step then refuses to run.

Pushing a `v*` tag creates a GitHub release with the three CLI archives and
`SHA256SUMS.txt`.

### Testing

```sh
go test ./...
```

The suite covers stdin/stdout round-trips, default output naming, usage errors,
pdfcpu validation of every output, glyph outline and text preservation after
subsetting, image presence/encoding, lossless pixel equality, downsampling,
deduplication, the never-larger rule, and a size regression against the
fixture (default output must stay ≤ 140,000 bytes). A rendering comparison
test runs when `pdftoppm` (poppler) is on `PATH` and is skipped otherwise.

### Dependencies and licenses

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

## Licenses

pdfdistill-pdfcpu (CLI and 4D component) is MIT licensed — see [LICENSE](LICENSE).
All Go dependencies are permissively licensed (table above).
