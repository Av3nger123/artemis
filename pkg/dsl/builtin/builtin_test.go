package builtin

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/token"
)

// Every entry is one a call can satisfy and a diagnostic can describe: a name
// that appears once, bounds that make a range, parameters that say what they
// take, and a signature that is a call of this builtin.
//
// The bounds are the part the old per-package tables got wrong without a test
// noticing: a minimum of 3 for env() would have made every env() call an arity
// error, and a minimum above the maximum is a builtin nothing can call.
func TestEveryEntryIsCoherent(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range table {
		if f.Name == "" {
			t.Fatalf("an entry has no name: %+v", f)
		}
		if seen[f.Name] {
			t.Errorf("%s() is in the table twice", f.Name)
		}
		seen[f.Name] = true
		if f.Min < 0 || f.Min > f.Max() {
			t.Errorf("%s(): accepts %d to %d arguments, which is not a range a call can satisfy",
				f.Name, f.Min, f.Max())
		}
		for i, p := range f.Params {
			if p.Want == "" || len(p.Kinds) == 0 {
				t.Errorf("%s()'s parameter %d says nothing about what it takes", f.Name, i)
			}
			if len(p.OneOf) > 0 && p.Type != TypeString {
				t.Errorf("%s()'s parameter %d lists the strings it accepts and is not a string", f.Name, i)
			}
			if p.Type == TypeBytes && !p.Accepts(token.String) {
				t.Errorf("%s()'s parameter %d takes text and does not accept a string literal", f.Name, i)
			}
			if p.Whole && p.Type != TypeNumber {
				t.Errorf("%s()'s parameter %d is a whole number and is not a number", f.Name, i)
			}
		}
		if !strings.HasPrefix(f.Signature, f.Name+"(") {
			t.Errorf("%s()'s signature %q is not a call of it", f.Name, f.Signature)
		}
	}
}

// The lookups are built from the table, so each answers for exactly the names
// it holds -- and for nothing else.
func TestLookupsAgreeWithTheTable(t *testing.T) {
	for _, name := range Names() {
		f, ok := Lookup(name)
		if !ok || f.Name != name || !Is(name) {
			t.Errorf("%s() is in the table and a lookup does not find it", name)
		}
		low, high, ok := Arity(name)
		if !ok || low != f.Min || high != f.Max() {
			t.Errorf("Arity(%q) = %d, %d, %v; the table says %d to %d", name, low, high, ok, f.Min, f.Max())
		}
	}
	for _, name := range []string{"", "Env", "length", "body"} {
		if Is(name) {
			t.Errorf("Is(%q) is true; it is not a builtin", name)
		}
		if _, _, ok := Arity(name); ok {
			t.Errorf("Arity(%q) knows a name that is not a builtin", name)
		}
	}
}

// Every builtin is callable somewhere, so the two scopes together are the
// whole table, each name exactly once.
func TestScopesPartitionTheTable(t *testing.T) {
	got := append(In(Everywhere), In(Page)...)
	if len(got) != len(table) {
		t.Fatalf("the scopes hold %d names and the table %d", len(got), len(table))
	}
	in := map[string]bool{}
	for _, name := range got {
		in[name] = true
	}
	for _, name := range Names() {
		if !in[name] {
			t.Errorf("%s() is callable in no scope", name)
		}
	}
}

// env(), match() and the value builtins are callable everywhere, which is what
// SPEC.md's builtins section says; the element functions are browser-only.
func TestTheScopesAreTheSpecs(t *testing.T) {
	for _, name := range []string{"env", "match", "uuid", "now", "len"} {
		if f, _ := Lookup(name); f.Scope != Everywhere {
			t.Errorf("%s() is not callable everywhere", name)
		}
	}
	if got := strings.Join(In(Page), ","); got != "text,value,attr,count,visible" {
		t.Errorf("callable in a browser step: %s, want text,value,attr,count,visible", got)
	}
}

// A builtin's name cannot be reserved for a later tier: the checker rejects a
// reserved word wherever a function name is expected, so a builtin with one
// could never be called.
func TestNoBuiltinIsReserved(t *testing.T) {
	for _, name := range Names() {
		if token.IsReserved(name) {
			t.Errorf("%s() is a builtin and a reserved word", name)
		}
	}
}

// Random names exactly the builtins that draw from the generator, each of which
// is in the table: a seed is reported for a run that called one, so a name here
// that no call can reach would make the seed line unreachable too.
func TestRandomNamesRealBuiltins(t *testing.T) {
	n := 0
	for _, name := range Names() {
		if Random(name) {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d builtins draw from the generator, want uuid, random_int and random_string", n)
	}
}
