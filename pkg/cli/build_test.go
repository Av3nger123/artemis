package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/codegen"
)

// buildArt is the smallest scenario that exercises all three step types, so one
// fixture is enough for the command's behaviour; what the Python looks like is
// pinned by pkg/codegen's goldens, not here.
const buildArt = `scenario "checkout" {
  var base = env("BASE_URL")

  step "create one" {
    post "${base}/orders" {
      body = {"sku": "ART-1"}
    }
    expect status == 201
    capture id = body.id
  }

  step "the row is there" {
    run "psql" {
      args = ["-tAc", "select 1"]
    }
    expect exit_code == 0
  }
}
`

// Without -o the generated module goes to stdout and nothing is written, so
// `artemis build --lang=python x.art > test_x.py` is the file and nothing else.
func TestBuildPrintsToStdout(t *testing.T) {
	path := writeArt(t, "checkout.art", buildArt)

	stdout, stderr, err := executeArgs(t, "build", "--lang=python", path)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	for _, want := range []string{
		"ONE-WAY EXPORT",
		"def test_checkout():",
		"requests.post(",
		"assert status == 201",
		"id = body[\"id\"]",
		"subprocess.run(",
		"assert exit_code == 0",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}

	// Nothing beside the .art file.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("build without -o wrote something: %v", entries)
	}
}

// With -o the directory is created, the file is written into it, and its path is
// printed -- a command that writes files silently is one you have to go looking
// for.
func TestBuildWritesIntoTheDirectory(t *testing.T) {
	path := writeArt(t, "checkout.art", buildArt)
	out := filepath.Join(t.TempDir(), "tests", "generated")

	stdout, stderr, err := executeArgs(t, "build", "--lang=python", "-o", out, path)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	want := filepath.Join(out, "test_checkout.py")
	if strings.TrimSpace(stdout) != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	written, err := os.ReadFile(want) //nolint:gosec // a path this test chose
	if err != nil {
		t.Fatalf("the file was not written: %v", err)
	}
	if !strings.Contains(string(written), "def test_checkout():") {
		t.Errorf("%s does not hold the test function:\n%s", want, written)
	}
}

// Regenerating is the normal case, so -o overwrites rather than refusing. That is
// the opposite of `artemis generate`, whose output someone edits by hand.
func TestBuildOverwritesWithoutAsking(t *testing.T) {
	path := writeArt(t, "checkout.art", buildArt)
	out := t.TempDir()
	target := filepath.Join(out, "test_checkout.py")
	if err := os.WriteFile(target, []byte("# stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := executeArgs(t, "build", "--lang=python", "-o", out, path); err != nil {
		t.Fatalf("Execute() = %v\nstderr:\n%s", err, stderr)
	}
	written, err := os.ReadFile(target) //nolint:gosec // a path this test chose
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "stale") {
		t.Error("build left the stale file in place")
	}
}

// The reserved targets. `go` is reserved by the design's non-goals and `js` is
// not built yet, and both say so by name rather than being answered with a
// did-you-mean against the languages that do exist.
func TestBuildRefusesAReservedLanguage(t *testing.T) {
	path := writeArt(t, "checkout.art", buildArt)
	for _, lang := range codegen.Reserved() {
		_, _, err := executeArgs(t, "build", "--lang="+lang, path)
		if err == nil {
			t.Fatalf("build --lang=%s succeeded", lang)
		}
		if !errors.Is(err, codegen.ErrReserved) {
			t.Errorf("build --lang=%s = %v, want ErrReserved", lang, err)
		}
		why, _ := codegen.Why(lang)
		if !strings.Contains(err.Error(), why) {
			t.Errorf("build --lang=%s = %q, want the reason %q", lang, err, why)
		}
	}
}

func TestBuildRefusesAnUnknownLanguage(t *testing.T) {
	path := writeArt(t, "checkout.art", buildArt)
	_, _, err := executeArgs(t, "build", "--lang=ruby", path)
	if !errors.Is(err, codegen.ErrUnknownTarget) {
		t.Fatalf("build --lang=ruby = %v, want ErrUnknownTarget", err)
	}
	if !strings.Contains(err.Error(), "python") {
		t.Errorf("build --lang=ruby = %q, want it to name python", err)
	}
}

// --lang is required rather than defaulted, so an invocation that means python
// today cannot come to mean something else when a second target lands.
func TestBuildRequiresTheLanguage(t *testing.T) {
	path := writeArt(t, "checkout.art", buildArt)
	_, _, err := executeArgs(t, "build", path)
	if err == nil {
		t.Fatal("build with no --lang succeeded")
	}
	if !strings.Contains(err.Error(), "lang") {
		t.Errorf("build with no --lang = %q, want it to name the flag", err)
	}
}

// A YAML scenario is not a .art file, and saying so beats handing it to the lexer
// and printing a page of diagnostics about a file that was never one.
func TestBuildRefusesAFileThatIsNotArt(t *testing.T) {
	path := writeArt(t, "checkout.yaml", "name: checkout\n")
	_, _, err := executeArgs(t, "build", "--lang=python", path)
	if err == nil {
		t.Fatal("build accepted a YAML file")
	}
	if !strings.Contains(err.Error(), artExt) {
		t.Errorf("build on a .yaml file = %q, want it to name %s", err, artExt)
	}
}

// A file that does not compile is reported and nothing is written. This is the
// same front end `artemis parse` runs, so the diagnostics are the same ones, on
// stderr, with stdout left for the document that was not produced.
func TestBuildOnABrokenFileWritesNothing(t *testing.T) {
	path := writeArt(t, "broken.art", `scenario "x" {
  step "s" {
    get "https://example.test/"
    expect statu == 200
  }
}
`)
	out := t.TempDir()

	stdout, stderr, err := executeArgs(t, "build", "--lang=python", "-o", out, path)
	if err == nil {
		t.Fatal("build succeeded on a file with an error")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if !strings.Contains(stderr, "statu") {
		t.Errorf("stderr does not report the unknown name:\n%s", stderr)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("build wrote %v for a file that does not compile", entries)
	}
}

// The language is resolved before the file is read, so a reserved target answers
// about the target rather than spending a parse on a file it will not export.
func TestBuildChecksTheLanguageBeforeTheFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there.art")
	_, _, err := executeArgs(t, "build", "--lang=go", missing)
	if !errors.Is(err, codegen.ErrReserved) {
		t.Fatalf("build --lang=go on a missing file = %v, want ErrReserved", err)
	}
}

// Running it twice over an unchanged scenario leaves the same bytes, so a build
// step in a Makefile does not churn the diff every time it runs.
func TestBuildIsStableAcrossRuns(t *testing.T) {
	path := writeArt(t, "checkout.art", buildArt)

	first, _, err := executeArgs(t, "build", "--lang=python", path)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := executeArgs(t, "build", "--lang=python", path)
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Error("two builds of the same file printed different bytes")
	}
}
