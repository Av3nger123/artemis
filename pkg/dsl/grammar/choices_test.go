package grammar

import (
	"encoding/json"
	"flag"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	dsltoken "artemis/pkg/dsl/token"
)

var update = flag.Bool("update", false, "rewrite testdata/choices.json from what the binary emits")

// tablesFile is the one file every word set of the language lives in. The
// drift test below reads it as source rather than importing it, so that a
// table added there and not here is a failure rather than a thing nobody
// noticed.
const tablesFile = "../token/tables.go"

// owner maps each exported table in token/tables.go to the choice point that
// has to carry it.
//
// It is a mapping and not a list on purpose: TestEveryTableIsAChoicePoint
// fails on a table with no entry here, and then goes on to compare the choice
// point's values to the table element for element. So an operator added to
// token.Comparisons and not emitted fails, and a table added to the language
// and not wired up fails, and the two failures say different things.
var owner = map[string]string{
	"Methods":             "method",
	"Actions":             "action",
	"BrowserActions":      "browserAction",
	"TypeNames":           "typeName",
	"WordOperators":       "wordOperator",
	"Comparisons":         "comparison",
	"Blocks":              "declaration",
	"StepStatements":      "stepStatement",
	"RequestFields":       "requestField",
	"RunFields":           "runField",
	"RetryFields":         "retryField",
	"StepFields":          "stepField",
	"ConfigBlocks":        "configSubject",
	"BrowserConfigFields": "configBrowserField",
	"Builtins":            "builtin",
	"Reserved":            "reservedWord",
	"CollectionItems":     "collectionItem",
	"UseLines":            "useLine",
	"DropTargets":         "dropTarget",
}

// TestEveryTableIsAChoicePoint is the gate this issue is graded on.
//
// `artemis grammar --json` exists so that a client's dropdowns come from the
// binary rather than from a hardcoded list that drifts. That is only true if
// the document is a projection of the language's own tables, and the way to
// keep it true is to read the tables as source: go/parser over tables.go,
// every exported var in it, each one reconciled with the choice point that
// owns it.
func TestEveryTableIsAChoicePoint(t *testing.T) {
	doc := Choices()
	for name, words := range exportedTables(t) {
		choice, ok := owner[name]
		if !ok {
			t.Errorf("token.%s is an exported word set with no choice point: add one to Choices() and name it in owner", name)
			continue
		}
		c, ok := doc.Choices[choice]
		if !ok {
			t.Errorf("owner says token.%s belongs to the choice point %q, which Choices() does not emit", name, choice)
			continue
		}
		got := valuesOf(c)
		if len(got) != len(words) {
			t.Errorf("choice %q has %d values, token.%s has %d: %v vs %v", choice, len(got), name, len(words), got, words)
			continue
		}
		for i := range words {
			if got[i] != words[i] {
				t.Errorf("choice %q value %d = %q, token.%s[%d] = %q", choice, i, got[i], name, i, words[i])
			}
		}
	}
}

// TestEveryChoicePointIsDocumented stops a choice point shipping with an
// empty doc or an empty list, either of which is a dropdown a client cannot
// label or cannot fill.
func TestEveryChoicePointIsDocumented(t *testing.T) {
	for name, c := range Choices().Choices {
		if c.Doc == "" {
			t.Errorf("choice %q has no doc", name)
		}
		if len(c.Values) == 0 {
			t.Errorf("choice %q has no values", name)
		}
		for i, v := range c.Values {
			if v.Value == "" {
				t.Errorf("choice %q value %d is empty", name, i)
			}
		}
	}
}

// TestEveryBlockIsAChoicePoint reconciles the other table-holder: check's
// block specs. A sixth block added to check.BlockNames and not emitted here
// would be a field set a form cannot render.
func TestEveryBlockIsAChoicePoint(t *testing.T) {
	emitted := map[string]string{
		"request":       "requestField",
		"run":           "runField",
		"retry":         "retryField",
		"step":          "stepField",
		"configBrowser": "configBrowserField",
	}
	doc := Choices()
	for _, block := range check.BlockNames() {
		choice, ok := emitted[block]
		if !ok {
			t.Errorf("check.BlockNames has %q with no choice point", block)
			continue
		}
		fields, _ := check.BlockFields(block)
		c := doc.Choices[choice]
		if len(c.Values) != len(fields) {
			t.Errorf("choice %q has %d values, check.BlockFields(%q) has %d", choice, len(c.Values), block, len(fields))
			continue
		}
		for i, f := range fields {
			v := c.Values[i]
			if v.Value != f.Name || v.ValueKind != f.ValueKind || v.NeedsKey != f.NeedsKey {
				t.Errorf("choice %q value %d = %+v, check says %+v", choice, i, v, f)
			}
		}
	}
}

// TestBrowserActionsCarryTheirArity is the browser half of the drift gate
// named in the issue: an action added to token.BrowserActions arrives here
// through check.BrowserActs, and without an example or an arity rule it ships
// a form widget that guesses.
func TestBrowserActionsCarryTheirArity(t *testing.T) {
	c := Choices().Choices["browserAction"]
	if len(c.Values) != len(dsltoken.BrowserActions) {
		t.Fatalf("browserAction has %d values, token.BrowserActions has %d", len(c.Values), len(dsltoken.BrowserActions))
	}
	for i, v := range c.Values {
		if v.Value != dsltoken.BrowserActions[i] {
			t.Errorf("browserAction value %d = %q, want %q", i, v.Value, dsltoken.BrowserActions[i])
		}
		if v.Example == "" {
			t.Errorf("browser action %q ships with no example", v.Value)
		}
	}
	// fill, select and upload are the three that take a value. Asserting the
	// count rather than the names keeps this from being a third copy of the
	// list while still failing if an action's arity flips.
	withValue := 0
	for _, v := range c.Values {
		if v.TakesValue {
			withValue++
		}
	}
	if withValue != 3 {
		t.Errorf("%d browser actions take a value; the language has 3 (fill, select, upload)", withValue)
	}
}

// TestStepTypesCarryTheirScope asserts the path picker's input is the
// checker's scope, root for root.
func TestStepTypesCarryTheirScope(t *testing.T) {
	c := Choices().Choices["stepType"]
	if len(c.Values) != len(check.StepTypes()) {
		t.Fatalf("stepType has %d values, check.StepTypes has %d", len(c.Values), len(check.StepTypes()))
	}
	for i, st := range check.StepTypes() {
		v := c.Values[i]
		if v.Value != st.String() {
			t.Errorf("stepType value %d = %q, want %q", i, v.Value, st.String())
		}
		if !equal(v.Roots, check.Roots(st)) {
			t.Errorf("%s roots = %v, check says %v", st, v.Roots, check.Roots(st))
		}
		if !equal(v.Functions, check.Functions(st)) {
			t.Errorf("%s functions = %v, check says %v", st, v.Functions, check.Functions(st))
		}
		for _, root := range v.Roots {
			members, closed := check.Members(root)
			if closed && !equal(v.Members[root], members) {
				t.Errorf("%s members[%q] = %v, check says %v", st, root, v.Members[root], members)
			}
			if !closed && v.Members[root] != nil {
				t.Errorf("%s members[%q] is set, but %q has no closed member set", st, root, root)
			}
		}
	}
}

// TestDiagnosticCodesAreTheRegistry: a UI keys behaviour off codes, so the
// index it is given has to be the registry and not a subset of it.
func TestDiagnosticCodesAreTheRegistry(t *testing.T) {
	c := Choices().Choices["diagnosticCode"]
	codes := diag.Codes()
	if len(c.Values) != len(codes) {
		t.Fatalf("diagnosticCode has %d values, diag.Codes has %d", len(c.Values), len(codes))
	}
	for i, v := range c.Values {
		if v.Value != string(codes[i].Code) {
			t.Errorf("diagnosticCode value %d = %q, want %q", i, v.Value, codes[i].Code)
		}
		if v.Severity != codes[i].Severity.String() || v.Doc != codes[i].Description {
			t.Errorf("code %q = {%q, %q}, registry says {%q, %q}",
				v.Value, v.Severity, v.Doc, codes[i].Severity, codes[i].Description)
		}
	}
}

// TestJSONParsesAndCarriesItsVersion is the shape check a client depends on.
func TestJSONParsesAndCarriesItsVersion(t *testing.T) {
	b, err := JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the document does not parse: %v", err)
	}
	if doc["schemaVersion"] != float64(SchemaVersion) {
		t.Errorf("schemaVersion = %v, want %d", doc["schemaVersion"], SchemaVersion)
	}
	if _, ok := doc["choices"].(map[string]any); !ok {
		t.Error("choices is not an object")
	}
	if b[len(b)-1] != '\n' {
		t.Error("the document does not end in a newline")
	}
}

// TestChoicesGolden is the contract snapshot. A change to what a client reads
// that nobody meant fails CI with a diff somebody has to approve.
func TestChoicesGolden(t *testing.T) {
	b, err := JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "choices.json")
	if *update {
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run `go test ./pkg/dsl/grammar -update` to create it)", path, err)
	}
	if string(b) != string(want) {
		t.Errorf("%s is out of date; run `go test ./pkg/dsl/grammar -update` and read the diff", path)
	}
}

func valuesOf(c Choice) []string {
	out := make([]string, 0, len(c.Values))
	for _, v := range c.Values {
		out = append(out, v.Value)
	}
	return out
}

func equal(a, b []string) bool {
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

// exportedTables parses token/tables.go and returns every exported
// `Name = []string{...}` in it, with its words in source order.
//
// Reading the file is the point. A test holding its own list of tables could
// not fail on a table that was added to the language, which is exactly the
// drift `artemis grammar --json` exists to prevent.
func exportedTables(t *testing.T) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, tablesFile, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", tablesFile, err)
	}
	out := map[string][]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !name.IsExported() || i >= len(vs.Values) {
					continue
				}
				if words, ok := stringSlice(vs.Values[i]); ok {
					out[name.Name] = words
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s yielded no exported string tables; has the file moved?", tablesFile)
	}
	return out
}

// stringSlice is the words of a `[]string{"a", "b"}` literal, and whether the
// expression was one. A var of any other shape -- the lookup maps, the
// comparison split -- is not a word table and is skipped.
func stringSlice(x ast.Expr) ([]string, bool) {
	lit, ok := x.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	at, ok := lit.Type.(*ast.ArrayType)
	if !ok {
		return nil, false
	}
	if id, ok := at.Elt.(*ast.Ident); !ok || id.Name != "string" {
		return nil, false
	}
	out := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		bl, ok := elt.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return nil, false
		}
		s, err := strconv.Unquote(bl.Value)
		if err != nil {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// TestOwnerNamesOnlyRealTables keeps owner from accumulating entries for
// tables that have been renamed or deleted, which would make
// TestEveryTableIsAChoicePoint quietly weaker.
func TestOwnerNamesOnlyRealTables(t *testing.T) {
	tables := exportedTables(t)
	var stale []string
	for name := range owner {
		if _, ok := tables[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("owner names %v, which token/tables.go no longer has", stale)
	}
}
