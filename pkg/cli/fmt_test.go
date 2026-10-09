package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"artemis/pkg/dsl/print"
)

// uglyArt is cleanArt's scenario with the layout wrecked: tabs, runs of
// spaces, inline blocks that should break, a missing trailing newline. It
// parses clean, so it is a file `fmt` is allowed to touch.
const uglyArt = "scenario \"checkout\"   {\n\tvar url=env(\"API_URL\")\n\n\n  step \"login\"  {\n        post \"${url}/token\" { header \"Content-Type\" = \"application/json\"\n   body = {\"username\":\"alice\"} }\n\texpect status==200\n  }\n}"

// canonical is what the printer makes of src, by the same route the command
// takes. The expectation is derived rather than written out, because pinning
// canonical layout is pkg/dsl/print's job and a second copy of it here would
// be a second thing to update when a layout rule changes.
func canonical(t *testing.T, path, src string) string {
	t.Helper()
	u := frontEnd(path, src)
	tree, bag := u.Tree, u.Bag
	if bag.HasErrors() {
		t.Fatalf("%s does not parse clean: %v", path, bag.All())
	}
	return print.Canonical(tree)
}

// Without -w: the formatted file goes to stdout and the file on disk is left
// exactly as it was, so `artemis fmt x.art > out.art` is the whole of what the
// flagless form does.
func TestFmtPrintsToStdoutAndLeavesTheFileAlone(t *testing.T) {
	path := writeArt(t, "checkout.art", uglyArt)
	want := canonical(t, path, uglyArt)

	stdout, stderr, err := executeArgs(t, "fmt", path)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if stdout != want {
		t.Errorf("stdout =\n%q\nwant\n%q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}

	on, err := os.ReadFile(path) //nolint:gosec // a path this test wrote
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != uglyArt {
		t.Errorf("the file on disk changed without -w:\n%q", on)
	}
}

// -w rewrites the file, prints nothing, and is idempotent on disk: the second
// run leaves the same bytes and does not rewrite the file at all, which is what
// makes `artemis fmt -w` over an already-formatted suite free.
func TestFmtWriteIsIdempotentOnDisk(t *testing.T) {
	path := writeArt(t, "checkout.art", uglyArt)
	want := canonical(t, path, uglyArt)

	stdout, stderr, err := executeArgs(t, "fmt", "-w", path)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q, stderr = %q, want nothing: -w writes to the file", stdout, stderr)
	}

	first, err := os.ReadFile(path) //nolint:gosec // a path this test wrote
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != want {
		t.Fatalf("the file was not formatted:\n%q", first)
	}

	// Backdated, so "the second run did not write" is checkable: a write would
	// move the mtime to now.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := executeArgs(t, "fmt", "-w", path); err != nil {
		t.Fatalf("second Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	second, err := os.ReadFile(path) //nolint:gosec // a path this test wrote
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Errorf("formatting twice changed the file:\nfirst:\n%q\nsecond:\n%q", first, second)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().After(old) {
		t.Error("the second run rewrote an already-formatted file; it should have skipped the write")
	}
}

// The file keeps its permission bits. Formatting a file its owner kept to
// themselves must not publish it.
func TestFmtWriteKeepsTheFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkout.art")
	if err := os.WriteFile(path, []byte(uglyArt), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := executeArgs(t, "fmt", "-w", path); err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o, want 0600", got)
	}
}

// A file with an error is not formatted at all: the diagnostics are reported,
// the exit is non-zero, and the bytes on disk are exactly what they were.
// print.Canonical would print the bad lines verbatim, but idempotence is only
// claimed for a clean file, and rewriting a broken one is how a formatter loses
// someone's work.
func TestFmtRefusesAFileWithErrors(t *testing.T) {
	path := writeArt(t, "many_errors.art", manyErrorsArt)

	stdout, stderr, err := executeArgs(t, "fmt", "-w", path)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing: a file with errors is not formatted", stdout)
	}
	if !strings.Contains(stderr, "cannot be chained") {
		t.Errorf("stderr does not hold the diagnostics:\n%s", stderr)
	}

	on, err := os.ReadFile(path) //nolint:gosec // a path this test wrote
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != manyErrorsArt {
		t.Errorf("the file was rewritten despite its errors:\n%q", on)
	}
}

// Formatting is layout only: the formatted file still checks clean, which is
// the cheapest assertion that nothing about its meaning moved.
func TestFmtOutputChecksClean(t *testing.T) {
	path := writeArt(t, "checkout.art", uglyArt)
	if _, stderr, err := executeArgs(t, "fmt", "-w", path); err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeArgs(t, "parse", "-f", path)
	if err != nil {
		t.Fatalf("the formatted file does not check clean: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, ": ok") {
		t.Errorf("stdout = %q, want the ok line", stdout)
	}
}

// fmt has no YAML printer, and quietly doing nothing to a file somebody asked
// to have formatted is worse than saying so.
func TestFmtRejectsANonArtFile(t *testing.T) {
	path := writeArt(t, "scenario.yaml", "name: health\n")

	_, _, err := executeArgs(t, "fmt", path)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if !strings.Contains(err.Error(), ".art") {
		t.Errorf("Execute() = %q, want it to say which files fmt formats", err)
	}
}

// One path, and it is required: `artemis fmt` with nothing to format is a
// usage error rather than silence.
func TestFmtNeedsExactlyOnePath(t *testing.T) {
	if _, _, err := executeArgs(t, "fmt"); err == nil {
		t.Error("Execute() = nil, want an argument error for no path")
	}
	a := writeArt(t, "a.art", cleanArt)
	if _, _, err := executeArgs(t, "fmt", a, a); err == nil {
		t.Error("Execute() = nil, want an argument error for two paths")
	}
}
