package parser

import (
	"os"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
)

// Recovery is the property that makes diagnostics worth having: all errors in
// a file are reported, and everything that parses still lands in the tree. A
// parser that gave up at the first fault would report one error per edit
// cycle, which is the decoder behaviour the whole front end exists to replace.
//
// Every test here asserts both halves -- the diagnostic, and the good code
// either side of the bad line surviving.

func TestRecoveryKeepsEverythingThatParses(t *testing.T) {
	src := readFile(t, "testdata/recover.art")
	f, bag := Parse("recover.art", src)

	if !bag.HasErrors() {
		t.Fatal("no diagnostics for a file with four faults in it")
	}
	assertRoundTrip(t, src, f)

	// Both scenarios survive, despite a bad line inside the first and a bad
	// line between them.
	var scenarios []string
	ast.Inspect(f, func(n ast.Node) {
		if s, ok := n.(*ast.Scenario); ok {
			scenarios = append(scenarios, s.Name.Value)
		}
	})
	if len(scenarios) != 2 || scenarios[0] != "good one" || scenarios[1] != "also good" {
		t.Fatalf("scenarios = %q, want [good one, also good]", scenarios)
	}

	// Every step survives, including the two that contain a fault.
	var steps []string
	ast.Inspect(f, func(n ast.Node) {
		if s, ok := n.(*ast.StepDecl); ok {
			steps = append(steps, s.Name.Value)
		}
	})
	want := []string{"survives a bad statement", "survives a bad field", "still parsed"}
	if len(steps) != len(want) {
		t.Fatalf("steps = %q, want %q", steps, want)
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Errorf("step %d = %q, want %q", i, steps[i], want[i])
		}
	}

	// And each step still has its action and its good statements. A step whose
	// action was lost to recovery would be worse than a step that failed to
	// parse, because the checker would then report a missing action that the
	// author did write.
	for _, name := range want {
		step := stepNamed(t, f, name)
		if step.Action == nil {
			t.Errorf("step %q lost its action block", name)
		}
	}

	// `var kept = 1` is after the bad line and before the first step: the skip
	// stopped at the statement boundary rather than running to the next brace.
	var vars []string
	ast.Inspect(f, func(n ast.Node) {
		if v, ok := n.(*ast.VarDecl); ok {
			vars = append(vars, v.Name.Value)
		}
	})
	if len(vars) != 2 || vars[1] != "kept" {
		t.Fatalf("vars = %q, want [url, kept]", vars)
	}
}

// TestOneBadLineIsOneDiagnostic: a cascade is as unhelpful as silence. The
// count is asserted, not just the presence, because the way recovery fails is
// by reporting the same mistake four times from four positions.
func TestOneBadLineIsOneDiagnostic(t *testing.T) {
	src := `scenario "s" {
  var a = 1
  nonsense here
  var b = 2
}
`
	f, bag := Parse("t.art", src)
	assertRoundTrip(t, src, f)

	if got := codes(bag); len(got) != 1 || got[0] != diag.UnexpectedToken {
		for _, d := range bag.All() {
			t.Logf("%d:%d [%s] %s", d.Span.Line, d.Span.Col, d.Code, d.Message)
		}
		t.Fatalf("codes = %v, want one unexpected-token", got)
	}
	if d := bag.All()[0]; d.Span.Line != 3 {
		t.Errorf("diagnostic on line %d, want 3", d.Span.Line)
	}
}

// TestBadLineKeepsItsBytes: the tokens a failed statement consumed have to go
// somewhere, or a broken file would not round-trip and a UI could not open it.
func TestBadLineKeepsItsBytes(t *testing.T) {
	src := "scenario \"s\" {\n  nonsense here\n}\n"
	f, _ := Parse("t.art", src)
	assertRoundTrip(t, src, f)

	bad := find(f, is[*ast.Bad])
	if bad == nil {
		t.Fatal("no Bad node for a line that did not parse")
	}
	if got, want := ast.Source(bad), "\n  nonsense here"; got != want {
		t.Errorf("Bad covers %q, want %q", got, want)
	}
}

// TestUnclosedBraceDoesNotHang is the fault that costs a parser its
// termination guarantee, so it is tested at every nesting depth the grammar
// has.
func TestUnclosedBraceDoesNotHang(t *testing.T) {
	cases := []string{
		`scenario "s" {`,
		"scenario \"s\" {\n  step \"t\" {",
		"scenario \"s\" {\n  step \"t\" {\n    get \"/x\" {",
		"scenario \"s\" {\n  step \"t\" {\n    get \"/x\" {\n      body = {",
		"scenario \"s\" {\n  step \"t\" {\n    get \"/x\" {\n      body = [",
		"scenario \"s\" {\n  var a = f(",
		"scenario \"s\" {\n  var a = b[",
		"scenario \"s\" {\n  var a = (",
		"scenario \"s\" {\n  config browser {",
		"scenario \"s\" {\n  step \"t\" {\n    browser {",
		"scenario \"s\" {\n  step \"t\" {\n    run \"x\"\n    retry {",
	}

	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			f, bag := Parse("t.art", src)
			if !bag.HasErrors() {
				t.Error("an unclosed bracket produced no diagnostic")
			}
			assertRoundTrip(t, src, f)
		})
	}
}

// TestUnclosedPointsAtTheOpener: the opener is what the author has to go and
// look at, so the span is there rather than at end of file.
func TestUnclosedPointsAtTheOpener(t *testing.T) {
	src := "scenario \"s\" {\n  var a = 1\n  var b = 2\n"
	_, bag := Parse("t.art", src)

	var found *diag.Diagnostic
	for _, d := range bag.All() {
		if d.Code == diag.UnclosedBlock {
			found = &d
			break
		}
	}
	if found == nil {
		t.Fatal("no unclosed-block diagnostic")
	}
	if found.Span.Line != 1 || found.Span.Col != 14 {
		t.Errorf("unclosed-block at %d:%d, want 1:14 -- the \"{\"", found.Span.Line, found.Span.Col)
	}
}

// TestEveryParseCodeIsReachable pins one source per code this issue added. The
// exhaustive corpus with rendered golden text is ART-32; this is the in-package
// proof that nothing was registered and then never emitted.
func TestEveryParseCodeIsReachable(t *testing.T) {
	cases := []struct {
		code diag.Code
		src  string
	}{
		{diag.UnexpectedToken, "nonsense\n"},
		{diag.MissingSeparator, "scenario \"s\" {\n  var a = 1 var b = 2\n}\n"},
		{diag.UnclosedBlock, "scenario \"s\" {\n"},
		{diag.MissingAction, "scenario \"s\" {\n  step \"t\" {\n    expect a == 1\n  }\n}\n"},
		{diag.DuplicateAction, "scenario \"s\" {\n  step \"t\" {\n    run \"a\"\n    run \"b\"\n  }\n}\n"},
		{diag.ActionNotFirst, "scenario \"s\" {\n  step \"t\" {\n    expect a == 1\n    run \"a\"\n  }\n}\n"},
		{diag.NonAssociativeOperator, "scenario \"s\" {\n  var a = b == c == d\n}\n"},
		{diag.UnclosedInterpolationExpr, "scenario \"s\" {\n  var a = \"${b\n"},
	}

	for _, c := range cases {
		t.Run(string(c.code), func(t *testing.T) {
			f, bag := Parse("t.art", c.src)
			assertRoundTrip(t, c.src, f)

			if !has(codes(bag), c.code) {
				for _, d := range bag.All() {
					t.Logf("%d:%d [%s] %s", d.Span.Line, d.Span.Col, d.Code, d.Message)
				}
				t.Fatalf("no %s diagnostic for %q", c.code, c.src)
			}
			// Every code in the bag has to be in diag's registry, or a client
			// keying behaviour off codes meets one it has never heard of.
			for _, got := range codes(bag) {
				if !diag.Registered(got) {
					t.Errorf("code %q is not in diag's registry", got)
				}
			}
		})
	}
}

// TestMissingActionNamesTheStep: the diagnostic points at the step's name,
// because that is the line an author goes to, and names the three action forms
// rather than saying "action expected".
func TestMissingActionNamesTheStep(t *testing.T) {
	src := "scenario \"s\" {\n  step \"no action\" {\n    expect a == 1\n  }\n}\n"
	_, bag := Parse("t.art", src)

	for _, d := range bag.All() {
		if d.Code != diag.MissingAction {
			continue
		}
		if d.Span.Line != 2 || d.Span.Col != 8 {
			t.Errorf("span at %d:%d, want 2:8 -- the step's name", d.Span.Line, d.Span.Col)
		}
		if d.Hint == "" {
			t.Error("no hint naming the three action forms")
		}
		return
	}
	t.Fatal("no missing-action diagnostic")
}

// TestDuplicateActionKeepsBothInTheTree: the second one still has to live
// somewhere, or the file would not round-trip.
func TestDuplicateActionKeepsBothInTheTree(t *testing.T) {
	src := "scenario \"s\" {\n  step \"t\" {\n    run \"a\"\n    run \"b\"\n  }\n}\n"
	f, bag := Parse("t.art", src)
	assertRoundTrip(t, src, f)

	if !has(codes(bag), diag.DuplicateAction) {
		t.Fatal("no duplicate-action diagnostic")
	}
	step := firstStep(t, f)
	if step.Action == nil {
		t.Error("the first action was not kept as the step's action")
	}
	if find(step, is[*ast.Bad]) == nil {
		t.Error("the second action is not in the tree")
	}
}

// TestActionNotFirstStillParsesTheWholeStep is the decision this plan settled:
// one precise diagnostic instead of a cascade, with the step intact.
func TestActionNotFirstStillParsesTheWholeStep(t *testing.T) {
	src := "scenario \"s\" {\n  step \"t\" {\n    expect a == 1\n    run \"x\"\n    expect b == 2\n  }\n}\n"
	f, bag := Parse("t.art", src)
	assertRoundTrip(t, src, f)

	if got := codes(bag); len(got) != 1 || got[0] != diag.ActionNotFirst {
		t.Fatalf("codes = %v, want one action-not-first", got)
	}
	step := firstStep(t, f)
	if step.Action == nil {
		t.Fatal("the action was not recognised")
	}
	if len(step.Body) != 2 {
		t.Errorf("statements = %d, want both expects", len(step.Body))
	}
	// Source order, not struct order: the action is written between the two
	// expects and the walk has to see it there.
	kids := ast.Children(step)
	if len(kids) != 3 {
		t.Fatalf("children = %d, want 3", len(kids))
	}
	if _, ok := kids[1].(*ast.Run); !ok {
		t.Errorf("child 1 is %T, want the *ast.Run written second", kids[1])
	}
}

// TestChainedComparisonIsReportedNotFolded: `a == b == c` has two plausible
// readings and neither is what the author meant, so it is reported -- once --
// and its tokens are kept.
func TestChainedComparisonIsReportedNotFolded(t *testing.T) {
	for _, src := range []string{"a == b == c", "a < b < c", "a exists exists", "a is number is number"} {
		t.Run(src, func(t *testing.T) {
			x, bag := ParseExpr("t.art", src)
			assertRoundTrip(t, src, x)
			if n := count(codes(bag), diag.NonAssociativeOperator); n != 1 {
				t.Errorf("non-associative-operator reported %d times, want 1 (codes %v)", n, codes(bag))
			}
		})
	}
}

// TestMissingSeparatorDoesNotLoseTheNextStatement: the author forgot a
// newline, and the statement after it is perfectly good -- skipping it would
// turn one small mistake into a missing step.
func TestMissingSeparatorDoesNotLoseTheNextStatement(t *testing.T) {
	src := "scenario \"s\" {\n  var a = 1 var b = 2\n}\n"
	f, bag := Parse("t.art", src)
	assertRoundTrip(t, src, f)

	if !has(codes(bag), diag.MissingSeparator) {
		t.Fatalf("codes = %v, want missing-separator", codes(bag))
	}
	var vars []string
	ast.Inspect(f, func(n ast.Node) {
		if v, ok := n.(*ast.VarDecl); ok {
			vars = append(vars, v.Name.Value)
		}
	})
	if len(vars) != 2 {
		t.Errorf("vars = %q, want both a and b", vars)
	}
}

// TestLexicalErrorsAreInTheSameBag: a file with a lexical fault and a
// syntactic one reports both, in file order, because an author fixing a file
// wants the whole list.
func TestLexicalErrorsAreInTheSameBag(t *testing.T) {
	src := "scenario \"s\" {\n  var a = \"unterminated\n  nonsense here\n}\n"
	f, bag := Parse("t.art", src)
	assertRoundTrip(t, src, f)

	got := codes(bag)
	if !has(got, diag.UnterminatedString) {
		t.Errorf("codes = %v, want the lexer's unterminated-string", got)
	}
	// In file order: diag.Bag sorts by offset, so the lexical fault on line 2
	// comes before anything on line 3.
	all := bag.All()
	for i := 1; i < len(all); i++ {
		if all[i].Span.Offset < all[i-1].Span.Offset {
			t.Fatalf("diagnostics are out of file order at %d", i)
		}
	}
}

// TestUnquotedNameSuggestsTheQuotes: `scenario checkout {` is the mistake
// someone coming from HCL makes, and a suggestion that adds the quotes is a
// one-click fix rather than a paragraph.
func TestUnquotedNameSuggestsTheQuotes(t *testing.T) {
	src := "scenario checkout {\n  step login {\n    run \"x\"\n  }\n}\n"
	f, bag := Parse("t.art", src)
	assertRoundTrip(t, src, f)

	all := bag.All()
	if len(all) != 2 {
		t.Fatalf("diagnostics = %d, want one per unquoted name", len(all))
	}
	for _, d := range all {
		if len(d.Suggestions) != 1 {
			t.Errorf("%d:%d has %d suggestions, want 1", d.Span.Line, d.Span.Col, len(d.Suggestions))
			continue
		}
		if want := []string{`"checkout"`, `"login"`}; !has(want, d.Suggestions[0].Replace) {
			t.Errorf("suggestion = %q, want one of %q", d.Suggestions[0].Replace, want)
		}
	}
	// And the names are still bound, so the rest of the file checks.
	if s := find(f, is[*ast.Scenario]); s == nil {
		t.Error("the scenario was abandoned rather than recovered")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func stepNamed(t *testing.T, f *ast.File, name string) *ast.StepDecl {
	t.Helper()
	var found *ast.StepDecl
	ast.Inspect(f, func(n ast.Node) {
		if s, ok := n.(*ast.StepDecl); ok && s.Name.Value == name {
			found = s
		}
	})
	if found == nil {
		t.Fatalf("no step named %q", name)
	}
	return found
}

func has[T comparable](in []T, want T) bool { return count(in, want) > 0 }

func count[T comparable](in []T, want T) int {
	n := 0
	for _, v := range in {
		if v == want {
			n++
		}
	}
	return n
}
