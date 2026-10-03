// Package encode is the Artemis DSL's machine contract: the tree as versioned
// JSON, in both directions.
//
// It is the thing subsystem D's UI builder is built against, and the reason the
// UI, the interpreter and the transpilers cannot drift -- they all consume the
// same tree, produced by the same front end that `artemis run` uses.
//
//	artemis ast -f x.art            # tree as JSON
//	artemis ast --from-json < t.json # JSON back to source
//
// # The encoding is semantic; the write-back is canonical
//
// One JSON object per node: its kind, its fields, its span, the checker's
// labels, and the comments canonical layout can place. Not tokens, not
// whitespace, not commas -- canonical layout supplies those. So a UI that
// writes through JSON gets the file `artemis fmt -w` would have written.
//
// That is a deliberate split of duty with pkg/dsl/print. The *lossless* path --
// load a file, change one field, write back a one-line diff -- is parse, edit
// the tree, print.Preserving, in Go. Making this encoding carry trivia as well
// would oblige every client to understand trivia attachment, for a guarantee the
// issue explicitly does not ask of it: `--from-json` prints "via the canonical
// printer".
//
// Comments are carried anyway. Whitespace is layout and canonical mode owns it,
// but a comment is the author's, and a save cycle that deleted one would be the
// exact failure this worklane exists to prevent. Canonical layout can place a
// comment in five positions, so the encoding has five slots named for them --
// see comments.go.
//
// # The two fixed points
//
// For canonically-formatted source c:
//
//	print.Canonical(Decode(Encode(parser.Parse(f, c)))) == c      // source
//	Encode(Decode(j)) == j                                        // structure
//
// byte for byte, over the whole fixture corpus. The second one is exact rather
// than "exact once spans are elided" because Decode canonically prints the tree
// it builds and parses the result back, so the spans on both sides locate the
// same canonical source. roundtrip_test.go asserts both.
//
// # What a client may rely on
//
// schemaVersion on the document, because the UI ships and upgrades
// independently of the CLI. Every node's span, with all six of file, line, col,
// endLine, endCol and offset -- the same shape and the same field names
// diag.JSON uses, so a client has one span parser and not two. And the
// checker's conclusions on the nodes they belong to: `class` on every expect,
// `stepType` and `scope` on every step. Those three are output-only; Decode
// ignores them, because a client sending a tree has no classifier of its own and
// should not need one.
//
// Schema() describes the whole thing, and testdata/schema.json pins it, so an
// unintended change to the UI contract fails CI with a diff somebody has to
// read.
package encode

import (
	"bytes"
	"encoding/json"
	"fmt"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/token"
)

// SchemaVersion is the version of the encoding this build speaks. It is the one
// place the number lives: the document carries it, Schema() reports it, and
// Decode refuses a document from the future.
//
// Bump it when a client that reads the old shape would misread the new one --
// a field removed, renamed, or changed in type. Adding an optional field is
// not a bump: a client ignoring an unknown key still reads the tree correctly.
const SchemaVersion = 1

// Encode writes tree as a JSON document.
//
// info is the checker's output for this tree and may be nil, in which case
// every expect reads `complex` and every step `unknown` -- check.Info's
// accessors are nil-safe and those are the conservative answers. A caller that
// has a tree has usually just run the checker over it; pkg/cli's loadArt
// returns both.
//
// Indented with two spaces and newline-terminated, like diag.JSON: a tree dump
// is read by people at least as often as by programs, and a golden file of one
// has to be reviewable. The output is fully determined by the input -- key
// order is insertion order, not Go map order -- so the same tree always
// produces the same bytes.
func Encode(tree *ast.File, info *check.Info) ([]byte, error) {
	return marshalIndent(document(tree, info))
}

// EncodeString is Encode into a string, for tests and for a caller assembling
// output rather than streaming it.
func EncodeString(tree *ast.File, info *check.Info) (string, error) {
	b, err := Encode(tree, info)
	return string(b), err
}

// document builds the top-level object: the version, the file the spans refer
// to, the scenarios, and whatever comments the end of the file was carrying.
//
// A nil tree is an empty document rather than an error, so a caller holding
// whatever the parser gave it never has to branch.
func document(tree *ast.File, info *check.Info) *obj {
	o := newObj()
	o.set("schemaVersion", SchemaVersion)
	if tree == nil {
		o.set("file", "")
		o.set("scenarios", []any{})
		return o
	}
	o.set("file", fileOf(tree))
	e := &enc{info: info}
	o.set("scenarios", e.list(toNodes(tree.Scenarios)))
	o.set("comments", eofComments(tree.EOF))
	return o
}

// fileOf is the file name the tree's spans carry, which is the name
// parser.Parse was given. It is read off the tree rather than passed in so that
// the document cannot disagree with the spans inside it.
//
// An empty string is the honest answer for a tree with no positioned token in
// it at all -- one built in Go rather than parsed.
func fileOf(tree *ast.File) string {
	if f := tree.Span().File; f != "" {
		return f
	}
	for _, t := range tree.Tokens(nil) {
		if t.Span.File != "" {
			return t.Span.File
		}
	}
	return ""
}

// enc carries the checker's output down the walk. There is nothing else to
// carry: the encoding of a node depends on the node and on info, and never on
// where in the tree it sits.
type enc struct{ info *check.Info }

// jsonSpan is a span as a client reads it. All six fields of token.Span, with
// diag/json.go's names and order, so `artemis ast` and `artemis parse --json`
// describe a position the same way.
type jsonSpan struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"endLine"`
	EndCol  int    `json:"endCol"`
	Offset  int    `json:"offset"`
}

func spanOf(s token.Span) jsonSpan {
	return jsonSpan{
		File:    s.File,
		Line:    s.Line,
		Col:     s.Col,
		EndLine: s.EndLine,
		EndCol:  s.EndCol,
		Offset:  s.Offset,
	}
}

// obj is a JSON object whose keys stay in the order they were set.
//
// A Go map would serialise in sorted order, which would scatter `kind` into the
// middle of a node and make a golden file harder to read than it has to be.
// Insertion order puts the kind first, the node's fields next, and the derived
// labels and comments last.
type obj struct {
	keys []string
	vals map[string]any
}

func newObj() *obj { return &obj{vals: map[string]any{}} }

// set records key, unless the value is one a client should see as absent rather
// than as null or empty.
//
// Absent-not-null is the rule diag/json.go already follows, for the same reason:
// a client testing truthiness and a client testing presence then agree. So an
// optional child that is not there, an empty list and a false flag all simply do
// not appear.
func (o *obj) set(key string, v any) *obj {
	if isEmptyValue(v) {
		return o
	}
	if _, seen := o.vals[key]; !seen {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
	return o
}

// isEmptyValue reports whether v is the nothing that set drops.
func isEmptyValue(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case *obj:
		return v == nil
	case bool:
		return !v
	case []any:
		return len(v) == 0
	case []string:
		return len(v) == 0
	case []comment:
		return len(v) == 0
	}
	return false
}

// MarshalJSON writes the object in insertion order.
//
// Each value is marshalled with HTML escaping off, so a `<` in a message or a
// `&` in a URL is itself rather than a < -- noise in a file a person reads
// and nothing a JSON parser needs. The result is compact; the top-level
// encoder re-indents the whole document.
func (o *obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := marshalValue(k)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		val, err := marshalValue(o.vals[k])
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %w", k, err)
		}
		b.Write(val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalValue is compact JSON for one value, with HTML escaping off.
func marshalValue(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// marshalIndent is the document as it is written: two-space indentation, HTML
// escaping off, one trailing newline.
func marshalIndent(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// toNodes widens a declaration list, because the walk is over ast.Node.
func toNodes[T ast.Node](ds []T) []ast.Node {
	out := make([]ast.Node, 0, len(ds))
	for _, d := range ds {
		out = append(out, d)
	}
	return out
}
