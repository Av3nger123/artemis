package shared

import (
	"os"
	"path/filepath"
	"testing"

	"artemis/pkg/shared/migrate"

	"gopkg.in/yaml.v3"
)

// readmePath is the README relative to this package's directory.
const readmePath = "../../README.md"

// knownTypes stands in for what the registry hands the loader in a real run.
// This package cannot read the registry -- the executors import it -- so the
// list is written out here.
var knownTypes = []string{"api", "exec"}

// wantREADMEScenarios is the number of whole-scenario examples the README is
// expected to carry. The test asserts it found at least this many, so an edit
// that renames the fences or deletes the examples fails here instead of leaving
// a test that passes on nothing.
const wantREADMEScenarios = 5

// TestREADMEYAMLBlocksAreWellFormed holds every fenced yaml block in the README
// to being valid YAML -- the fragments included, since a fragment a reader
// copies has to paste into a file cleanly too.
func TestREADMEYAMLBlocksAreWellFormed(t *testing.T) {
	for _, b := range docBlocks(t, readmePath, "yaml") {
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(b.body), &node); err != nil {
			t.Errorf("README line %d: not valid YAML: %v\n%s", b.line, err, b.body)
		}
	}
}

// TestREADMEScenariosParse runs every whole-scenario example in the README
// through migrate.ParseYAMLFile: strict decoding plus validation. A documented
// example that artemis would reject is a bug in the README, and this is what
// keeps the two from drifting apart again.
//
// That loader is no longer `artemis run`'s -- ART-40 took YAML off the run path
// -- so what this now asserts is that every YAML example in the README is one
// `artemis migrate` can still convert. The README itself is rewritten for the
// DSL in ART-42, and this test goes with its last YAML block.
func TestREADMEScenariosParse(t *testing.T) {
	scenarios := 0
	for _, b := range docBlocks(t, readmePath, "yaml") {
		if !isYAMLScenario(t, b) {
			continue
		}
		scenarios++

		path := filepath.Join(t.TempDir(), "readme.yaml")
		if err := os.WriteFile(path, []byte(b.body), 0o600); err != nil {
			t.Fatalf("writing the example from README line %d: %v", b.line, err)
		}
		config, err := migrate.ParseYAMLFile(path, knownTypes)
		if err != nil {
			t.Errorf("README line %d: artemis cannot load this example: %v\n%s", b.line, err, b.body)
			continue
		}
		if len(config.Steps) == 0 {
			t.Errorf("README line %d: example parsed to a scenario with no steps\n%s", b.line, b.body)
		}
	}

	if scenarios < wantREADMEScenarios {
		t.Errorf("found %d whole-scenario examples in the README, want at least %d -- did the examples move or lose their ```yaml fences?", scenarios, wantREADMEScenarios)
	}
}
