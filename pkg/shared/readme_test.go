package shared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// readmePath is the README relative to this package's directory.
const readmePath = "../../README.md"

// wantScenarios is the number of whole-scenario examples the README is expected
// to carry. The test asserts it found at least this many, so an edit that
// renames the fences or deletes the examples fails here instead of leaving a
// test that passes on nothing.
const wantScenarios = 5

// readmeBlock is one fenced yaml block, with the 1-based README line its
// opening fence sits on so a failure can be navigated to.
type readmeBlock struct {
	line int
	body string
}

// TestREADMEYAMLBlocksAreWellFormed holds every fenced yaml block in the README
// to being valid YAML -- the fragments included, since a fragment a reader
// copies has to paste into a file cleanly too.
func TestREADMEYAMLBlocksAreWellFormed(t *testing.T) {
	for _, b := range readmeYAMLBlocks(t) {
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(b.body), &node); err != nil {
			t.Errorf("README line %d: not valid YAML: %v\n%s", b.line, err, b.body)
		}
	}
}

// TestREADMEScenariosParse runs every whole-scenario example in the README
// through the loader `artemis test -f` uses: strict decoding plus validation. A
// documented example that artemis would reject is a bug in the README, and this
// is what keeps the two from drifting apart again.
func TestREADMEScenariosParse(t *testing.T) {
	scenarios := 0
	for _, b := range readmeYAMLBlocks(t) {
		if !isScenario(t, b) {
			continue
		}
		scenarios++

		path := filepath.Join(t.TempDir(), "readme.yaml")
		if err := os.WriteFile(path, []byte(b.body), 0o600); err != nil {
			t.Fatalf("writing the example from README line %d: %v", b.line, err)
		}
		config, err := ParseYAMLFile(path, knownTypes)
		if err != nil {
			t.Errorf("README line %d: artemis cannot load this example: %v\n%s", b.line, err, b.body)
			continue
		}
		if len(config.Steps) == 0 {
			t.Errorf("README line %d: example parsed to a scenario with no steps\n%s", b.line, b.body)
		}
	}

	if scenarios < wantScenarios {
		t.Errorf("found %d whole-scenario examples in the README, want at least %d -- did the examples move or lose their ```yaml fences?", scenarios, wantScenarios)
	}
}

// isScenario reports whether a block is a whole scenario rather than an excerpt:
// a mapping at the top level with a steps: key. Everything else -- a lone
// response:, a single body: line -- is only checked for shape.
func isScenario(t *testing.T, b readmeBlock) bool {
	t.Helper()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(b.body), &doc); err != nil {
		// Reported by TestREADMEYAMLBlocksAreWellFormed; nothing to add here.
		return false
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false
	}
	// Content alternates key, value, key, value.
	for i := 0; i+1 < len(doc.Content[0].Content); i += 2 {
		if doc.Content[0].Content[i].Value == "steps" {
			return true
		}
	}
	return false
}

// readmeYAMLBlocks returns every ```yaml block in the README, in order.
func readmeYAMLBlocks(t *testing.T) []readmeBlock {
	t.Helper()

	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("reading %s: %v", readmePath, err)
	}

	lines := strings.Split(string(data), "\n")
	var blocks []readmeBlock
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```yaml" {
			continue
		}
		open, end := i+1, i+1
		for end < len(lines) && strings.TrimSpace(lines[end]) != "```" {
			end++
		}
		if end == len(lines) {
			t.Fatalf("README line %d: yaml fence is never closed", open)
		}
		blocks = append(blocks, readmeBlock{line: open, body: strings.Join(lines[i+1:end], "\n") + "\n"})
		i = end
	}

	if len(blocks) == 0 {
		t.Fatalf("no ```yaml blocks found in %s", readmePath)
	}
	return blocks
}
