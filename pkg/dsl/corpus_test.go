// Package dsl holds no code. It holds the corpus of invalid .art files that
// pins what the front end *says* about them.
//
// Diagnostics are the feature this rewrite exists for -- the design's bar is
// "a compiler's, not a decoder's" -- so they are tested like a feature rather
// than like log formatting: every fixture in testdata/invalid pins the full
// text a person receives, and any change to a message, a span, a severity, a
// hint, a suggestion or a code is a failure here that someone has to read and
// approve.
//
// The driver lives above pkg/dsl/parser rather than inside it because the
// corpus is about the front end as a whole. Today that is parser.Parse; ART-33
// adds check.Check to frontEnd below and nothing else here changes. It is not
// in testdata/ itself because Go tooling ignores that directory, and it does
// not shell out to the binary because `artemis parse` does not exist yet and a
// golden that depended on a command's framing would move when the framing did.
//
// # Fixtures for stages that do not exist yet
//
// Six of the eleven faults the corpus must cover -- unknown field, out-of-scope
// root, reserved word, uncompilable regex, bad duration, unknown identifier --
// are pkg/dsl/check's, which lands in ART-33. The design calls for these
// goldens to be written *before* the checker is finished, because that is what
// stops diagnostics being under-built, and an empty golden with a note asserts
// nothing. So such a fixture declares what it is waiting for:
//
//	# todo(ART-33): unknown-field
//
// and its golden is hand-written: the text ART-33 is expected to produce. The
// test then asserts the two things that are checkable today -- the file parses
// with *zero* diagnostics, so nothing reports a false syntax error on code
// whose only fault is a name, and the golden's code footer is exactly the todo
// list -- and fails loudly the moment the stage lands, naming the lines to
// delete. -update never rewrites such a golden.
package dsl

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
)

// -update rewrites the goldens of the live fixtures from what the front end
// actually prints:
//
//	go test ./pkg/dsl -update
//
// or `make golden`. Read the diff before committing it. A golden regenerated
// without being read asserts that the code does what the code does.
var update = flag.Bool("update", false, "rewrite the goldens of the live fixtures in testdata/invalid")

const corpusDir = "testdata/invalid"

// TestInvalidCorpus is the whole issue: one subtest per .art file in
// testdata/invalid, each pinned to its golden.
func TestInvalidCorpus(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(corpusDir, "*.art"))
	if err != nil {
		t.Fatalf("globbing the corpus: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no fixtures in %s", corpusDir)
	}
	sort.Strings(files)

	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".art")
		t.Run(name, func(t *testing.T) {
			src := read(t, path)
			tree, bag := frontEnd(filepath.Base(path), src)

			// Holds for every input, valid or not: the tree is the source
			// again. ART-31 asserts it over well-formed files; the point of
			// asserting it here is that these files are chosen to be hostile,
			// and a token dropped by recovery would make ART-34's printer
			// silently lossy on exactly the files a person is editing.
			if got := ast.Source(tree); got != src {
				t.Errorf("ast.Source(tree) != src\n got:\n%s\nwant:\n%s", got, src)
			}
			for _, d := range bag.All() {
				if !diag.Registered(d.Code) {
					t.Errorf("diagnostic at %d:%d carries unregistered code %q",
						d.Span.Line, d.Span.Col, d.Code)
				}
			}

			goldenPath := filepath.Join(corpusDir, name+".golden")
			if todos := todoCodes(src); len(todos) > 0 {
				checkPending(t, name, goldenPath, todos, bag)
				return
			}

			if bag.Len() == 0 {
				t.Fatalf("%s is in the invalid corpus but produced no diagnostics; "+
					"either it is not invalid, or it is waiting on a stage and needs a "+
					"`# todo(ART-NN): <code>` line", filepath.Base(path))
			}
			checkGolden(t, goldenPath, render(diag.NewFiles(), filepath.Base(path), src, bag))
		})
	}
}

// frontEnd is every stage the corpus runs, and the one place a later issue
// extends. ART-33 adds:
//
//	bag.Merge(check.Check(tree))
//
// and the checker fixtures' todo lines come out. Nothing else in this file
// changes, and no golden moves except the ones ART-33 is responsible for.
func frontEnd(file, src string) (*ast.File, *diag.Bag) {
	return parser.Parse(file, src)
}

// TestAllErrorsReported is R5, asserted in Go rather than through a golden.
//
// "All errors in a file are reported" is the property the design names, and a
// golden cannot defend it: a parser that regressed to stopping at the first
// fault would produce a shorter golden, and the next person would regenerate
// it. Counting here means that regression has to be argued with, not absorbed.
func TestAllErrorsReported(t *testing.T) {
	const name = "many_errors.art"
	src := read(t, filepath.Join(corpusDir, name))
	_, bag := frontEnd(name, src)

	diags := bag.All()
	if len(diags) < 4 {
		t.Fatalf("%s has four unrelated faults in it and produced %d diagnostics; "+
			"the parser stopped early", name, len(diags))
	}

	// Spread, not just count: four diagnostics all at one position would be one
	// fault cascading, which is the opposite of what this fixture is for.
	lines := map[int]bool{}
	for _, d := range diags {
		lines[d.Span.Line] = true
	}
	if len(lines) < 4 {
		t.Errorf("the %d diagnostics cover only %d distinct lines; the four faults are on four",
			len(diags), len(lines))
	}

	// The last fault is near the end of the file, so reporting it at all proves
	// the parser ran to the end rather than recovering once and giving up.
	last := diags[len(diags)-1]
	total := strings.Count(src, "\n") + 1
	if last.Span.Line < total/2 {
		t.Errorf("the last diagnostic is at line %d of %d; nothing in the second half of the file was reported",
			last.Span.Line, total)
	}
}

// TestEveryNamedFaultHasAFixture pins the coverage list itself. The issue names
// eleven faults; a corpus that quietly lost one of them to a rename or a
// deletion would still pass every other test in this file.
func TestEveryNamedFaultHasAFixture(t *testing.T) {
	want := []string{
		"unknown_field",     // unknown field with a near-miss name
		"out_of_scope_root", // out-of-scope root
		"reserved_word",     // reserved word
		"unclosed_interp",   // unclosed ${
		"bad_regex",         // uncompilable regex
		"bad_duration",      // bad duration string
		"missing_action",    // missing action block
		"two_actions",       // two action blocks in one step
		"unknown_ident",     // unknown identifier
		"bad_object",        // malformed object literal
		"many_errors",       // several unrelated errors, all reported
	}
	for _, name := range want {
		for _, ext := range []string{".art", ".golden"} {
			if _, err := os.Stat(filepath.Join(corpusDir, name+ext)); err != nil {
				t.Errorf("the corpus must cover %s: %v", name, err)
			}
		}
	}
}

// TestEveryGoldenHasAFixture catches the other direction: a golden left behind
// by a renamed or deleted .art file, which would otherwise sit in the tree
// asserting nothing.
func TestEveryGoldenHasAFixture(t *testing.T) {
	goldens, err := filepath.Glob(filepath.Join(corpusDir, "*.golden"))
	if err != nil {
		t.Fatalf("globbing: %v", err)
	}
	for _, g := range goldens {
		art := strings.TrimSuffix(g, ".golden") + ".art"
		if _, err := os.Stat(art); err != nil {
			t.Errorf("%s has no fixture: %v", filepath.Base(g), err)
		}
	}
}

// render builds a fixture's golden: the terminal rendering a person receives,
// then the footer that pins what the terminal rendering cannot show.
//
// The two sections are different audiences for the same diagnostics. The
// terminal section is diag.TerminalString verbatim -- the corpus renders, it
// never formats, so the thing under test is the thing shipped. The footer is
// the machine contract: the code, the severity and the span's *end*, none of
// which appear above, so a code rename or a span that grew by one column fails
// here even when the caret run happens not to move.
func render(files *diag.Files, file, src string, bag *diag.Bag) string {
	files.Add(file, src)

	var b strings.Builder
	b.WriteString("=== terminal\n")
	b.WriteString(diag.TerminalString(files, bag.All()))
	b.WriteString("\n=== codes\n")
	for _, d := range bag.All() {
		fmt.Fprintf(&b, "%s\t%s\t%d:%d-%d:%d",
			d.Code, d.Severity, d.Span.Line, d.Span.Col, d.Span.EndLine, d.Span.EndCol)
		if len(d.Suggestions) > 0 {
			reps := make([]string, len(d.Suggestions))
			for i, s := range d.Suggestions {
				reps[i] = fmt.Sprintf("%q", s.Replace)
			}
			fmt.Fprintf(&b, "\tsuggest: %s", strings.Join(reps, ", "))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// todoLine matches a fixture's declaration that it is waiting for a stage:
//
//	# todo(ART-33): unknown-field
//
// The code is a plain string, deliberately not checked against diag's registry:
// the four codes these fixtures name -- reserved-word, invalid-regex,
// invalid-duration, unknown-identifier -- are ART-33's to register, and
// registering a code with no emitter would make diag's own codes.golden claim
// something false.
var todoLine = regexp.MustCompile(`^#\s*todo\((ART-\d+)\):\s*(\S+)\s*$`)

// todoCodes returns the codes a fixture is waiting for, in the order declared.
func todoCodes(src string) []string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if m := todoLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			out = append(out, m[2])
		}
	}
	return out
}

// checkPending is the gate on a fixture whose stage has not landed.
//
// It asserts the promise rather than skipping it. The file must parse clean --
// a fixture whose only fault is a name must not draw a syntax error, which is
// the parser-versus-checker division ART-31 settled -- and the hand-written
// golden must claim exactly the codes the todo lines name, so the authored text
// and the declaration cannot drift apart.
func checkPending(t *testing.T, name, goldenPath string, todos []string, bag *diag.Bag) {
	t.Helper()

	if bag.Len() > 0 {
		var got []string
		for _, d := range bag.All() {
			got = append(got, fmt.Sprintf("%s at %d:%d", d.Code, d.Span.Line, d.Span.Col))
		}
		t.Fatalf("%s.art is waiting on %s but the front end now reports %s.\n"+
			"If the stage has landed: delete the todo line(s) from %s.art, run "+
			"`go test ./pkg/dsl -update`, and read the diff against the text that was "+
			"written for it.\nIf it has not: a stage reported something on a file whose "+
			"only fault is a name, which is the fault this fixture exists to catch.",
			name, strings.Join(todos, ", "), strings.Join(got, "; "), name)
	}

	golden := read(t, goldenPath)
	claimed := footerCodes(t, golden)
	if !equalStrings(claimed, todos) {
		t.Errorf("%s.golden pins codes %v but %s.art declares `# todo` for %v; "+
			"the authored text and the declaration must name the same codes",
			name, claimed, name, todos)
	}
	if !strings.Contains(golden, "=== terminal\n") || strings.TrimSpace(golden) == "=== terminal" {
		t.Errorf("%s.golden has no terminal section; a pending fixture's whole point is "+
			"that the text a person will receive is written down now", name)
	}
}

// footerCodes reads the codes out of a golden's "=== codes" section, which is
// how a hand-written golden states what it is pinning.
func footerCodes(t *testing.T, golden string) []string {
	t.Helper()
	_, footer, ok := strings.Cut(golden, "=== codes\n")
	if !ok {
		t.Fatalf("golden has no `=== codes` section")
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(footer, "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, strings.Fields(line)[0])
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkGolden compares got with the golden, or rewrites it under -update.
func checkGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run `go test ./pkg/dsl -update` to create it)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s does not match.\nA diagnostic's wording, span, severity, hint, "+
			"suggestion or code changed. If that was deliberate, run "+
			"`go test ./pkg/dsl -update` and read the diff.\n got:\n%s\nwant:\n%s",
			filepath.Base(path), got, string(want))
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}
