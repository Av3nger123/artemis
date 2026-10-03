package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// -update rewrites the goldens from what the target actually prints:
//
//	go test ./pkg/codegen -update
//
// or `make golden`. Read the resulting diff before committing it -- a golden that
// is regenerated without being read asserts nothing.
var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// corpusDir holds the Python target's corpus: one .art file a person could have
// written, and the module it exports to.
const corpusDir = "testdata/python"

// TestPythonGoldens is the issue's done-when, and the corpus is chosen so that
// between them the fixtures use every step type, every operator, every builtin,
// every browser action and every block field -- which TestCorpusCoversTheLanguage
// checks rather than asserts.
//
// Each case is <name>.art beside <name>.py.golden. The pair is the point: the
// .art file reads as a scenario and the golden reads as a pytest module, so a
// change to the emitter shows up as a diff a person can judge.
func TestPythonGoldens(t *testing.T) {
	for _, name := range corpus(t) {
		t.Run(name, func(t *testing.T) {
			artPath := filepath.Join(corpusDir, name+artExt)
			src, err := os.ReadFile(artPath)
			if err != nil {
				t.Fatal(err)
			}
			tree := parse(t, artPath, string(src))

			files, err := Python{}.Generate(tree)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if len(files) != 1 {
				t.Fatalf("Generate returned %d files, want 1 per .art file", len(files))
			}
			if want := "test_" + name + ".py"; files[0].Path != want {
				t.Errorf("Path = %q, want %q", files[0].Path, want)
			}

			goldenPath := filepath.Join(corpusDir, name+".py.golden")
			if *update {
				if err := os.WriteFile(goldenPath, []byte(files[0].Content), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("%v (run `go test ./pkg/codegen -update` to write it)", err)
			}
			if files[0].Content != string(want) {
				t.Errorf("%s does not match %s:\n%s", artPath, goldenPath,
					firstDifference(string(want), files[0].Content))
			}
		})
	}
}

// TestGoldensAreValidPython asks Python whether every golden parses.
//
// Running the generated tests against a server is ART-49; this is the cheap half
// of it, and it catches the whole class of emission bug that produces a file
// which looks right and will not import. It skips with a note when python3 is
// absent, the way `make lint` skips a missing linter.
func TestGoldensAreValidPython(t *testing.T) {
	python := python3(t)
	for _, name := range corpus(t) {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(corpusDir, name+".py.golden"))
			if err != nil {
				t.Skipf("%v; run `go test ./pkg/codegen -update` first", err)
			}
			if err := parsePython(python, string(src)); err != nil {
				t.Errorf("%s.py.golden is not valid python: %v", name, err)
			}
		})
	}
}

// Generating twice must produce the same bytes, which is what makes the goldens
// worth having and `artemis build` safe to run from a Makefile.
func TestGeneratingTwiceIsIdentical(t *testing.T) {
	for _, name := range corpus(t) {
		artPath := filepath.Join(corpusDir, name+artExt)
		src, err := os.ReadFile(artPath)
		if err != nil {
			t.Fatal(err)
		}
		first, err := Python{}.Generate(parse(t, artPath, string(src)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		again, err := Python{}.Generate(parse(t, artPath, string(src)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if first[0].Content != again[0].Content {
			t.Errorf("%s generated differently the second time", name)
		}
	}
}

// corpus is the fixture names in testdata/python, sorted by the walk.
func corpus(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(corpusDir, "*"+artExt))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures in %s", corpusDir)
	}
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, strings.TrimSuffix(filepath.Base(p), artExt))
	}
	return names
}

// firstDifference is the first line the two differ on, with its number, because a
// whole-file diff of a generated module buries the one line that moved.
func firstDifference(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		w, g := "<end of file>", "<end of file>"
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			return "line " + strconv.Itoa(i+1) + "\n want: " + w + "\n  got: " + g
		}
	}
	return "the files are equal, so something else differs"
}
