package shared

import (
	"fmt"
	"os"
	"path"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
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

	blocks := fencedBlocks(t, path, lang)
	if len(blocks) == 0 {
		t.Fatalf("no ```%s blocks found in %s", lang, path)
	}
	return blocks
}

// fencedBlocks is docBlocks for a document that may have none: one guide among
// several, where the test asserts on what all of them hold together.
func fencedBlocks(t *testing.T, path, lang string) []docBlock {
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

// isArtTest reports whether a block is a whole test rather than an excerpt or
// a collection: a flow file, `flow checkout { ... }`, or a scenario file, whose
// first line is `flow "../flows/checkout.art"`. It is what the doc tests
// count, so that an edit which loses the examples fails rather than leaving a
// test that passes on nothing; isArtFile is what they compile whole.
func isArtTest(b docBlock) bool {
	tree, _ := parser.Parse("doc.art", b.body)
	for _, d := range tree.Scenarios {
		switch d.(type) {
		case *ast.Flow, *ast.FlowRef:
			return true
		}
	}
	return false
}

// isArtFile reports whether a block is a whole file rather than an excerpt:
// its first line that is neither blank nor a comment opens an import, a
// collection, a flow, a scenario file's flow header or a scenario. Every kind
// of file is as whole as another, and is compiled as one.
func isArtFile(b docBlock) bool {
	for _, line := range strings.Split(b.body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, opener := range []string{"import ", "collection ", "flow ", "scenario "} {
			if strings.HasPrefix(line, opener) {
				return true
			}
		}
		return false
	}
	return false
}

// docDirs are the folders a documented file can name itself under: the three
// kinds of file, each in the folder the documents keep it in.
var docDirs = []string{"collections/", "flows/", "scenarios/"}

// docPath is the path a documented file names in its first line --
// `# collections/auth.art`, `# flows/checkout.art`, `# scenarios/checkout.art`
// -- and "" for any other block. It is how the documents say which file an
// example is, so that a flow importing "../collections/auth.art", or a
// scenario file naming "../flows/checkout.art", can be compiled against the
// block that is that file.
func docPath(b docBlock) string {
	first, _, _ := strings.Cut(b.body, "\n")
	name, ok := strings.CutPrefix(strings.TrimSpace(first), "# ")
	if !ok || !strings.HasSuffix(name, ".art") || strings.ContainsAny(name, " \t") {
		return ""
	}
	for _, dir := range docDirs {
		if strings.HasPrefix(name, dir) {
			return name
		}
	}
	return ""
}

// docFiles is every block in the document at doc that names its path, keyed
// by that path. A document's flows and scenario files are its own -- two
// guides may each show a flows/checkout.art -- but within one document a
// path is one file, and a second block for it is a fatal error: one of the
// two would silently be the one that was tested.
func docFiles(t *testing.T, doc string) expand.MapLoader {
	t.Helper()

	files := expand.MapLoader{}
	for _, b := range fencedBlocks(t, doc, "art") {
		name := docPath(b)
		if name == "" {
			continue
		}
		if _, dup := files[name]; dup {
			t.Fatalf("%s line %d: a second block for %s", doc, b.line, name)
		}
		files[name] = b.body
	}
	return files
}

// docCollections is SPEC.md's collection blocks, keyed by the path each names
// in its first line. Every document's examples are compiled against them, so
// the collections an example imports are the ones the reference shows, and a
// guide that shows a collection has to show SPEC.md's (see
// TestGuideCollectionsAreSPECs).
func docCollections(t *testing.T) expand.MapLoader {
	t.Helper()

	files := expand.MapLoader{}
	for name, src := range docFiles(t, specPath) {
		if strings.HasPrefix(name, "collections/") {
			files[name] = src
		}
	}
	if len(files) == 0 {
		t.Fatalf("SPEC.md has no block whose first line is # collections/<name>.art -- did the collections examples move?")
	}
	return files
}

// docLoaderFor is the loader the examples of the document at doc compile
// against: SPEC.md's collections, and the document's own named files over
// them.
func docLoaderFor(t *testing.T, doc string) docLoader {
	t.Helper()

	files := docCollections(t)
	for name, src := range docFiles(t, doc) {
		files[name] = src
	}
	return docLoader{files: files}
}

// docLoader serves the files an example imports or names, and a stand-in for
// every data file it reads: json() and csv() in a document name files the
// document does not show, and what they hold is beside the point of the
// example. A .json path serves one row holding the fields the documents'
// examples read, and a .csv path a header and a row; whether a path names a
// file at all is pkg/dsl/front's test, not the documents'.
type docLoader struct{ files expand.MapLoader }

func (d docLoader) Load(from, p string) (string, string, error) {
	if strings.HasSuffix(p, ".art") {
		return d.files.Load(from, p)
	}
	switch {
	case strings.HasSuffix(p, ".json"):
		return p, docRow, nil
	case strings.HasSuffix(p, ".csv"):
		return p, "name,plan\nalice,pro\n", nil
	}
	return p, "", nil
}

// with is d serving src at name as well.
func (d docLoader) with(name, src string) docLoader {
	files := expand.MapLoader{name: src}
	for k, v := range d.files {
		if k != name {
			files[k] = v
		}
	}
	return docLoader{files: files}
}

// docRow is the one row a documented JSON file holds.
const docRow = `[{"id": 1, "name": "alice", "role": "admin", "email": "alice@example.com",
  "password": "s3cret", "plan": "pro", "expect_status": 200}]`

// frontEnd is the whole front end over one documented file -- parse, expand
// its imports and item calls against the documented files, name-check -- with
// every bag merged, through front.CompileWith, the function `artemis run`
// compiles with.
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
// demanding a whole flow around each would make the document worse to read in
// exchange for making it testable. So the test supplies what the prose already
// said, and these are the levels a fragment can be at: a flow's declarations,
// a collection's items, a step's statements, an action block's fields, and
// the lines of a scenario in a scenario file.
var fragmentContexts = []string{
	"flow doc {\nBLOCK\n}\n",
	"collection \"doc\" {\nBLOCK\n}\n",
	"flow doc {\n  step \"doc\" {\n    get \"https://example.test\"\nBLOCK\n  }\n}\n",
	"flow doc {\n  step \"doc\" {\n    post \"https://example.test\" {\nBLOCK\n    }\n  }\n}\n",
	"flow \"doc.art\"\n\nscenario \"doc\" {\nBLOCK\n}\n",
}

// compileBlock holds one `art` block to compiling.
//
// A whole file is parsed as the file it is and must also name-check clean:
// every step's type inferred, every name resolvable, every field known. That is
// the strong check, and it is why a README example cannot drift from the
// language again. A file is compiled under the path it names, so its imports
// and its flow header resolve beside it against l, the documented files.
//
// A flow is checked when a scenario file names it, so a flow file is also
// compiled through a scenario file of its own, made here, that names it and
// sets each var it requires: a flow no document's scenario file runs is still
// held to the checker.
//
// A fragment is wrapped in each context in turn and has to parse clean in one
// of them. Parse only, not check: a fragment names values its surrounding prose
// declares -- `url`, `token`, `limit` -- so resolving names would report an
// unknown identifier for an example that is correct, and SPEC.md's
// deliberately-rejected `match()` example is a checker error by design.
func compileBlock(t *testing.T, doc string, b docBlock, l docLoader) {
	t.Helper()

	if isArtFile(b) {
		name := docPath(b)
		if name == "" {
			name = "doc.art"
		}
		if bag := frontEnd(name, b.body, l); bag.HasErrors() {
			t.Errorf("%s line %d: this file does not compile:%s\n%s", doc, b.line, errorsIn(bag), b.body)
			return
		}
		if src, ok := flowCheck(name, b.body); ok {
			check := path.Join(path.Dir(name), "doc-check.art")
			if bag := frontEnd(check, src, l.with(name, b.body)); bag.HasErrors() {
				t.Errorf("%s line %d: this flow does not compile when a scenario runs it:%s\n%s", doc, b.line, errorsIn(bag), b.body)
			}
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
	// flow declaration, a step statement or an action field is usually
	// nothing at all, and the first report is the one with the least scaffolding
	// of ours in it.
	t.Errorf("%s line %d: this fragment parses in no part of a flow:%s\n%s", doc, b.line, errorsIn(first), b.body)
}

// flowCheck is a scenario file beside the flow file name that runs it once,
// setting each var the flow requires to null, and false when src declares no
// flow.
func flowCheck(name, src string) (string, bool) {
	tree, _ := parser.Parse(name, src)
	for _, d := range tree.Scenarios {
		f, ok := d.(*ast.Flow)
		if !ok {
			continue
		}
		var sets []string
	top:
		for _, d := range f.Body {
			switch v := d.(type) {
			case *ast.VarDecl:
				if v.Required() && v.Private.Text == "" {
					sets = append(sets, "  "+v.Name.Value+" = null\n")
				}
			case *ast.ConfigDecl:
			default:
				break top
			}
		}
		return fmt.Sprintf("flow %q\n\nscenario \"doc\" {\n%s}\n", path.Base(name), strings.Join(sets, "")), true
	}
	return "", false
}
