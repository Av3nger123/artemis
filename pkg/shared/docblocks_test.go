package shared

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
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
// fragment the prose has already placed inside something. It is what the doc
// tests count; isArtFile is what they compile whole.
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

// isArtFile reports whether a block is a whole file rather than an excerpt:
// its first line that is neither blank nor a comment opens a scenario, an
// import or a collection. A file of collections and a file that imports them
// are as whole as a scenario is, and are compiled as one.
func isArtFile(b docBlock) bool {
	for _, line := range strings.Split(b.body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, opener := range []string{"scenario ", "import ", "collection "} {
			if strings.HasPrefix(line, opener) {
				return true
			}
		}
		return false
	}
	return false
}

// collectionPath is the path a documented collection file names in its first
// line -- `# collections/auth.art` -- and "" for any other block. It is how
// the documents say which file an example is, so that an example importing
// "collections/auth.art" can be compiled against the block that is that file.
func collectionPath(b docBlock) string {
	first, _, _ := strings.Cut(b.body, "\n")
	name, ok := strings.CutPrefix(strings.TrimSpace(first), "# ")
	if !ok || !strings.HasPrefix(name, "collections/") || !strings.HasSuffix(name, ".art") || strings.ContainsAny(name, " \t") {
		return ""
	}
	return name
}

// docCollections is the map loader both doc tests compile importing examples
// against: SPEC.md's collection blocks, keyed by the path each names in its
// first line. They are read out of SPEC.md rather than written here, so the
// collections an example imports are the ones the document shows and the two
// cannot drift. A block naming a path twice is a fatal error: one of the two
// would silently be the one that was tested.
func docCollections(t *testing.T) expand.MapLoader {
	t.Helper()

	files := expand.MapLoader{}
	for _, b := range docBlocks(t, specPath, "art") {
		name := collectionPath(b)
		if name == "" {
			continue
		}
		if _, dup := files[name]; dup {
			t.Fatalf("SPEC.md line %d: a second block for %s", b.line, name)
		}
		files[name] = b.body
	}
	if len(files) == 0 {
		t.Fatalf("SPEC.md has no block whose first line is # collections/<name>.art -- did the collections examples move?")
	}
	return files
}

// frontEnd is the whole front end over one documented file -- parse, expand
// its uses against the documented collections, name-check -- with every bag
// merged, through front.CompileWith, the function `artemis run` compiles with.
//
// Merging rather than short-circuiting is the point: a block with a syntax
// error on one line and an unknown field on another reports both, so one test
// run finds everything wrong with an example.
func frontEnd(file, src string, l expand.Loader) *diag.Bag {
	return front.CompileWith(file, src, l).Bag
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
		fmt.Fprintf(&b, "\n  %s:%d:%d: %s [%s]", d.Span.File, d.Span.Line, d.Span.Col, d.Message, d.Code)
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
// scenario's declarations, a step's statements, and an action block's fields --
// and a collection's items, for a lone request or flow.
var fragmentContexts = []string{
	"scenario \"doc\" {\nBLOCK\n}\n",
	"collection \"doc\" {\nBLOCK\n}\n",
	"scenario \"doc\" {\n  step \"doc\" {\n    get \"https://example.test\"\nBLOCK\n  }\n}\n",
	"scenario \"doc\" {\n  step \"doc\" {\n    post \"https://example.test\" {\nBLOCK\n    }\n  }\n}\n",
}

// compileBlock holds one `art` block to compiling.
//
// A whole file is parsed as the file it is and must also name-check clean:
// every step's type inferred, every name resolvable, every field known. That is
// the strong check, and it is why a README example cannot drift from the
// language again. A file that imports is expanded against l, the documented
// collections; a documented collection file is compiled under the path it
// names, so its own imports resolve beside it.
//
// A fragment is wrapped in each context in turn and has to parse clean in one
// of them. Parse only, not check: a fragment names values its surrounding prose
// declares -- `url`, `token`, `limit` -- so resolving names would report an
// unknown identifier for an example that is correct, and SPEC.md's
// deliberately-rejected `match()` example is a checker error by design.
func compileBlock(t *testing.T, doc string, b docBlock, l expand.Loader) {
	t.Helper()

	if isArtFile(b) {
		name := collectionPath(b)
		if name == "" {
			name = "doc.art"
		}
		if bag := frontEnd(name, b.body, l); bag.HasErrors() {
			t.Errorf("%s line %d: this file does not compile:%s\n%s", doc, b.line, errorsIn(bag), b.body)
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
