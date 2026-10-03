package encode

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/parser"
)

// goldenFixtures are the three fixtures whose whole encoding is pinned.
//
// Not the whole corpus: the round trip and the schema validation already run
// over every file, and forty-eight span-laden JSON documents would be forty-six
// nobody reads. kinds.art exists for this: every node kind once each, in as few
// lines as the grammar allows, so the document pinning a published contract is
// one a reviewer can read end to end. The other two cover what it cannot --
// every comment slot, and a file with ast.Bad nodes in it.
//
// TestGoldensCoverEveryKind fails if a kind is added without one of them growing
// to hold it.
var goldenFixtures = map[string]string{
	"kinds.json":          "testdata/kinds.art",
	"print-comments.json": "../print/testdata/comments.art",
	"parser-recover.json": "../parser/testdata/recover.art",
}

// TestEncodingGoldens pins the encoding of the three fixtures.
func TestEncodingGoldens(t *testing.T) {
	for name, path := range goldenFixtures {
		t.Run(name, func(t *testing.T) {
			src := read(t, path)
			tree, _ := parser.Parse(path, src)
			info, _ := check.Check(tree)
			doc, err := Encode(tree, info)
			if err != nil {
				t.Fatalf("encoding %s: %v", path, err)
			}
			golden(t, name, string(doc))
		})
	}
}

// TestGoldensCoverEveryKind is what makes three goldens enough. Every kind the
// schema declares has to appear in one of them, so a node type added to the
// encoder and not exercised by a golden is a failure here rather than an
// untested field in a published contract.
func TestGoldensCoverEveryKind(t *testing.T) {
	seen := map[string]bool{}
	for name := range goldenFixtures {
		for _, k := range kindsIn(t, read(t, "testdata/"+name)) {
			seen[k] = true
		}
	}
	var missing []string
	for _, n := range nodeSchemas {
		if !seen[n.Kind] {
			missing = append(missing, n.Kind)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("no golden exercises %s; add a fixture to goldenFixtures that does",
			strings.Join(missing, ", "))
	}
}

// TestEncodingIsTotalOverTheCorpus is R1: every file in the corpus encodes,
// including the ones full of ast.Bad nodes, and every node in the result
// carries a six-field span.
//
// The invalid corpus matters most here. A file mid-edit in a UI is exactly one
// of those files, and `artemis ast` has to describe it rather than refuse it.
func TestEncodingIsTotalOverTheCorpus(t *testing.T) {
	for path, src := range corpus(t) {
		tree, _ := parser.Parse(path, src)
		info, _ := check.Check(tree)
		doc, err := Encode(tree, info)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		var raw any
		if err := json.Unmarshal(doc, &raw); err != nil {
			t.Errorf("%s: encoded to invalid JSON: %v", path, err)
			continue
		}
		walkNodes(t, raw, func(nodePath string, node map[string]any) {
			span, ok := node["span"].(map[string]any)
			if !ok {
				t.Errorf("%s: %s has no span", path, nodePath)
				return
			}
			for _, f := range []string{"file", "line", "col", "endLine", "endCol", "offset"} {
				if _, ok := span[f]; !ok {
					t.Errorf("%s: %s's span has no %q", path, nodePath, f)
				}
			}
		})
	}
}

// TestBadNodesKeepTheirBytes is the other half of totality: a line that did not
// parse reaches the document verbatim, so `artemis ast` on a broken file tells
// a UI what the broken part actually says.
func TestBadNodesKeepTheirBytes(t *testing.T) {
	path := "../parser/testdata/recover.art"
	src := read(t, path)
	tree, _ := parser.Parse(path, src)
	doc, err := Encode(tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	var raw any
	if err := json.Unmarshal(doc, &raw); err != nil {
		t.Fatal(err)
	}
	found := 0
	walkNodes(t, raw, func(nodePath string, node map[string]any) {
		if node["kind"] != kindBad {
			return
		}
		found++
		s, ok := node["source"].(string)
		if !ok {
			t.Errorf("%s has no source", nodePath)
			return
		}
		if !strings.Contains(src, s) {
			t.Errorf("%s's source is not in the file: %q", nodePath, s)
		}
	})
	if found == 0 {
		t.Fatalf("%s drew no bad nodes, so this test is asserting nothing", path)
	}
}

// TestEncodingIsDeterministic is what a golden depends on: no map iteration, no
// timestamps, so the same tree is always the same bytes.
func TestEncodingIsDeterministic(t *testing.T) {
	path := "../parser/testdata/exprs.art"
	src := read(t, path)
	tree, _ := parser.Parse(path, src)
	info, _ := check.Check(tree)
	for i := 0; i < 8; i++ { //nolint:intrange // go 1.21
		first, err := Encode(tree, info)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Encode(tree, info)
		if err != nil {
			t.Fatal(err)
		}
		if string(first) != string(again) {
			t.Fatalf("two encodings of the same tree differ on run %d", i)
		}
	}
}

// TestNilInfoIsConservative: a caller that has a tree but never ran the checker
// gets `complex` and `unknown`, which are the answers a form can keep rather
// than a promise it cannot.
func TestNilInfoIsConservative(t *testing.T) {
	tree, bag := parser.Parse("x.art", "scenario \"s\" {\n  step \"one\" {\n    get \"/a\"\n    expect status == 200\n  }\n}\n")
	if bag.HasErrors() {
		t.Fatalf("the fixture does not parse: %v", bag.All())
	}
	doc, err := EncodeString(tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, `"class": "complex"`) {
		t.Error("an expect encoded without the checker should read complex")
	}
	if !strings.Contains(doc, `"stepType": "unknown"`) {
		t.Error("a step encoded without the checker should read unknown")
	}

	info, _ := check.Check(tree)
	withInfo, err := EncodeString(tree, info)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withInfo, `"class": "simple"`) || !strings.Contains(withInfo, `"stepType": "api"`) {
		t.Error("with the checker, `expect status == 200` in a get step is simple and api")
	}
}

// TestNilTreeIsAnEmptyDocument: a caller holding whatever the parser gave it
// never has to branch. There is no "scenarios" key, because an empty list is
// absent rather than `[]` -- obj.set's rule, so a client testing truthiness and
// a client testing presence agree.
func TestNilTreeIsAnEmptyDocument(t *testing.T) {
	doc, err := EncodeString(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"schemaVersion\": 1,\n  \"file\": \"\"\n}\n"
	if doc != want {
		t.Errorf("nil tree encoded as\n%s\nwant\n%s", doc, want)
	}
}

// TestHTMLIsNotEscaped: a `<` in a selector or a `&` in a URL is itself. The
// escapes are noise in a file a person reads and nothing a JSON parser needs,
// which is the rule diag.JSON already follows.
func TestHTMLIsNotEscaped(t *testing.T) {
	tree, bag := parser.Parse("x.art", `scenario "s" { var q = "a&b<c>d" }`)
	if bag.HasErrors() {
		t.Fatalf("the fixture does not parse: %v", bag.All())
	}
	doc, err := EncodeString(tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, escape := range []string{"\\u0026", "\\u003c", "\\u003e"} {
		if strings.Contains(doc, escape) {
			t.Errorf("%s is in the output:\n%s", escape, doc)
		}
	}
	if !strings.Contains(doc, `a&b<c>d`) {
		t.Errorf("the decoded value is not in the output as written:\n%s", doc)
	}
}

// kindsIn is every `kind` value in a document.
func kindsIn(t *testing.T, doc string) []string {
	t.Helper()
	var raw any
	if err := json.Unmarshal([]byte(doc), &raw); err != nil {
		t.Fatalf("reading a golden: %v", err)
	}
	var out []string
	walkNodes(t, raw, func(_ string, node map[string]any) {
		if k, ok := node["kind"].(string); ok {
			out = append(out, k)
		}
	})
	return out
}

// walkNodes calls f for every object in the document that has a `kind`, which
// is every node and nothing else: a span, a comment and a segment have none.
func walkNodes(t *testing.T, v any, f func(path string, node map[string]any)) {
	t.Helper()
	walkJSON("", v, func(path string, m map[string]any) {
		if _, ok := m["kind"]; ok {
			f(path, m)
		}
	})
}

func walkJSON(path string, v any, f func(string, map[string]any)) {
	switch v := v.(type) {
	case map[string]any:
		f(path, v)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkJSON(path+"."+k, v[k], f)
		}
	case []any:
		for i, it := range v {
			walkJSON(path+"["+itoa(i)+"]", it, f)
		}
	}
}
