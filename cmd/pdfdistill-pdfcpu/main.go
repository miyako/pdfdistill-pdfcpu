// Command pdfdistill-pdfcpu reduces the size of bloated PDF files, notably
// those produced by Microsoft Print to PDF.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/miyako/pdfdistill-pdfcpu/internal/distill"
	flag "github.com/spf13/pflag"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: pdfdistill-pdfcpu <INPUT|-> [-o <OUTPUT|->] [options]

Reduce the size of a PDF file (font subsetting, image recompression,
duplicate removal, structural compression).

Arguments:
  INPUT                  input PDF path, or '-' for stdin

Options:
  -o, --output <path>    output path or '-' for stdout
                         (default: stdout if INPUT is '-', else <input>.distilled.pdf)
      --quality <1-100>  JPEG quality for recompressed images (default 75)
      --max-dpi <n>      downsample images whose effective resolution exceeds n
                         (default 150; 0 = never)
      --no-images        skip image recompression
      --no-subset        skip font subsetting
      --no-dedup         skip duplicate object/image/font removal
      --lossless         only lossless optimizations (no JPEG, no downsampling)
  -v, --verbose          print per-step size statistics to stderr
  -q, --quiet            suppress all non-error output
      --version          print version and exit
  -h, --help             print this help and exit
`

func pdfcpuVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, m := range bi.Deps {
			if m.Path == "github.com/pdfcpu/pdfcpu" {
				return m.Version
			}
		}
	}
	return "unknown"
}

func defaultOutput(in string) string {
	ext := filepath.Ext(in)
	base := in
	if strings.EqualFold(ext, ".pdf") {
		base = strings.TrimSuffix(in, ext)
	}
	return base + ".distilled.pdf"
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pdfdistill-pdfcpu", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.SortFlags = false
	output := fs.StringP("output", "o", "", "")
	quality := fs.Int("quality", 75, "")
	maxDPI := fs.Int("max-dpi", 150, "")
	noImages := fs.Bool("no-images", false, "")
	noSubset := fs.Bool("no-subset", false, "")
	noDedup := fs.Bool("no-dedup", false, "")
	lossless := fs.Bool("lossless", false, "")
	verbose := fs.BoolP("verbose", "v", false, "")
	quiet := fs.BoolP("quiet", "q", false, "")
	showVersion := fs.Bool("version", false, "")
	help := fs.BoolP("help", "h", false, "")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "pdfdistill-pdfcpu: %v\n\n%s", err, usage)
		return 2
	}
	if *help {
		fmt.Fprint(stderr, usage)
		return 0
	}
	if *showVersion {
		fmt.Fprintf(stderr, "pdfdistill-pdfcpu %s (pdfcpu %s)\n", version, pdfcpuVersion())
		return 0
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(stderr, "pdfdistill-pdfcpu: expected exactly one INPUT argument\n\n%s", usage)
		return 2
	}
	if *quality < 1 || *quality > 100 {
		fmt.Fprintf(stderr, "pdfdistill-pdfcpu: --quality must be between 1 and 100\n")
		return 2
	}
	if *maxDPI < 0 {
		fmt.Fprintf(stderr, "pdfdistill-pdfcpu: --max-dpi must be >= 0\n")
		return 2
	}
	if *verbose && *quiet {
		*verbose = false
	}

	in := fs.Arg(0)
	out := *output
	if !fs.Changed("output") {
		if in == "-" {
			out = "-"
		} else {
			out = defaultOutput(in)
		}
	}
	if out == "" {
		fmt.Fprintf(stderr, "pdfdistill-pdfcpu: empty output path\n")
		return 2
	}

	var data []byte
	var err error
	if in == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(in)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdfdistill-pdfcpu: %v\n", err)
		return 1
	}

	opts := distill.Options{
		Quality:  *quality,
		MaxDPI:   *maxDPI,
		NoImages: *noImages,
		NoSubset: *noSubset,
		NoDedup:  *noDedup,
		Lossless: *lossless,
		Verbose:  *verbose,
		Quiet:    *quiet,
		Log:      stderr,
	}
	result, st, err := distill.Distill(data, opts)
	if err != nil {
		fmt.Fprintf(stderr, "pdfdistill-pdfcpu: %s: %v\n", displayName(in, "stdin"), err)
		return 1
	}

	if out == "-" {
		_, err = stdout.Write(result)
	} else {
		err = writeFileAtomic(out, result)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdfdistill-pdfcpu: %v\n", err)
		return 1
	}
	if !*quiet {
		pct := 0.0
		if st.InputSize > 0 {
			pct = 100 * (1 - float64(st.OutputSize)/float64(st.InputSize))
		}
		note := ""
		if st.Encrypted {
			note = " (encrypted; original kept)"
		} else if st.UsedOriginal {
			note = " (no reduction; original kept)"
		}
		fmt.Fprintf(stderr, "%s -> %s: %d -> %d bytes (-%.1f%%)%s\n",
			displayName(in, "stdin"), displayName(out, "stdout"), st.InputSize, st.OutputSize, pct, note)
	}
	return 0
}

// displayName returns p for user-facing messages, substituting stdName
// ("stdin" or "stdout") for "-". Avoid angle brackets: the 4D wrapper shows
// stderr in forms where "<name>" is parsed as a variable reference.
func displayName(p, stdName string) string {
	if p == "-" {
		return stdName
	}
	return p
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".pdfdistill-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
