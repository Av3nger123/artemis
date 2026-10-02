package shared

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// This file is the part of documenting artemis that both documentation tests
// need: finding the fenced code blocks in a markdown file. The README's blocks
// are YAML and are loaded through the real decoder (readme_test.go); SPEC.md's
// are `.art` and can only be linted, because nothing parses that language yet
// (spec_test.go). One scanner rather than two, so the two tests cannot come to
// disagree about what a code block is.

// docBlock is one fenced code block, with the 1-based line of the document its
// opening fence sits on so a failure can be navigated to.
type docBlock struct {
	line int
	body string
}

// docBlocks returns every fenced block of the given language in path, in order.
//
// A document with none at all is a fatal error rather than an empty result: a
// test that silently checks nothing is worse than no test, and a renamed fence
// is exactly how that happens.
func docBlocks(t *testing.T, path, lang string) []docBlock {
	t.Helper()

	lines := strings.Split(docText(t, path), "\n")
	var blocks []docBlock
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```"+lang {
			continue
		}
		open, end := i+1, i+1
		for end < len(lines) && strings.TrimSpace(lines[end]) != "```" {
			end++
		}
		if end == len(lines) {
			t.Fatalf("%s line %d: %s fence is never closed", path, open, lang)
		}
		blocks = append(blocks, docBlock{line: open, body: strings.Join(lines[i+1:end], "\n") + "\n"})
		i = end
	}

	if len(blocks) == 0 {
		t.Fatalf("no ```%s blocks found in %s", lang, path)
	}
	return blocks
}

// docText is the whole document, for a check about its prose rather than its
// examples -- that a list in it matches a list in the code.
func docText(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// isYAMLScenario reports whether a block is a whole scenario rather than an
// excerpt: a mapping at the top level with a steps: key. Everything else -- a
// lone response:, a single body: line -- is only checked for shape.
func isYAMLScenario(t *testing.T, b docBlock) bool {
	t.Helper()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(b.body), &doc); err != nil {
		// Reported by the well-formedness test; nothing to add here.
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
