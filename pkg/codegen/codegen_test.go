package codegen

import (
	"errors"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// parse is the front end over one source, failing the test if it does not
// compile. Every test here starts from a tree the checker accepted, because that
// is the only tree `artemis build` ever hands a target.
func parse(t *testing.T, name, src string) *ast.File {
	t.Helper()
	tree, bag := parser.Parse(name, src)
	_, checked := check.Check(tree)
	bag.Merge(checked)
	if bag.HasErrors() {
		for _, d := range bag.All() {
			t.Logf("%s:%d: %s: %s", name, d.Span.Line, d.Code, d.Message)
		}
		t.Fatalf("%s does not compile", name)
	}
	return tree
}

func TestLookupResolvesAnImplementedTarget(t *testing.T) {
	for _, name := range Names() {
		target, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q) = %v, want a target", name, err)
		}
		if target.Name() != name {
			t.Errorf("Lookup(%q).Name() = %q, want %q", name, target.Name(), name)
		}
	}
}

// A --lang value is a command-line argument, so the case and the stray space a
// shell leaves behind are both the language the user meant.
func TestLookupIgnoresCaseAndSpace(t *testing.T) {
	for _, name := range []string{"Python", "PYTHON", " python ", "python\t", "JS", " js "} {
		if _, err := Lookup(name); err != nil {
			t.Errorf("Lookup(%q) = %v, want a target", name, err)
		}
	}
}

// implemented is every target the registry resolves, for the tests below that are
// about a target and not about a language. A target added to the registry is
// covered by all of them without a second edit, which is the only way a claim
// like "every generated file says it is one way" stays true.
func implemented(t *testing.T) []Target {
	t.Helper()
	out := make([]Target, 0, len(Names()))
	for _, name := range Names() {
		target, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q) = %v", name, err)
		}
		out = append(out, target)
	}
	return out
}

// The whole point of Reserved: `go` is a decision, not a typo, so it must not be
// answered with a list of the languages that do exist.
func TestLookupRefusesAReservedTargetByName(t *testing.T) {
	for _, name := range Reserved() {
		target, err := Lookup(name)
		if target != nil {
			t.Fatalf("Lookup(%q) returned a target; %q is reserved", name, name)
		}
		if !errors.Is(err, ErrReserved) {
			t.Fatalf("Lookup(%q) = %v, want ErrReserved", name, err)
		}
		why, ok := Why(name)
		if !ok || why == "" {
			t.Fatalf("Why(%q) = %q, %v; a reserved name needs a reason", name, why, ok)
		}
		if !strings.Contains(err.Error(), why) {
			t.Errorf("Lookup(%q) = %q, want it to carry the reason %q", name, err, why)
		}
	}
}

// `go` is reserved by the design's non-goals, and this is the issue's
// done-when: reserved, and not implemented.
func TestGoIsReservedAndNotImplemented(t *testing.T) {
	if _, err := Lookup("go"); !errors.Is(err, ErrReserved) {
		t.Fatalf(`Lookup("go") = %v, want ErrReserved`, err)
	}
	for _, name := range Names() {
		if name == "go" {
			t.Fatal("the go target is implemented; the non-goals say it is reserved and not built")
		}
	}
}

func TestLookupOnAnUnknownTargetListsTheOnesThatExist(t *testing.T) {
	_, err := Lookup("ruby")
	if !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf(`Lookup("ruby") = %v, want ErrUnknownTarget`, err)
	}
	for _, name := range Names() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("Lookup(\"ruby\") = %q, want it to name %q", err, name)
		}
	}
}

// A name cannot be both, or the registry would have two answers for it.
func TestNoNameIsBothImplementedAndReserved(t *testing.T) {
	for _, name := range Names() {
		if _, ok := Why(name); ok {
			t.Errorf("%q is implemented and reserved", name)
		}
	}
}

func TestSourceNameComesOffTheTree(t *testing.T) {
	tree := parse(t, "orders.art", "scenario \"x\" {\n}\n")
	if got := SourceName(tree); got != "orders.art" {
		t.Errorf("SourceName = %q, want orders.art", got)
	}

	// An empty file has only an EOF token, and it still carries the name.
	if got := SourceName(parse(t, "empty.art", "")); got != "empty.art" {
		t.Errorf("SourceName of an empty file = %q, want empty.art", got)
	}

	// A tree assembled in Go has no spans, and the target names the output
	// itself rather than writing a file called "".
	if got := SourceName(&ast.File{}); got != "" {
		t.Errorf("SourceName of a span-less tree = %q, want empty", got)
	}
	if got := SourceName(nil); got != "" {
		t.Errorf("SourceName(nil) = %q, want empty", got)
	}
}

func TestGenerateOnASpanlessTreeNamesTheFileItself(t *testing.T) {
	// The fallback name every target derives from the .art extension when the
	// tree has no spans to name it, in each target's own file-naming convention.
	want := map[string]string{"python": "test_artemis.py", "js": "artemis.test.js"}
	for _, target := range implemented(t) {
		files, err := target.Generate(&ast.File{})
		if err != nil {
			t.Fatalf("%s: Generate = %v", target.Name(), err)
		}
		if len(files) != 1 || files[0].Path != want[target.Name()] {
			t.Fatalf("%s: Generate = %+v, want one %s", target.Name(), files, want[target.Name()])
		}
	}
}

func TestGenerateOnNilIsAnEmptyModule(t *testing.T) {
	for _, target := range implemented(t) {
		files, err := target.Generate(nil)
		if err != nil {
			t.Fatalf("%s: Generate(nil) = %v", target.Name(), err)
		}
		if len(files) != 1 {
			t.Fatalf("%s: Generate(nil) returned %d files, want 1", target.Name(), len(files))
		}
		// No test function, under either runner's spelling of one.
		for _, forbidden := range []string{"def test_", "test(\""} {
			if strings.Contains(files[0].Content, forbidden) {
				t.Errorf("%s: Generate(nil) produced a test", target.Name())
			}
		}
	}
}

// Every generated file says the export is one way. That is the issue's
// requirement and the one line a reader has to be able to rely on.
func TestEveryGeneratedFileSaysItIsOneWay(t *testing.T) {
	tree := parse(t, "x.art", `scenario "x" {
  step "s" {
    get "https://example.test/"
    expect status == 200
  }
}
`)
	for _, target := range implemented(t) {
		files, err := target.Generate(tree)
		if err != nil {
			t.Fatalf("%s: Generate = %v", target.Name(), err)
		}
		for _, f := range files {
			if !strings.Contains(f.Content, "ONE-WAY EXPORT") {
				t.Errorf("%s does not say it is a one-way export", f.Path)
			}
			if !strings.Contains(f.Content, "artemis does not read it back") &&
				!strings.Contains(f.Content, "Artemis does not read it back") {
				t.Errorf("%s does not say artemis never reads it back", f.Path)
			}
		}
	}
}

// Nothing in the header may change between two runs over the same file, or a
// build step in CI would churn the diff every time it ran.
func TestGenerateIsDeterministic(t *testing.T) {
	src := `scenario "x" {
  var base = env("B")

  step "s" {
    get "${base}/a" { header "A" = "1" }
    retry { times = 2, delay = "1s" }
    expect status == 200
    expect body.x contains "y"
    capture id = body.id
  }
}
`
	for _, target := range implemented(t) {
		first, err := target.Generate(parse(t, "x.art", src))
		if err != nil {
			t.Fatalf("%s: Generate = %v", target.Name(), err)
		}
		for i := 0; i < 5; i++ {
			again, err := target.Generate(parse(t, "x.art", src))
			if err != nil {
				t.Fatalf("%s: Generate = %v", target.Name(), err)
			}
			if again[0].Content != first[0].Content {
				t.Fatalf("%s: two runs over the same file produced different bytes", target.Name())
			}
		}
	}
}

// A target is handed a checked tree, but it must not panic on one that was built
// by hand -- `artemis ast --from-json` can decode a tree nothing checked. A step
// with no action is an error naming the step. The same two refusals for the
// JavaScript target are in js_test.go.
func TestGenerateRefusesAStepWithNoAction(t *testing.T) {
	tree := &ast.File{Scenarios: []ast.Decl{&ast.Scenario{
		Name: token.Token{Kind: token.String, Value: "x"},
		Body: []ast.Decl{&ast.StepDecl{Name: token.Token{Kind: token.String, Value: "nothing to do"}}},
	}}}
	_, err := Python{}.Generate(tree)
	if err == nil {
		t.Fatal("Generate accepted a step with no action")
	}
	if !strings.Contains(err.Error(), "nothing to do") {
		t.Errorf("Generate = %q, want it to name the step", err)
	}
}

// A Bad node is what the parser leaves where a line did not parse. Reaching a
// target means the file was not checked; it has to be reported, not emitted as
// Python that will not compile.
func TestGenerateRefusesABadExpression(t *testing.T) {
	tree := &ast.File{Scenarios: []ast.Decl{&ast.Scenario{
		Name: token.Token{Kind: token.String, Value: "x"},
		Body: []ast.Decl{&ast.StepDecl{
			Name: token.Token{Kind: token.String, Value: "broken"},
			Action: &ast.Request{
				Method: token.Token{Kind: token.Ident, Value: "get"},
				URL:    &ast.Bad{Toks: []token.Token{{Kind: token.Ident, Value: "?"}}},
			},
		}},
	}}}
	if _, err := (Python{}).Generate(tree); err == nil {
		t.Fatal("Generate accepted a Bad node in a value position")
	}
}
