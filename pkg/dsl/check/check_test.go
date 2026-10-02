package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// TestFixturesCheckClean is the single most load-bearing test in the package.
//
// exprs.art was written in ART-31 "so that the round trip and the checker's
// future corpus both have something exhaustive to run against": every
// expression form in the grammar, every comparison, every postfix predicate,
// all three step types, every browser action, every element function. If the
// checker reports anything about it, the checker is wrong about the language.
//
// checkout.art is the design document's worked example, which makes this also
// the assertion that the design's own scenario compiles.
func TestFixturesCheckClean(t *testing.T) {
	for _, name := range []string{"exprs.art", "checkout.art"} {
		t.Run(name, func(t *testing.T) {
			src := readFixture(t, name)
			tree, bag := parser.Parse(name, src)
			if bag.HasErrors() {
				t.Fatalf("%s does not parse clean, so this test proves nothing:\n%s",
					name, render(name, src, bag))
			}

			_, checked := Check(tree)
			if checked.Len() > 0 {
				t.Errorf("%s is a valid file and the checker reported %d diagnostic(s):\n%s",
					name, checked.Len(), render(name, src, checked))
			}
		})
	}
}

// TestStepTypeInference is the ordering the issue calls load-bearing, asserted
// directly: a step's type comes from its action block's Go type and from
// nothing else, before any name in it is resolved.
func TestStepTypeInference(t *testing.T) {
	const src = `scenario "s" {
  step "api" { get "/x" }
  step "terminal" { run "echo" }
  step "browser" { browser { goto "/" } }
  step "none" { expect status == 200 }
}
`
	tree, _ := parser.Parse("t.art", src)
	info, _ := Check(tree)

	want := map[string]StepType{
		`"api"`:      API,
		`"terminal"`: Terminal,
		`"browser"`:  Browser,
		`"none"`:     Unknown,
	}
	seen := 0
	ast.Inspect(tree, func(n ast.Node) {
		s, ok := n.(*ast.StepDecl)
		if !ok {
			return
		}
		seen++
		if got := info.StepType(s); got != want[s.Name.Text] {
			t.Errorf("step %s: got %s, want %s", s.Name.Text, got, want[s.Name.Text])
		}
	})
	if seen != len(want) {
		t.Fatalf("found %d steps, want %d", seen, len(want))
	}
	if info.Steps() != len(want) {
		t.Errorf("Info.Steps() = %d, want %d", info.Steps(), len(want))
	}
}

// TestUntypedStepIsNotScopeChecked is the other half of the inference
// decision. A step with no action has no type, so there is nothing to resolve
// names against -- and the parser has already said what is wrong with it. One
// diagnostic for one mistake.
func TestUntypedStepIsNotScopeChecked(t *testing.T) {
	const src = `scenario "s" {
  step "none" {
    expect status == 200
    expect page.url contains "/x"
    capture k = nowhere
  }
}
`
	tree, _ := parser.Parse("t.art", src)
	_, bag := Check(tree)
	if bag.Len() != 0 {
		t.Errorf("a step with no action drew %d checker diagnostic(s); the parser's "+
			"missing-action is the whole story:\n%s", bag.Len(), render("t.art", src, bag))
	}
}

// TestNilAndBadTreesAreSafe is R1 at its edges. Check is handed trees that
// never came from a well-formed file, because that is the normal case: the
// parser recovers rather than giving up.
func TestNilAndBadTreesAreSafe(t *testing.T) {
	if info, bag := Check(nil); info == nil || bag == nil {
		t.Fatal("Check(nil) must return a non-nil Info and Bag")
	}

	// A nil Info answers conservatively rather than panicking, so a client
	// holding a tree it never checked is not a crash.
	var nilInfo *Info
	if got := nilInfo.StepType(nil); got != Unknown {
		t.Errorf("(*Info)(nil).StepType = %s, want unknown", got)
	}
	if got := nilInfo.Class(nil); got != Complex {
		t.Errorf("(*Info)(nil).Class = %s, want complex", got)
	}
	if got := nilInfo.Scope(nil); got != nil {
		t.Errorf("(*Info)(nil).Scope = %v, want nil", got)
	}
	if got := nilInfo.Steps(); got != 0 {
		t.Errorf("(*Info)(nil).Steps = %d, want 0", got)
	}

	if got := TypeOf(nil); got != Unknown {
		t.Errorf("TypeOf(nil) = %s, want unknown", got)
	}
	// A typed nil in an interface is what a failed parse can leave behind.
	if got := TypeOf((*ast.Request)(nil)); got != Unknown {
		t.Errorf("TypeOf((*ast.Request)(nil)) = %s, want unknown", got)
	}
	if got := TypeOf(&ast.Bad{}); got != Unknown {
		t.Errorf("TypeOf(&ast.Bad{}) = %s, want unknown", got)
	}
}

// TestEveryReservedWordHasAPurpose stops a word being added to the language's
// reserved list and arriving here as an empty sentence. The hint is the whole
// reason reserved words are the checker's business and not the lexer's.
func TestEveryReservedWordHasAPurpose(t *testing.T) {
	for _, w := range token.Reserved {
		if purposes[w] == "" {
			t.Errorf("%q is reserved and has no purpose in purposes; the diagnostic "+
				"would read \"; rename this var\"", w)
		}
	}
	for w := range purposes {
		if !token.IsReserved(w) {
			t.Errorf("purposes names %q, which is not in token.Reserved", w)
		}
	}
}

// TestEveryBuiltinHasAnArityAndASignature holds the three tables that describe
// a call -- token.Builtins, arity and signatures -- to each other, so a
// builtin added to the language cannot reach a user as `() takes 0 arguments`
// with an empty hint.
func TestEveryBuiltinHasAnArityAndASignature(t *testing.T) {
	for _, b := range token.Builtins {
		if _, ok := arity[b]; !ok {
			t.Errorf("builtin %q has no arity", b)
		}
		if signatures[b] == "" {
			t.Errorf("builtin %q has no signature for its hint", b)
		}
	}
	for _, f := range elementFns {
		if !token.IsBuiltin(f) {
			t.Errorf("element function %q is not in token.Builtins", f)
		}
	}
	for _, a := range token.BrowserActions {
		if actSignatures[a] == "" {
			t.Errorf("browser action %q has no signature for its arity hint", a)
		}
	}
}

// TestEveryStepTypeHasRootsAndAHint is the table at the centre of the issue.
// A step type with no hint would produce a not-in-scope diagnostic whose
// second line -- the list of roots that *are* in scope, which is the whole
// point of the code -- was blank.
func TestEveryStepTypeHasRootsAndAHint(t *testing.T) {
	for _, ty := range []StepType{API, Terminal, Browser} {
		if len(Roots(ty)) == 0 {
			t.Errorf("%s binds no roots", ty)
		}
		if scopeHints[ty] == "" {
			t.Errorf("%s has no scope hint", ty)
		}
		if !strings.Contains(scopeHints[ty], ty.String()) {
			t.Errorf("%s's hint does not name the step type: %q", ty, scopeHints[ty])
		}
		for _, r := range Roots(ty) {
			if got, ok := allRoots[r]; !ok || got != ty {
				t.Errorf("root %q of %s is not indexed back to it", r, ty)
			}
		}
	}
	if scopeHints[Unknown] == "" {
		t.Error("a var declaration has no scope hint, so an unresolvable name in one " +
			"would get a blank second line")
	}
	// Roots and Functions hand out copies; a client that sorts the result must
	// not reorder the checker's candidate order.
	rs := Roots(API)
	rs[0] = "clobbered"
	if Roots(API)[0] == "clobbered" {
		t.Error("Roots returns the package's own slice")
	}
}

// TestScopeIsACopy guards the other accessor that hands out a slice.
func TestScopeIsACopy(t *testing.T) {
	const src = `scenario "s" {
  var base = env("U")
  step "one" { get "${base}/x" }
}
`
	tree, _ := parser.Parse("t.art", src)
	info, _ := Check(tree)

	var step *ast.StepDecl
	ast.Inspect(tree, func(n ast.Node) {
		if s, ok := n.(*ast.StepDecl); ok {
			step = s
		}
	})
	got := info.Scope(step)
	want := []string{"status", "body", "raw", "headers", "base"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("scope = %v, want %v (roots first, then vars: the candidate order)", got, want)
	}
	got[0] = "clobbered"
	if info.Scope(step)[0] == "clobbered" {
		t.Error("Info.Scope returns the checker's own slice")
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "parser", "testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// render is the terminal rendering of a bag, for a failure message that shows
// what the checker actually said rather than how many things it said.
func render(name, src string, bag *diag.Bag) string {
	files := diag.NewFiles()
	files.Add(name, src)
	return diag.TerminalString(files, bag.All())
}
