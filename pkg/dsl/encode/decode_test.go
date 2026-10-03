package encode

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/parser"
)

// A document is a machine contract, so the thing that matters about a bad one
// is that the error says *where*. Every case here asserts the JSON path as well
// as the complaint, because "missing \"op\"" on a tree with two hundred nodes
// is not a message anyone can act on.
func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want []string
	}{{
		name: "not JSON at all",
		doc:  `{"schemaVersion": 1`,
		want: []string{"reading the tree as JSON"},
	}, {
		name: "not an object",
		doc:  `[1, 2]`,
		want: []string{"<document>", "expected an object", "an array"},
	}, {
		name: "no schemaVersion",
		doc:  `{"file": "x.art"}`,
		want: []string{"<document>", `missing "schemaVersion"`},
	}, {
		name: "a schemaVersion from the future",
		doc:  `{"schemaVersion": 99, "scenarios": []}`,
		want: []string{"schemaVersion 99", "understands up to 1"},
	}, {
		name: "a schemaVersion that is not a number",
		doc:  `{"schemaVersion": "1"}`,
		want: []string{`"schemaVersion" should be a whole number`, "a string"},
	}, {
		name: "an unknown node kind",
		doc:  `{"schemaVersion": 1, "scenarios": [{"kind": "frobnicate"}]}`,
		want: []string{"<document>.scenarios[0]", `"frobnicate" is not a declaration`},
	}, {
		name: "a node with no kind",
		doc:  `{"schemaVersion": 1, "scenarios": [{"name": {"text": "\"s\""}}]}`,
		want: []string{"<document>.scenarios[0]", `missing "kind"`},
	}, {
		name: "a scenario with no name",
		doc:  `{"schemaVersion": 1, "scenarios": [{"kind": "scenario"}]}`,
		want: []string{"<document>.scenarios[0]", `missing "name"`},
	}, {
		name: "a step with no action",
		doc: `{"schemaVersion": 1, "scenarios": [{"kind": "scenario",
			"name": {"text": "\"s\""}, "body": [{"kind": "step", "name": {"text": "\"one\""}}]}]}`,
		want: []string{"body[0]", `missing "action"`, "a step with no action is not a complete tree"},
	}, {
		name: "a binary with no right-hand side",
		doc: `{"schemaVersion": 1, "scenarios": [{"kind": "scenario", "name": {"text": "\"s\""},
			"body": [{"kind": "step", "name": {"text": "\"one\""},
			"action": {"kind": "request", "method": "get", "url": {"kind": "literal", "literal": "string", "text": "\"/a\""}},
			"body": [{"kind": "expect", "value": {"kind": "binary", "op": "==",
			"x": {"kind": "ident", "name": "status"}}}]}]}]}`,
		want: []string{"body[0].value", `missing "y"`},
	}, {
		name: "an expression where a statement belongs",
		doc: `{"schemaVersion": 1, "scenarios": [{"kind": "scenario", "name": {"text": "\"s\""},
			"body": [{"kind": "step", "name": {"text": "\"one\""},
			"action": {"kind": "request", "method": "get", "url": {"kind": "ident", "name": "u"}},
			"body": [{"kind": "ident", "name": "x"}]}]}]}`,
		want: []string{"body[0]", `"ident" is not a step statement`},
	}, {
		name: "a field with both a value and a block",
		doc: `{"schemaVersion": 1, "scenarios": [{"kind": "scenario", "name": {"text": "\"s\""},
			"body": [{"kind": "step", "name": {"text": "\"one\""},
			"action": {"kind": "request", "method": "get", "url": {"kind": "ident", "name": "u"}},
			"body": [{"kind": "field", "name": "timeout",
			"value": {"kind": "literal", "literal": "string", "text": "\"5s\""},
			"block": {"kind": "block"}}]}]}]}`,
		want: []string{"body[0]", `exactly one of "value" and "block"`},
	}, {
		name: "a literal kind that is not one",
		doc: `{"schemaVersion": 1, "scenarios": [{"kind": "scenario", "name": {"text": "\"s\""},
			"body": [{"kind": "var", "name": "x", "value": {"kind": "literal", "literal": "date", "text": "1"}}]}]}`,
		want: []string{`value.literal`, `"date" is not a literal kind`},
	}, {
		name: "a field of the wrong type",
		doc: `{"schemaVersion": 1, "scenarios": [{"kind": "scenario", "name": {"text": "\"s\""},
			"body": [{"kind": "var", "name": 42, "value": {"kind": "ident", "name": "y"}}]}]}`,
		want: []string{"scenarios[0].body[0]", `"name" should be a string`, "a number"},
	}, {
		name: "scenarios that is not an array",
		doc:  `{"schemaVersion": 1, "scenarios": {"kind": "scenario"}}`,
		want: []string{`"scenarios" should be an array`, "an object"},
	}, {
		name: "an interpolation with no segments",
		doc: `{"schemaVersion": 1, "scenarios": [{"kind": "scenario", "name": {"text": "\"s\""},
			"body": [{"kind": "var", "name": "x", "value": {"kind": "interp", "end": "\"a\""}}]}]}`,
		want: []string{"at least one segment", "a string with no holes in it is a literal"},
	}, {
		name: "a method the grammar does not know",
		doc: `{"schemaVersion": 1, "scenarios": [{"kind": "scenario", "name": {"text": "\"s\""},
			"body": [{"kind": "step", "name": {"text": "\"one\""},
			"action": {"kind": "request", "method": "frobnicate", "url": {"kind": "ident", "name": "u"}}}]}]}`,
		want: []string{"the tree does not describe a valid file", "it printed as:"},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecodeString(c.doc)
			if err == nil {
				t.Fatalf("decoded without complaint")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not mention %q:\n%v", want, err)
				}
			}
		})
	}
}

// TestDecodeRefusesANodeThatDidNotParse is the asymmetry the design asks for.
// Encoding is total, because `artemis ast` has to describe a file mid-edit;
// decoding is not, because the UI owns incomplete state in its own memory and
// only ever serialises a complete tree.
func TestDecodeRefusesANodeThatDidNotParse(t *testing.T) {
	path := "../parser/testdata/recover.art"
	src := read(t, path)
	tree, bag := parser.Parse(path, src)
	if !bag.HasErrors() {
		t.Fatalf("%s parses clean, so this test asserts nothing", path)
	}
	info, _ := check.Check(tree)
	doc, err := Encode(tree, info)
	if err != nil {
		t.Fatalf("encoding a recovered file should work: %v", err)
	}

	_, err = Decode(doc)
	if err == nil {
		t.Fatal("decoded a tree holding a node that did not parse")
	}
	for _, want := range []string{"did not parse", "must be complete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%v", want, err)
		}
	}
}

// TestDecodeAcceptsAnOlderSchemaVersion: nothing has been removed yet, so a
// document from version 1 is readable by a later build. The check is on the
// upper bound only.
func TestDecodeAcceptsAnOlderSchemaVersion(t *testing.T) {
	if _, err := DecodeString(`{"schemaVersion": 1, "file": "x.art", "scenarios": []}`); err != nil {
		t.Fatalf("an empty document at the current version should decode: %v", err)
	}
}

// TestDecodeAnEmptyDocument: a document with no scenarios is an empty file, not
// an error -- the shape `artemis ast` writes for one.
func TestDecodeAnEmptyDocument(t *testing.T) {
	src, err := Source([]byte(`{"schemaVersion": 1, "file": "x.art"}`))
	if err != nil {
		t.Fatal(err)
	}
	if src != "" {
		t.Errorf("an empty document printed as %q, want nothing", src)
	}
}

// TestDecodeIgnoresAnUnknownKey is the forward-compatibility rule in the other
// direction: adding an optional field is not a schemaVersion bump, so a build
// that does not know a key must not choke on it.
func TestDecodeIgnoresAnUnknownKey(t *testing.T) {
	doc := `{"schemaVersion": 1, "file": "x.art", "somethingNew": {"a": 1}, "scenarios": [
		{"kind": "scenario", "name": {"text": "\"s\""}, "alsoNew": true}]}`
	src, err := Source([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if want := "scenario \"s\" {}\n"; src != want {
		t.Errorf("printed %q, want %q", src, want)
	}
}

// TestDecodeKeepsParentheses: canonical layout adds none, so a client that
// means `(a or b) and c` sends the paren node and gets it back. Dropping it
// would reassociate the expression into a different one.
func TestDecodeKeepsParentheses(t *testing.T) {
	expr := `{"kind": "binary", "op": "and",
		"x": {"kind": "paren", "x": {"kind": "binary", "op": "or",
			"x": {"kind": "ident", "name": "a"}, "y": {"kind": "ident", "name": "b"}}},
		"y": {"kind": "ident", "name": "c"}}`
	doc := `{"schemaVersion": 1, "file": "x.art", "scenarios": [
		{"kind": "scenario", "name": {"text": "\"s\""},
		 "body": [{"kind": "var", "name": "v", "value": ` + expr + `}]}]}`
	src, err := Source([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "var v = (a or b) and c") {
		t.Errorf("printed:\n%s", src)
	}
}

// TestDecodeRebuildsComments: the five slots come back where they were, which
// is what stops a UI's save cycle deleting the author's comments.
func TestDecodeRebuildsComments(t *testing.T) {
	doc := `{"schemaVersion": 1, "file": "x.art",
		"scenarios": [{"kind": "scenario", "name": {"text": "\"s\""},
			"comments": {"above": [{"text": "# above the scenario"}],
			             "open": ["# opening"],
			             "beforeClose": [{"text": "# before the brace"}],
			             "afterClose": ["# after the brace"]},
			"body": [{"kind": "var", "name": "v",
			          "value": {"kind": "literal", "literal": "number", "text": "1"},
			          "comments": {"blankAbove": true, "after": ["# trailing the var"]}}]}],
		"comments": {"above": [{"text": "# at the end of the file", "blank": true}]}}`
	src, err := Source([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := `# above the scenario
scenario "s" { # opening
  var v = 1 # trailing the var
  # before the brace
} # after the brace

# at the end of the file
`
	if src != want {
		t.Errorf("printed:\n%s\nwant:\n%s", src, want)
	}
}
