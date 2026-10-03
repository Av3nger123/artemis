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

// corpusDir holds the corpus both targets export: one .art file a person could
// have written, and the module each target exports it to.
//
// One directory and not one per target, which is this issue's done-when made
// structural -- TestEveryFixtureHasAGolden requires a golden per target, so a
// rule the Python target implements and the JavaScript target forgets is a
// missing golden rather than a silent gap.
const corpusDir = "testdata/corpus"

// goldenTargets is every target the corpus is exported by, with the suffix its
// goldens carry and the name it gives a generated file.
//
// Read by every test in this file and by coverage_test.go, so a target added to
// the registry and not to this table is a target with no goldens, which
// TestEveryTargetHasGoldens reports by name.
var goldenTargets = []struct {
	target Target
	suffix string
	// path is the file name the target gives a fixture called name, which is
	// asserted rather than assumed: pytest and vitest discover by file name, so
	// the name is part of the contract and not a detail.
	path func(name string) string
}{{
	target: Python{},
	suffix: ".py.golden",
	path:   func(name string) string { return "test_" + name + ".py" },
}, {
	target: JS{},
	suffix: ".js.golden",
	path:   func(name string) string { return name + ".test.js" },
}}

// TestGoldens is the issue's done-when, and the corpus is chosen so that between
// them the fixtures use every step type, every operator, every builtin, every
// browser action and every block field -- which TestCorpusCoversTheLanguage
// checks rather than asserts.
//
// Each case is <name>.art beside one golden per target. The pair is the point:
// the .art file reads as a scenario and each golden reads as a module of its own
// runner, so a change to an emitter shows up as a diff a person can judge -- and
// the two goldens side by side are the only place the two targets can be
// compared at all.
func TestGoldens(t *testing.T) {
	for _, tc := range goldenTargets {
		t.Run(tc.target.Name(), func(t *testing.T) {
			for _, name := range corpus(t) {
				t.Run(name, func(t *testing.T) {
					artPath := filepath.Join(corpusDir, name+artExt)
					src, err := os.ReadFile(artPath)
					if err != nil {
						t.Fatal(err)
					}
					tree := parse(t, artPath, string(src))

					files, err := tc.target.Generate(tree)
					if err != nil {
						t.Fatalf("Generate: %v", err)
					}
					if len(files) != 1 {
						t.Fatalf("Generate returned %d files, want 1 per .art file", len(files))
					}
					if want := tc.path(name); files[0].Path != want {
						t.Errorf("Path = %q, want %q", files[0].Path, want)
					}

					goldenPath := filepath.Join(corpusDir, name+tc.suffix)
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
		})
	}
}

// Every implemented target has goldens. Without this the registry could grow a
// target whose output nothing ever looked at, which is the one way this file
// could be green and prove nothing about a language artemis claims to export.
func TestEveryTargetHasGoldens(t *testing.T) {
	covered := map[string]bool{}
	for _, tc := range goldenTargets {
		covered[tc.target.Name()] = true
	}
	for _, name := range Names() {
		if !covered[name] {
			t.Errorf("the %s target has no goldens; add it to goldenTargets in golden_test.go", name)
		}
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

// And the same question of node, for the same reason.
//
// `node --check` decides a file is ESM or not by its extension, so each golden
// is written to a .mjs file first -- the generated module is .js with import
// statements, which Vite transforms whatever the host package.json says, and
// node on its own would reject.
func TestGoldensAreValidJavaScript(t *testing.T) {
	node := nodeBinary(t)
	dir := t.TempDir()
	for _, name := range corpus(t) {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(corpusDir, name+".js.golden"))
			if err != nil {
				t.Skipf("%v; run `go test ./pkg/codegen -update` first", err)
			}
			if err := parseJavaScript(node, dir, name, string(src)); err != nil {
				t.Errorf("%s.js.golden is not valid javascript: %v", name, err)
			}
		})
	}
}

// Generating twice must produce the same bytes, which is what makes the goldens
// worth having and `artemis build` safe to run from a Makefile.
func TestGeneratingTwiceIsIdentical(t *testing.T) {
	for _, tc := range goldenTargets {
		for _, name := range corpus(t) {
			artPath := filepath.Join(corpusDir, name+artExt)
			src, err := os.ReadFile(artPath)
			if err != nil {
				t.Fatal(err)
			}
			first, err := tc.target.Generate(parse(t, artPath, string(src)))
			if err != nil {
				t.Fatalf("%s %s: %v", tc.target.Name(), name, err)
			}
			again, err := tc.target.Generate(parse(t, artPath, string(src)))
			if err != nil {
				t.Fatalf("%s %s: %v", tc.target.Name(), name, err)
			}
			if first[0].Content != again[0].Content {
				t.Errorf("%s generated %s differently the second time", tc.target.Name(), name)
			}
		}
	}
}

// corpus is the fixture names in testdata/corpus, sorted by the walk.
func corpus(t *testing.T) []string {
	t.Helper()
	return corpusIn(t, corpusDir)
}

// corpusIn is the .art file names in one corpus directory, without their
// extension, sorted by the glob.
//
// Two corpora read it: testdata/corpus, whose fixtures have a golden per target
// beside them, and testdata/conformance, whose scenarios are run every way by
// parity_test.go and jsparity_test.go. A directory with no .art file in it is a
// mistake rather than an empty corpus -- a renamed folder would otherwise assert
// nothing and say nothing.
func corpusIn(t *testing.T, dir string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*"+artExt))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures in %s", dir)
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
