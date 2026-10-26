package shared

import (
	"os"
	"path/filepath"
	"testing"

	"artemis/pkg/shared/migrate"
)

// readmePath is the README relative to this package's directory, and guidesGlob
// the user guides beside it.
const (
	readmePath = "../../README.md"
	guidesGlob = "../../docs/*.md"
)

// knownTypes stands in for what the registry hands the YAML loader in a real
// migration. This package cannot read the registry -- the executors import it
// -- so the list is written out here.
var knownTypes = []string{"api", "exec"}

// wantGuideScenarios is the number of whole-scenario examples the README and
// the guides are expected to carry between them. The test asserts it found at
// least this many, so an edit that renames the fences or deletes the examples
// fails here instead of leaving a test that passes on nothing.
const wantGuideScenarios = 5

// guides is every document written for someone using artemis: the README and
// each page under docs/. SPEC.md is the reference, and spec_test.go holds it
// to the code on its own terms.
func guides(t *testing.T) []string {
	t.Helper()

	pages, err := filepath.Glob(guidesGlob)
	if err != nil {
		t.Fatalf("listing the guides: %v", err)
	}
	if len(pages) == 0 {
		t.Fatalf("no guides match %s", guidesGlob)
	}
	return append([]string{readmePath}, pages...)
}

// TestGuideArtExamplesCompile holds every fenced art block in the README and
// the guides to the language artemis actually reads -- the fragments included,
// since a fragment a reader copies has to belong somewhere in a scenario too.
//
// A whole scenario is parsed and name-checked as the file it is; a fragment is
// wrapped in the part of a scenario its prose puts it in and has to parse. An
// example here that artemis would reject is a failing test, not a surprise for
// whoever copies it, and that is what keeps the guides and the language from
// drifting apart the way they did before the DSL landed.
func TestGuideArtExamplesCompile(t *testing.T) {
	collections := docLoader{docCollections(t)}
	scenarios := 0
	for _, doc := range guides(t) {
		for _, b := range fencedBlocks(t, doc, "art") {
			if isArtScenario(b) {
				scenarios++
			}
			compileBlock(t, doc, b, collections)
		}
	}

	if scenarios < wantGuideScenarios {
		t.Errorf("found %d whole-scenario examples in the README and docs/, want at least %d -- did the examples move or lose their ```art fences?",
			scenarios, wantGuideScenarios)
	}
}

// TestGuideCollectionsAreSPECs holds a collection file a guide shows to the one
// SPEC.md shows under the same path. Examples that import are compiled against
// SPEC.md's collection blocks, so a guide's copy that drifted would be a file
// the guide shows and no test compiles anything against.
func TestGuideCollectionsAreSPECs(t *testing.T) {
	spec := docCollections(t)
	for _, doc := range guides(t) {
		for _, b := range fencedBlocks(t, doc, "art") {
			name := collectionPath(b)
			if name == "" {
				continue
			}
			want, ok := spec[name]
			if !ok {
				t.Errorf("%s line %d: %s is not a collection SPEC.md documents", doc, b.line, name)
				continue
			}
			if b.body != want {
				t.Errorf("%s line %d: %s differs from SPEC.md's block for it\n%s:\n%s\nSPEC.md:\n%s", doc, b.line, name, doc, b.body, want)
			}
		}
	}
}

// TestGuideMigrateInputLoads holds the guides' remaining YAML to being YAML
// `artemis migrate` can still read.
//
// There is one such block left in the repository and this is it: the input
// docs/migrating.md shows being converted. It is loaded through
// migrate.ParseYAMLFile -- strict decoding plus validation, the same call the
// command makes -- so a documented migration input that migrate would refuse
// fails here. Every other example is `.art`.
func TestGuideMigrateInputLoads(t *testing.T) {
	found := 0
	for _, doc := range guides(t) {
		for _, b := range fencedBlocks(t, doc, "yaml") {
			found++
			path := filepath.Join(t.TempDir(), "guide.yaml")
			if err := os.WriteFile(path, []byte(b.body), 0o600); err != nil {
				t.Fatalf("writing the example from %s line %d: %v", doc, b.line, err)
			}
			config, err := migrate.ParseYAMLFile(path, knownTypes)
			if err != nil {
				t.Errorf("%s line %d: artemis migrate cannot read this example: %v\n%s", doc, b.line, err, b.body)
				continue
			}
			if len(config.Steps) == 0 {
				t.Errorf("%s line %d: example parsed to a scenario with no steps\n%s", doc, b.line, b.body)
			}
		}
	}
	if found == 0 {
		t.Error("no ```yaml block in the README or docs/: the migrate input moved or lost its fence")
	}
}
