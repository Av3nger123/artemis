package check

import (
	"os"
	"path/filepath"
	"reflect"
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

// TestModuleWordsSayWhatTheyDoNow fails when import or use still describes
// itself as a future feature after it has shipped.
func TestModuleWordsSayWhatTheyDoNow(t *testing.T) {
	for _, w := range []string{"import", "use"} {
		if strings.Contains(purposes[w], "future") {
			t.Errorf("%q is real now; its purpose still says %q", w, purposes[w])
		}
	}
}

// TestEveryBuiltinHasAnArityAndASignature holds the three tables that describe
// a call -- token.Builtins, params and signatures -- to each other, so a
// builtin added to the language cannot reach a user as `() takes 0 arguments`
// with an empty hint.
func TestEveryBuiltinHasAnArityAndASignature(t *testing.T) {
	for _, b := range token.Builtins {
		if _, ok := params[b]; !ok {
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

// TestMatchDiagnostics pins what the checker says about the one builtin whose
// arguments are not all strings.
//
// The four faults are the four things a `match()` call can get wrong, and
// each is a compile error rather than a run-time surprise -- which is the
// whole reason the regex extraction is a builtin with a signature rather than
// a free-form string the runtime interprets.
func TestMatchDiagnostics(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		code    diag.Code
		message string
		hint    string
	}{
		{
			name:    "one argument",
			src:     step("api", `get "/x"`, `capture id = match(raw)`),
			code:    diag.BadArity,
			message: `match() takes two arguments; this call has one`,
			hint:    `match(raw, /id=([0-9]+)/)`,
		},
		{
			name:    "a number where the text goes",
			src:     step("api", `get "/x"`, `capture id = match(1, /id=([0-9]+)/)`),
			code:    diag.BadValue,
			message: `match()'s first argument must be a string, not a number`,
			hint:    `match(raw, /id=([0-9]+)/)`,
		},
		{
			name:    "a number where the pattern goes",
			src:     step("api", `get "/x"`, `capture id = match(raw, 1)`),
			code:    diag.BadValue,
			message: `match()'s second argument must be a regular expression, not a number`,
			hint:    `match(raw, /id=([0-9]+)/)`,
		},
		{
			name:    "two capturing groups",
			src:     step("api", `get "/x"`, `capture id = match(raw, /(items|orders)\/([0-9]+)/)`),
			code:    diag.BadValue,
			message: `match() reads one capturing group, and this pattern has two`,
			hint:    `make the groups you do not want non-capturing: (?:...)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, parsed := parser.Parse("t.art", tc.src)
			if parsed.HasErrors() {
				t.Fatalf("source does not parse, so this test proves nothing:\n%s",
					render("t.art", tc.src, parsed))
			}
			_, bag := Check(tree)
			all := bag.All()
			if len(all) != 1 {
				t.Fatalf("got %d diagnostics, want exactly 1 -- one mistake is one "+
					"diagnostic:\n%s", len(all), render("t.art", tc.src, bag))
			}
			d := all[0]
			if d.Code != tc.code {
				t.Errorf("code = %s, want %s", d.Code, tc.code)
			}
			if d.Message != tc.message {
				t.Errorf("message = %q, want %q", d.Message, tc.message)
			}
			if d.Hint != tc.hint {
				t.Errorf("hint = %q, want %q", d.Hint, tc.hint)
			}
		})
	}
}

// A pattern that does not compile is one diagnostic, not two: counting the
// groups of a regex Go could not read would be a second complaint about one
// mistake.
func TestAnUncompilablePatternIsNotAlsoCounted(t *testing.T) {
	src := step("api", `get "/x"`, `capture id = match(raw, /(a(b/)`)
	tree, _ := parser.Parse("t.art", src)
	_, bag := Check(tree)
	all := bag.All()
	if len(all) != 1 || all[0].Code != diag.InvalidRegex {
		t.Fatalf("got %d diagnostics, want one invalid-regex:\n%s",
			len(all), render("t.art", src, bag))
	}
}

// TestEnvTakesOneOrTwoArguments is ART-25's whole point for this task: env()
// with a default value is as legal as env() without one. Task 2 is where an
// absent name without a default becomes an error; this only widens the call
// shape the checker accepts.
func TestEnvTakesOneOrTwoArguments(t *testing.T) {
	for _, src := range []string{
		"scenario \"s\" {\n  var u = env(\"API_URL\")\n}\n",
		"scenario \"s\" {\n  var u = env(\"API_URL\", \"http://localhost\")\n}\n",
	} {
		tree, parsed := parser.Parse("t.art", src)
		if parsed.HasErrors() {
			t.Fatalf("source does not parse, so this test proves nothing:\n%s",
				render("t.art", src, parsed))
		}
		_, bag := Check(tree)
		if bag.HasErrors() {
			t.Fatalf("env() rejected a legal call: %v", bag.All())
		}
	}
}

// TestEnvRejectsThreeArguments pins the message a range produces, as against
// the fixed count every other builtin still reports.
func TestEnvRejectsThreeArguments(t *testing.T) {
	src := "scenario \"s\" {\n  var u = env(\"A\", \"b\", \"c\")\n}\n"
	tree, parsed := parser.Parse("t.art", src)
	if parsed.HasErrors() {
		t.Fatalf("source does not parse, so this test proves nothing:\n%s",
			render("t.art", src, parsed))
	}
	_, bag := Check(tree)
	if !bag.HasErrors() {
		t.Fatal("env() with three arguments was accepted")
	}
	got := bag.All()[0].Message
	want := `env() takes one argument or two; this call has three`
	if got != want {
		t.Fatalf("message\n got: %s\nwant: %s", got, want)
	}
}

// TestEnvSecondArgumentMustBeAString is the default value held to the same
// rule as every other string parameter: a literal of the wrong kind is caught
// here rather than surprising a run.
func TestEnvSecondArgumentMustBeAString(t *testing.T) {
	src := "scenario \"s\" {\n  var u = env(\"A\", 8080)\n}\n"
	tree, parsed := parser.Parse("t.art", src)
	if parsed.HasErrors() {
		t.Fatalf("source does not parse, so this test proves nothing:\n%s",
			render("t.art", src, parsed))
	}
	_, bag := Check(tree)
	if !bag.HasErrors() {
		t.Fatal("a number as the default value was accepted")
	}
}

// An empty name is rejected here rather than at the step that uses it: no
// environment can supply "", and the run-time sentence would be "the
// environment variable  has no value" -- a message with a hole in it where the
// name should be.
func TestEnvRejectsAnEmptyName(t *testing.T) {
	for _, src := range []string{
		"scenario \"s\" {\n  var u = env(\"\")\n}\n",
		// A default does not excuse it: env("", "8080") always answers
		// "8080", so the call says one thing and does another.
		"scenario \"s\" {\n  var u = env(\"\", \"8080\")\n}\n",
	} {
		tree, parsed := parser.Parse("t.art", src)
		if parsed.HasErrors() {
			t.Fatalf("source does not parse, so this test proves nothing:\n%s",
				render("t.art", src, parsed))
		}
		info, bag := Check(tree)
		all := bag.All()
		if len(all) != 1 {
			t.Fatalf("got %d diagnostics, want exactly 1:\n%s", len(all), render("t.art", src, bag))
		}
		if all[0].Code != diag.BadValue {
			t.Errorf("code = %s, want %s", all[0].Code, diag.BadValue)
		}
		want := "env()'s argument must name an environment variable, not the empty string"
		if all[0].Message != want {
			t.Errorf("message\n got: %s\nwant: %s", all[0].Message, want)
		}
		if needs := info.EnvNeeds(); len(needs) != 0 {
			t.Errorf("EnvNeeds() = %v, want none: a nameless row in the gate's block names nothing", needs)
		}
	}
}

// Every builtin the language names is one the checker knows, each of its
// parameters says what it takes, and its two bounds are coherent.
//
// The bounds are the part that had no test. The assertion used to be `high !=
// len(ps)` against a high that Arity derives from len(ps) -- len(ps) !=
// len(ps), which cannot fail -- and nothing read minArgs at all: minArgs["env"]
// = 3 would have made every env() call an arity error with every test still
// green. A minimum above the maximum, or below one, is now the failure.
func TestParamsAndBuiltinsAgree(t *testing.T) {
	for name, ps := range params {
		low, high, ok := Arity(name)
		if !ok {
			t.Errorf("%s() is in params and Arity does not know it", name)
			continue
		}
		if high != len(ps) {
			t.Errorf("%s(): max arity %d, %d parameters", name, high, len(ps))
		}
		if low < 1 || low > high {
			t.Errorf("%s(): accepts %d to %d arguments, which is not a range a call can satisfy",
				name, low, high)
		}
		if !token.IsBuiltin(name) {
			t.Errorf("%s() is in params and not in token.Builtins, so token.IsBuiltin "+
				"disagrees with the checker about what is callable", name)
		}
		for i, p := range ps {
			if p.want == "" || len(p.kinds) == 0 {
				t.Errorf("%s()'s parameter %d says nothing about what it takes", name, i)
			}
		}
	}
	for name, low := range minArgs {
		ps, ok := params[name]
		if !ok {
			t.Errorf("minArgs has %q, which has no parameter list -- so Arity never reads it", name)
			continue
		}
		// A minimum equal to the maximum is the default and says nothing; a
		// builtin that no longer takes a range should leave minArgs instead.
		if low >= len(ps) {
			t.Errorf("minArgs[%q] = %d against %d parameters: a minimum that is not below the "+
				"maximum is either a contradiction or a line with no effect", name, low, len(ps))
		}
	}
}

// match() is callable in every step type, like env(): it searches a string and
// needs neither a page nor a response to be meaningful.
func TestMatchIsCallableEverywhere(t *testing.T) {
	for _, ty := range []StepType{API, Terminal, Browser, Unknown} {
		fns := Functions(ty)
		if !contains(fns, "match") {
			t.Errorf("Functions(%s) = %v, and does not offer match()", ty, fns)
		}
		if !contains(fns, "env") {
			t.Errorf("Functions(%s) = %v, and does not offer env()", ty, fns)
		}
	}
}

// TestInfoCollectsEnvNames states the three exclusions together: a default
// value answers the call so it is not a need, a name that is not a plain
// string literal is a value this stage cannot read, and the two live names
// come back in the order they were written.
func TestInfoCollectsEnvNames(t *testing.T) {
	src := `scenario "s" {
  var url = env("API_URL")
  var pw  = env("API_PASSWORD")
  var prt = env("PORT", "8080")
  var dyn = env(url)
  step "x" {
    get "${url}" {
      header "A" = pw
    }
    expect status == 200
  }
}`
	info, bag := checkSource(t, src)
	if bag.HasErrors() {
		t.Fatalf("the file did not compile: %v", bag.All())
	}
	var got []string
	for _, need := range info.EnvNeeds() {
		got = append(got, need.Name)
	}
	want := []string{"API_URL", "API_PASSWORD"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestInfoCollectsOneNameOnce is the same name read twice, which is one need:
// a run only needs to be told once that API_URL is absent.
func TestInfoCollectsOneNameOnce(t *testing.T) {
	src := `scenario "s" {
  var a = env("API_URL")
  var b = env("API_URL")
  step "x" {
    get "${a}${b}"
    expect status == 200
  }
}`
	info, _ := checkSource(t, src)
	if len(info.EnvNeeds()) != 1 {
		t.Fatalf("got %d needs, want 1", len(info.EnvNeeds()))
	}
}

// checkSource parses and checks src, the shared setup for a test that wants
// an Info rather than a diagnostic about a specific call. A parse failure
// fails loudly here rather than letting Check run on a nil tree and the real
// assertion below report a confusing zero.
func checkSource(t *testing.T, src string) (*Info, *diag.Bag) {
	t.Helper()
	tree, parsed := parser.Parse("t.art", src)
	if parsed.HasErrors() {
		t.Fatalf("source does not parse, so this test proves nothing:\n%s",
			render("t.art", src, parsed))
	}
	return Check(tree)
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
