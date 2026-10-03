package encode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"
)

// Decode turns a document back into a tree.
//
// It is three stages, and the middle one is the interesting one:
//
//	build     the document into a tree of synthetic tokens (build.go)
//	print     that tree in canonical layout (pkg/dsl/print)
//	parse     the printed source back (pkg/dsl/parser)
//
// The tree returned is the *reparsed* one, which buys three things. Its spans
// are real -- they locate the canonical source a caller is about to write -- so
// a caller can check it, lower it, and get diagnostics that point somewhere. A
// document that does not describe a well-formed file is caught by the grammar
// rather than by a hand-written validator here, which would be a second and
// divergent copy of it. And `Encode(Decode(j)) == j` is exact rather than exact
// modulo spans, because both sides' spans locate the same canonical source.
//
// What it will not do is guess. A tree holding a node that did not parse is
// refused, because the design settles that the UI owns incomplete state in its
// own memory and only ever serialises a complete tree. And canonical layout adds
// no parentheses: a client that means `(a or b) and c` has to send the paren
// node, because `a or b and c` is a different expression and nothing here will
// invent the brackets to keep them apart.
func Decode(data []byte) (*ast.File, error) {
	doc, err := readDocument(data)
	if err != nil {
		return nil, err
	}
	if err := checkVersion(doc); err != nil {
		return nil, err
	}
	name, err := doc.optStr("file")
	if err != nil {
		return nil, err
	}

	tree, err := buildFile(doc)
	if err != nil {
		return nil, err
	}

	src := print.Canonical(tree)
	parsed, bag := parser.Parse(name, src)
	if bag.HasErrors() {
		return nil, notAFile(src, bag)
	}
	return parsed, nil
}

// DecodeString is Decode over a string.
func DecodeString(s string) (*ast.File, error) { return Decode([]byte(s)) }

// Source is Decode followed by the canonical printer: a document in, `.art`
// source out, which is what `artemis ast --from-json` writes.
//
// Printing the reparsed tree rather than the one Decode built is free of
// surprises because canonical mode is idempotent -- its own output measures the
// same as what it was given -- so this is the same source Decode parsed.
func Source(data []byte) (string, error) {
	tree, err := Decode(data)
	if err != nil {
		return "", err
	}
	return print.Canonical(tree), nil
}

// readDocument parses the bytes as a JSON object. The error says the document is not
// JSON rather than quoting encoding/json's offset at somebody, which on a 4000
// line tree is not a position anyone can use on its own -- so it keeps both.
func readDocument(data []byte) (fields, error) {
	var raw any
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		return fields{}, fmt.Errorf("reading the tree as JSON: %w", err)
	}
	return value{v: raw, path: "<document>"}.object()
}

// checkVersion refuses a document this build cannot read.
//
// A version from the future is an error and not a warning: the UI ships and
// upgrades independently of the CLI, so the mismatch is real and guessing at a
// shape that was designed after this binary was built is how a tree gets
// silently mangled. An older version would be readable -- nothing has been
// removed yet -- and when something is, this is where the migration goes.
func checkVersion(doc fields) error {
	v, ok := doc.m["schemaVersion"]
	if !ok {
		return fmt.Errorf("<document>: missing %q; "+
			"every tree artemis writes carries one and every tree it reads must", "schemaVersion")
	}
	n, ok := v.(float64)
	if !ok || n != float64(int(n)) {
		return fmt.Errorf("<document>: %q should be a whole number, found %s", "schemaVersion", jsonType(v))
	}
	if int(n) > SchemaVersion {
		return fmt.Errorf("<document>: this tree is schemaVersion %d; this artemis understands up to %d",
			int(n), SchemaVersion)
	}
	return nil
}

// buildFile is the document's scenarios and the comments at the end of the
// file.
func buildFile(doc fields) (*ast.File, error) {
	items, err := doc.list("scenarios")
	if err != nil {
		return nil, err
	}
	f := &ast.File{EOF: syn(token.EOF, "")}
	if f.Scenarios, err = decls(items); err != nil {
		return nil, err
	}

	c, present, err := commentsOf(doc)
	if err != nil {
		return nil, err
	}
	if present {
		attachAbove(&f.EOF, c.above, false)
		attachAfter(&f.EOF, c.after)
	}
	return f, nil
}

// notAFile is the error for a document that built a tree the grammar does not
// accept: an unknown HTTP verb in a request, a field name with a space in it, a
// literal whose text is not a literal.
//
// It quotes the canonical source it produced and the diagnostics that source
// drew, because the fault is in the document and the printed line is the one
// thing that shows what the document actually said.
func notAFile(src string, bag *diag.Bag) error {
	var b strings.Builder
	b.WriteString("the tree does not describe a valid file")
	for i, d := range bag.All() {
		if d.Severity != diag.Error {
			continue
		}
		if i > 4 {
			b.WriteString("\n  ...")
			break
		}
		fmt.Fprintf(&b, "\n  %d:%d %s (%s)", d.Span.Line, d.Span.Col, d.Message, d.Code)
	}
	if strings.TrimSpace(src) != "" {
		fmt.Fprintf(&b, "\nit printed as:\n%s", indentLines(src))
	}
	return fmt.Errorf("%s", b.String())
}

// indentLines indents the printed source two spaces, and stops after twenty
// lines: the error is for the reader, and a whole file inside one is not.
func indentLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 20 {
		lines = append(lines[:20], "  ...")
	}
	for i, l := range lines {
		if l != "  ..." {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}
