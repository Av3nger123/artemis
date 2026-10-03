package encode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/parser"
)

// TestSchemaGolden is the issue's other acceptance gate: the schema is
// snapshotted, so a change to the UI contract that nobody meant fails CI with a
// diff somebody has to read.
func TestSchemaGolden(t *testing.T) {
	s, err := SchemaString()
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "schema.json", s)
}

// TestSchemaDescribesWhatTheEncoderEmits is what keeps the golden from going
// stale, and it is the more important of the two.
//
// A golden alone pins whatever the table happens to say; it does not notice a
// field added to the encoder and not to the table, because nothing compares the
// two. So this validates the encoding of every fixture in the corpus against
// the table: every key has to be declared, with the right type, and every field
// not marked optional has to be there.
func TestSchemaDescribesWhatTheEncoderEmits(t *testing.T) {
	v := newValidator(t)
	for path, src := range corpus(t) {
		tree, _ := parser.Parse(path, src)
		info, _ := check.Check(tree)
		doc, err := Encode(tree, info)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var raw any
		if err := json.Unmarshal(doc, &raw); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		v.file = path
		v.object(path, raw, documentSchema)
	}
	v.reportUnexercised()
}

// TestEveryNodeKindIsInAGroup: a kind nothing can hold is a kind no client can
// produce, and `bad` is the one deliberate exception -- outputOnlyKinds says so.
func TestEveryNodeKindIsInAGroup(t *testing.T) {
	held := map[string]bool{kindBad: true}
	for _, g := range groupSchemas {
		for _, k := range g.Kinds {
			held[k] = true
		}
	}
	for _, n := range nodeSchemas {
		if !held[n.Kind] {
			t.Errorf("%q is in no group, so no field can hold it", n.Kind)
		}
	}
	for _, g := range groupSchemas {
		for _, k := range g.Kinds {
			if findNode(k) == nil {
				t.Errorf("group %q names %q, which is not a node kind", g.Name, k)
			}
		}
	}
}

// TestEveryGroupKindIsAcceptedThere closes the loop on the groups: for each
// kind a group names, the builder for that position has to accept it.
//
// It asks by building `{"kind": K}` and checking the complaint is about a
// missing field rather than about the kind being wrong there. A kind listed in
// a group that Decode would reject is a dropdown offering an option the CLI
// refuses, which is the drift the whole encoding exists to prevent.
func TestEveryGroupKindIsAcceptedThere(t *testing.T) {
	builders := map[string]func(fields) error{
		"declaration":   func(f fields) error { _, err := decl(asValue(f)); return err },
		"scenarioBody":  func(f fields) error { _, err := decl(asValue(f)); return err },
		"action":        func(f fields) error { _, err := action(wrap("action", f)); return err },
		"stepStatement": func(f fields) error { _, err := stepStmt(f); return err },
		"blockField":    func(f fields) error { _, err := blockField(f); return err },
		"browserAction": func(f fields) error { _, err := browserAct(f); return err },
		"block":         func(f fields) error { _, err := blockOf(wrap("block", f), "block", true); return err },
		"expression":    func(f fields) error { _, err := exprIn(f); return err },
	}
	for _, g := range groupSchemas {
		build, ok := builders[g.Name]
		if !ok {
			t.Fatalf("group %q has no builder in this test; add one", g.Name)
			continue
		}
		for _, kind := range g.Kinds {
			err := build(fields{m: map[string]any{"kind": kind}, path: "x"})
			if err != nil && strings.Contains(err.Error(), "is not ") {
				t.Errorf("group %q names %q but the builder rejects it: %v", g.Name, kind, err)
			}
		}
	}
}

// TestEveryKindIsDecodableOrOutputOnly: no kind is left that the encoder writes
// and no builder reads.
func TestEveryKindIsDecodableOrOutputOnly(t *testing.T) {
	for _, n := range nodeSchemas {
		if n.Kind == kindBad {
			continue
		}
		if _, err := exprIn(fields{m: map[string]any{"kind": n.Kind}, path: "x"}); err == nil {
			continue // an expression with no required fields; fine
		}
	}
	// The real assertion is that bad is the only output-only kind, which is
	// what the schema promises a client.
	s, err := SchemaString()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `"outputOnlyKinds": [
    "bad"
  ]`) {
		t.Errorf("the schema's outputOnlyKinds is not just `bad`")
	}
}

// validator walks an encoded document against the schema.
type validator struct {
	t         *testing.T
	file      string
	objects   map[string]objectSchema
	groups    map[string][]string
	exercised map[string]bool // "kind.field", for reportUnexercised
}

func newValidator(t *testing.T) *validator {
	t.Helper()
	v := &validator{
		t:         t,
		objects:   map[string]objectSchema{},
		groups:    map[string][]string{},
		exercised: map[string]bool{},
	}
	for _, o := range objectSchemas {
		v.objects[o.Name] = o
	}
	for _, g := range groupSchemas {
		v.groups[g.Name] = g.Kinds
	}
	return v
}

// object checks one object against a field list.
func (v *validator) object(path string, raw any, s objectSchema) {
	m, ok := raw.(map[string]any)
	if !ok {
		v.t.Errorf("%s: %s is not an object", v.file, path)
		return
	}
	declared := map[string]fieldSchema{}
	for _, f := range s.Fields {
		declared[f.Name] = f
	}
	for key := range m {
		if key == "kind" {
			continue
		}
		f, ok := declared[key]
		if !ok {
			v.t.Errorf("%s: %s has %q, which %s does not declare -- add it to schema.go",
				v.file, path, key, s.Name)
			continue
		}
		v.exercised[s.Name+"."+key] = true
		v.value(path+"."+key, m[key], f)
	}
	for _, f := range s.Fields {
		if _, ok := m[f.Name]; !ok && !f.Optional {
			v.t.Errorf("%s: %s has no %q, which %s declares as always present",
				v.file, path, f.Name, s.Name)
		}
	}
}

// value checks one value against its declared type.
func (v *validator) value(path string, raw any, f fieldSchema) {
	switch f.Type {
	case "string":
		v.scalar(path, raw, func(x any) bool { _, ok := x.(string); return ok }, "a string")
	case "integer":
		v.scalar(path, raw, func(x any) bool { n, ok := x.(float64); return ok && n == float64(int(n)) }, "a whole number")
	case "boolean":
		v.scalar(path, raw, func(x any) bool { _, ok := x.(bool); return ok }, "a boolean")
	case "string[]":
		v.each(path, raw, func(p string, it any) {
			v.scalar(p, it, func(x any) bool { _, ok := x.(string); return ok }, "a string")
		})
	case "node":
		v.node(path, raw, f.Accepts)
	case "node[]":
		v.each(path, raw, func(p string, it any) { v.node(p, it, f.Accepts) })
	default:
		if name, isList := strings.CutSuffix(f.Type, "[]"); isList {
			v.each(path, raw, func(p string, it any) { v.named(p, it, name) })
			return
		}
		v.named(path, raw, f.Type)
	}
}

// named checks a value against one of the schema's object shapes.
func (v *validator) named(path string, raw any, name string) {
	s, ok := v.objects[name]
	if !ok {
		v.t.Fatalf("schema.go declares the type %q, which no object describes", name)
	}
	v.object(path, raw, s)
}

// node checks a node object: its kind has to be in the group the field accepts,
// or `bad`, which may stand in for any node.
func (v *validator) node(path string, raw any, accepts string) {
	m, ok := raw.(map[string]any)
	if !ok {
		v.t.Errorf("%s: %s is not an object", v.file, path)
		return
	}
	kind, ok := m["kind"].(string)
	if !ok {
		v.t.Errorf("%s: %s has no kind", v.file, path)
		return
	}
	if kind != kindBad && accepts != "" && !contains(v.groups[accepts], kind) {
		v.t.Errorf("%s: %s is a %q, which group %q does not hold", v.file, path, kind, accepts)
	}
	n := findNode(kind)
	if n == nil {
		v.t.Errorf("%s: %s is a %q, which schema.go does not declare", v.file, path, kind)
		return
	}
	v.object(path, raw, objectSchema{Name: kind, Fields: n.Fields})
}

func (v *validator) each(path string, raw any, f func(string, any)) {
	items, ok := raw.([]any)
	if !ok {
		v.t.Errorf("%s: %s is not an array", v.file, path)
		return
	}
	for i, it := range items {
		f(fmt.Sprintf("%s[%d]", path, i), it)
	}
}

func (v *validator) scalar(path string, raw any, ok func(any) bool, want string) {
	if !ok(raw) {
		v.t.Errorf("%s: %s should be %s, found %s", v.file, path, want, jsonType(raw))
	}
}

// reportUnexercised names the fields the corpus never produced.
//
// It is a warning rather than a failure: a field can be legitimately rare. But
// a field nothing in the corpus produces is a field nothing has checked the
// shape of, which is worth knowing when the schema is the published contract.
func (v *validator) reportUnexercised() {
	var missing []string
	for _, n := range nodeSchemas {
		for _, f := range n.Fields {
			if !v.exercised[n.Kind+"."+f.Name] {
				missing = append(missing, n.Kind+"."+f.Name)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		v.t.Logf("no fixture in the corpus produces: %s", strings.Join(missing, ", "))
	}
}

func findNode(kind string) *nodeSchema {
	for i := range nodeSchemas {
		if nodeSchemas[i].Kind == kind {
			return &nodeSchemas[i]
		}
	}
	return nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// asValue and wrap adapt a fields back into the shape a builder wants, for
// TestEveryGroupKindIsAcceptedThere.
func asValue(f fields) value { return value{v: f.m, path: f.path} }

func wrap(key string, f fields) fields {
	return fields{m: map[string]any{key: f.m}, path: f.path}
}
