package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

const fixture = "../../testdata/ms-print-to-pdf-1.pdf"

// TestMain lets the test binary act as the CLI so that tests can exercise
// real process stdin/stdout/exit codes.
func TestMain(m *testing.M) {
	if os.Getenv("PDFDISTILL_RUN_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func cli(t *testing.T, stdin []byte, args ...string) (stdout, stderr []byte, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "PDFDISTILL_RUN_MAIN=1")
	cmd.Stdin = bytes.NewReader(stdin)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return o.Bytes(), e.Bytes(), code
}

func validate(t *testing.T, b []byte) {
	t.Helper()
	conf := model.NewStatelessConfiguration()
	conf.ValidationMode = model.ValidationStrict
	if err := api.Validate(context.Background(), bytes.NewReader(b), conf, nil); err != nil {
		t.Fatalf("pdfcpu validate: %v", err)
	}
}

func TestStdinStdout(t *testing.T) {
	in, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, code := cli(t, in, "-")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) || !bytes.HasSuffix(bytes.TrimSpace(out), []byte("%%EOF")) {
		t.Fatalf("stdout is not a clean PDF (%d bytes)", len(out))
	}
	if len(out) >= len(in) {
		t.Errorf("no reduction: %d -> %d", len(in), len(out))
	}
	if !strings.Contains(string(errOut), "->") {
		t.Errorf("expected summary on stderr, got %q", errOut)
	}
	validate(t, out)

	// Explicit '-o -' with quiet: no stderr output at all.
	out2, errOut, code := cli(t, in, "-", "-o", "-", "-q")
	if code != 0 || len(errOut) != 0 {
		t.Fatalf("quiet run: exit %d, stderr %q", code, errOut)
	}
	if len(out2) == 0 {
		t.Fatal("no output")
	}
}

func TestDefaultOutputPath(t *testing.T) {
	in, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(p, in, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, errOut, code := cli(t, nil, p, "-v")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(stdout) != 0 {
		t.Error("stdout must stay empty when writing to a file")
	}
	for _, step := range []string{"dedup:", "subset:", "images:", "structure:"} {
		if !strings.Contains(string(errOut), step) {
			t.Errorf("verbose output lacks %q", step)
		}
	}
	out, err := os.ReadFile(filepath.Join(dir, "doc.distilled.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	validate(t, out)

	if got := defaultOutput("a/b.PDF"); got != "a/b.distilled.pdf" {
		t.Errorf("defaultOutput = %q", got)
	}
	if got := defaultOutput("noext"); got != "noext.distilled.pdf" {
		t.Errorf("defaultOutput = %q", got)
	}
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		args []string
		code int
	}{
		{nil, 2},
		{[]string{"a.pdf", "b.pdf"}, 2},
		{[]string{"-", "--quality", "0"}, 2},
		{[]string{"-", "--quality", "101"}, 2},
		{[]string{"-", "--max-dpi", "-1"}, 2},
		{[]string{"-", "--bogus"}, 2},
		{[]string{"/nonexistent/file.pdf"}, 1},
		{[]string{"--version"}, 0},
		{[]string{"--help"}, 0},
	}
	for _, c := range cases {
		stdout, stderr, code := cli(t, []byte("not a pdf"), c.args...)
		if code != c.code {
			t.Errorf("%v: exit %d, want %d (%s)", c.args, code, c.code, stderr)
		}
		if len(stdout) != 0 {
			t.Errorf("%v: unexpected stdout %q", c.args, stdout)
		}
	}
	_, stderr, code := cli(t, []byte("not a pdf"), "-")
	if code != 1 || len(stderr) == 0 {
		t.Errorf("invalid PDF: exit %d, stderr %q", code, stderr)
	}
	_, stderr, _ = cli(t, nil, "--version")
	if !strings.HasPrefix(string(stderr), "pdfdistill-pdfcpu ") {
		t.Errorf("version output %q", stderr)
	}
}
