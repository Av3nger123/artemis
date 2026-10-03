package codegen

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/token"
)

// The issue's done-when is "golden files cover every step type and operator", and
// a claim like that rots the moment the language grows. So it is not a claim here:
// the required sets are read out of pkg/dsl/token and pkg/dsl/check, the used sets
// are walked out of the corpus's own trees, and a word in the first and not the
// second fails this test by name.
//
// One corpus serves both targets, so this gates both of them at once: a word in
// the token tables and not in the corpus is a word neither export has ever been
// asked to emit.
//
// Walked rather than grepped. A `contains` inside a string literal is not a use of
// `contains`, and the point of the check is that the emitter was exercised.

// seen is everything the corpus writes, by the kind of thing it is.
type seen struct {
	methods  map[string]bool // the HTTP verbs that open an api step
	actions  map[string]bool // run, browser
	acts     map[string]bool // the browser actions
	ops      map[string]bool // every operator, symbol and word
	types    map[string]bool // the right-hand side of `is`
	fns      map[string]bool // the builtins called
	fields   map[string]bool // every field name of every block
	idents   map[string]bool // every identifier, for the roots
	members  map[string]bool // page.<member>
	shapes   map[string]bool // the expression shapes with no word of their own
	settings map[string]bool // config subjects
}

func TestCorpusCoversTheLanguage(t *testing.T) {
	got := walkCorpus(t, corpusDir)

	// Every root of every step type, so no step type's observation goes
	// unexported.
	for _, typ := range check.StepTypes() {
		missing(t, corpusDir, "root of a "+typ.String()+" step", check.Roots(typ), got.idents)
	}
	if members, ok := check.Members("page"); ok {
		missing(t, corpusDir, "member of page", members, got.members)
	}

	missing(t, corpusDir, "HTTP verb", token.Methods, got.methods)
	missing(t, corpusDir, "action block", token.Actions, got.actions)
	missing(t, corpusDir, "browser action", token.BrowserActions, got.acts)
	missing(t, corpusDir, "comparison operator", token.Comparisons, got.ops)
	missing(t, corpusDir, "word operator", token.WordOperators, got.ops)
	missing(t, corpusDir, "type name", token.TypeNames, got.types)
	missing(t, corpusDir, "builtin", token.Builtins, got.fns)
	missing(t, corpusDir, "config subject", token.ConfigBlocks, got.settings)

	// Every field of every block, which is where a request, a run, a retry, a
	// step and a config keep their settings.
	for _, fields := range [][]string{
		token.RequestFields, token.RunFields, token.RetryFields,
		token.StepFields, token.BrowserConfigFields,
	} {
		missing(t, corpusDir, "block field", fields, got.fields)
	}

	// The shapes with no entry in a token table: unary minus, an author's
	// parentheses, the two composite literals, an interpolated string, a regex
	// literal, a subscript and a dotted path.
	missing(t, corpusDir, "expression shape", []string{
		"unary minus", "parentheses", "object", "array",
		"interpolation", "regex", "index", "member",
	}, got.shapes)
}

// Every fixture has a golden for every target beside it.
//
// This is the issue's done-when in one test. A fixture with a .py.golden and no
// .js.golden is a rule the Python target implements and the JavaScript target
// was never asked about, and it fails here by name rather than passing quietly
// -- which is the difference between a missing golden and a silent gap.
func TestEveryFixtureHasAGolden(t *testing.T) {
	for _, name := range corpus(t) {
		for _, tc := range goldenTargets {
			if _, err := os.Stat(filepath.Join(corpusDir, name+tc.suffix)); err != nil {
				t.Errorf("%s has no %s golden: %v", name, tc.target.Name(), err)
			}
		}
	}
}

// And every golden has a fixture, so a renamed .art file leaves no orphan behind.
func TestEveryGoldenHasAFixture(t *testing.T) {
	have := map[string]bool{}
	for _, name := range corpus(t) {
		have[name] = true
	}
	for _, tc := range goldenTargets {
		paths, err := filepath.Glob(filepath.Join(corpusDir, "*"+tc.suffix))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range paths {
			name := strings.TrimSuffix(filepath.Base(p), tc.suffix)
			if !have[name] {
				t.Errorf("%s has no %s%s beside it", p, name, artExt)
			}
		}
	}
}

// missing reports every word of want the corpus in where does not use.
func missing(t *testing.T, where, what string, want []string, have map[string]bool) {
	t.Helper()
	var absent []string
	for _, w := range want {
		if !have[w] {
			absent = append(absent, w)
		}
	}
	if len(absent) > 0 {
		sort.Strings(absent)
		t.Errorf("the corpus uses no %s: %s -- add one to a fixture in %s",
			what, strings.Join(absent, ", "), where)
	}
}

// walkCorpus parses every fixture in each directory and records what it uses.
//
// It takes the directories rather than reading corpusDir, because two corpora
// are checked against the token tables for different things: the golden one for
// the whole language, and parity_test.go's conformance one for every operator
// over both non-browser step types.
func walkCorpus(t *testing.T, dirs ...string) seen {
	t.Helper()
	got := seen{
		methods: map[string]bool{}, actions: map[string]bool{}, acts: map[string]bool{},
		ops: map[string]bool{}, types: map[string]bool{}, fns: map[string]bool{},
		fields: map[string]bool{}, idents: map[string]bool{}, members: map[string]bool{},
		shapes: map[string]bool{}, settings: map[string]bool{},
	}
	for _, dir := range dirs {
		for _, name := range corpusIn(t, dir) {
			path := filepath.Join(dir, name+artExt)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(parse(t, path, string(src)), func(n ast.Node) { record(&got, n) })
		}
	}
	return got
}

// record notes what one node is.
func record(got *seen, n ast.Node) {
	switch n := n.(type) {
	case *ast.Request:
		got.methods[n.Method.Value] = true
	case *ast.Run:
		got.actions[n.Keyword.Value] = true
	case *ast.Browser:
		got.actions[n.Keyword.Value] = true
	case *ast.BrowserAct:
		got.acts[n.Name.Value] = true
	case *ast.ConfigDecl:
		got.settings[n.Subject.Value] = true
	case *ast.Field:
		got.fields[n.Name.Value] = true
	case *ast.Binary:
		got.ops[operator(n.Op)] = true
	case *ast.Unary:
		if n.Op.Kind == token.Minus {
			got.shapes["unary minus"] = true
			return
		}
		got.ops[n.Op.Value] = true
	case *ast.Exists:
		got.ops["exists"] = true
	case *ast.IsType:
		got.ops["is"] = true
		got.types[n.Type.Value] = true
	case *ast.Call:
		if n.Callee != nil {
			got.fns[n.Callee.Name()] = true
		}
	case *ast.Ident:
		got.idents[n.Name()] = true
	case *ast.Member:
		got.shapes["member"] = true
		if id, ok := n.X.(*ast.Ident); ok && id.Name() == "page" {
			got.members[n.Name.Value] = true
		}
	case *ast.Index:
		got.shapes["index"] = true
	case *ast.Paren:
		got.shapes["parentheses"] = true
	case *ast.Object:
		got.shapes["object"] = true
	case *ast.Array:
		got.shapes["array"] = true
	case *ast.Interp:
		got.shapes["interpolation"] = true
	case *ast.Literal:
		if n.Kind() == token.Regex {
			got.shapes["regex"] = true
		}
	}
}
