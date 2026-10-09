package shared

import (
	"os"
	"path/filepath"
	"testing"

	"artemis/pkg/shared/migrate"
)

// readmePath is the README relative to this package's directory.
const readmePath = "../../README.md"

// knownTypes stands in for what the registry hands the YAML loader in a real
// migration. This package cannot read the registry -- the executors import it
// -- so the list is written out here.
var knownTypes = []string{"api", "exec"}

// wantREADMEScenarios is the number of whole-scenario examples the README is
// expected to carry. The test asserts it found at least this many, so an edit
// that renames the fences or deletes the examples fails here instead of leaving
// a test that passes on nothing.
const wantREADMEScenarios = 5

// TestREADMEArtExamplesCompile holds every fenced art block in the README to the
// language artemis actually reads -- the fragments included, since a fragment a
// reader copies has to belong somewhere in a scenario too.
//
// A whole scenario is parsed and name-checked as the file it is; a fragment is
// wrapped in the part of a scenario its prose puts it in and has to parse. An
// example here that artemis would reject is a failing test, not a surprise for
// whoever copies it, and that is what keeps the README and the language from
// drifting apart the way they did before the DSL landed.
func TestREADMEArtExamplesCompile(t *testing.T) {
	collections := docCollections(t)
	scenarios := 0
	for _, b := range docBlocks(t, readmePath, "art") {
		if isArtScenario(b) {
			scenarios++
		}
		compileBlock(t, readmePath, b, collections)
	}

	if scenarios < wantREADMEScenarios {
		t.Errorf("found %d whole-scenario examples in the README, want at least %d -- did the examples move or lose their ```art fences?",
			scenarios, wantREADMEScenarios)
	}
}

// TestREADMECollectionsAreSPECs holds a collection file the README shows to
// the one SPEC.md shows under the same path. README examples that import are
// compiled against SPEC.md's collection blocks, so a README copy that drifted
// would be a file the README shows and no test compiles anything against.
func TestREADMECollectionsAreSPECs(t *testing.T) {
	spec := docCollections(t)
	for _, b := range docBlocks(t, readmePath, "art") {
		name := collectionPath(b)
		if name == "" {
			continue
		}
		want, ok := spec[name]
		if !ok {
			t.Errorf("README line %d: %s is not a collection SPEC.md documents", b.line, name)
			continue
		}
		if b.body != want {
			t.Errorf("README line %d: %s differs from SPEC.md's block for it\nREADME:\n%s\nSPEC.md:\n%s", b.line, name, b.body, want)
		}
	}
}

// TestREADMEMigrateInputLoads holds the README's remaining YAML to being YAML
// `artemis migrate` can still read.
//
// There is one such block left in the repository and this is it: the input the
// README shows being converted. It is loaded through migrate.ParseYAMLFile --
// strict decoding plus validation, the same call the command makes -- so a
// documented migration input that migrate would refuse fails here. Every other
// example in the README is `.art`.
func TestREADMEMigrateInputLoads(t *testing.T) {
	for _, b := range docBlocks(t, readmePath, "yaml") {
		path := filepath.Join(t.TempDir(), "readme.yaml")
		if err := os.WriteFile(path, []byte(b.body), 0o600); err != nil {
			t.Fatalf("writing the example from README line %d: %v", b.line, err)
		}
		config, err := migrate.ParseYAMLFile(path, knownTypes)
		if err != nil {
			t.Errorf("README line %d: artemis migrate cannot read this example: %v\n%s", b.line, err, b.body)
			continue
		}
		if len(config.Steps) == 0 {
			t.Errorf("README line %d: example parsed to a scenario with no steps\n%s", b.line, b.body)
		}
	}
}
