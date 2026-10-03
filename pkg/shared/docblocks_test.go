package shared

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
)

// This file is the part of documenting artemis that both documentation tests
// need: finding the fenced code blocks in a markdown file, and compiling the
// `art` ones. README.md and SPEC.md are both written in the DSL now, so they
// are held to it the same way -- one scanner and one compiler rather than two,
// so the two tests cannot come to disagree about what a code block is or about
// what makes one valid.

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

// isArtScenario reports whether a block is a whole scenario file rather than an
// excerpt: its first line that is neither blank nor a comment opens a
// `scenario`. Everything else -- a lone `expect`, a single `body =` line -- is a
// fragment the prose has already placed inside something.
func isArtScenario(b docBlock) bool {
	for _, line := range strings.Split(b.body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return strings.HasPrefix(line, "scenario ")
	}
	return false
}

// frontEnd is lex, parse and name-check over one file's source, with the two
// bags merged.
//
// It is pkg/cli/art.go's function of the same name, less the checker Info
// neither doc test has any use for. Merging rather than short-circuiting is the
// point: a block with a syntax error on one line and an unknown field on
// another reports both, so one test run finds everything wrong with an example.
func frontEnd(file, src string) *diag.Bag {
	tree, bag := parser.Parse(file, src)
	_, checked := check.Check(tree)
	bag.Merge(checked)
	return bag
}

// parseOnly is the front end stopped after the parser, which is as far as a
// fragment can be taken -- see compileBlock.
func parseOnly(file, src string) *diag.Bag {
	_, bag := parser.Parse(file, src)
	return bag
}

// errorsIn is every error diagnostic in a bag, rendered one per line for a test
// failure. Warnings are left out: a warning is not a reason to fail a document.
func errorsIn(bag *diag.Bag) string {
	var b strings.Builder
	for _, d := range bag.All() {
		if d.Severity != diag.Error {
			continue
		}
		fmt.Fprintf(&b, "\n  %d:%d: %s [%s]", d.Span.Line, d.Span.Col, d.Message, d.Code)
	}
	return b.String()
}

// fragmentContexts are the places a documented fragment can sit, outermost
// first. BLOCK is where the fragment goes.
//
// A doc example is prose-placed rather than a file: SPEC.md shows one `expect`
// line under its operator table and one `body =` line under "Bodies", and
// demanding a whole scenario around each would make the document worse to read
// in exchange for making it testable. So the test supplies what the prose
// already said, and these are the three levels a fragment can be at: a
// scenario's declarations, a step's statements, and an action block's fields.
var fragmentContexts = []string{
	"scenario \"doc\" {\nBLOCK\n}\n",
	"scenario \"doc\" {\n  step \"doc\" {\n    get \"https://example.test\"\nBLOCK\n  }\n}\n",
	"scenario \"doc\" {\n  step \"doc\" {\n    post \"https://example.test\" {\nBLOCK\n    }\n  }\n}\n",
}

// compileBlock holds one `art` block to compiling.
//
// A whole scenario is parsed as the file it is and must also name-check clean:
// every step's type inferred, every name resolvable, every field known. That is
// the strong check, and it is why a README example cannot drift from the
// language again.
//
// A fragment is wrapped in each context in turn and has to parse clean in one
// of them. Parse only, not check: a fragment names values its surrounding prose
// declares -- `url`, `token`, `limit` -- so resolving names would report an
// unknown identifier for an example that is correct, and SPEC.md's
// deliberately-rejected `match()` example is a checker error by design.
func compileBlock(t *testing.T, doc string, b docBlock) {
	t.Helper()

	if isArtScenario(b) {
		if bag := frontEnd(doc, b.body); bag.HasErrors() {
			t.Errorf("%s line %d: this scenario does not compile:%s\n%s", doc, b.line, errorsIn(bag), b.body)
		}
		return
	}

	var first *diag.Bag
	for _, context := range fragmentContexts {
		bag := parseOnly(doc, strings.Replace(context, "BLOCK", strings.TrimRight(b.body, "\n"), 1))
		if !bag.HasErrors() {
			return
		}
		if first == nil {
			first = bag
		}
	}
	// The outermost context's diagnostics, because a fragment that is not a
	// scenario declaration, a step statement or an action field is usually
	// nothing at all, and the first report is the one with the least scaffolding
	// of ours in it.
	t.Errorf("%s line %d: this fragment parses in no part of a scenario:%s\n%s", doc, b.line, errorsIn(first), b.body)
}
