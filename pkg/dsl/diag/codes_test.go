package diag

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/lexer"
	"artemis/pkg/dsl/token"
)

// TestCodeListGolden pins the whole registry to testdata/codes.golden.
//
// This is what makes the stability invariant enforceable rather than aspired
// to. Rewording a message touches nothing here; renaming or removing a code
// shows up as a diff someone has to look at and approve, which is the only
// protection a client keying behaviour off a code has.
func TestCodeListGolden(t *testing.T) {
	var b strings.Builder
	for _, c := range Codes() {
		b.WriteString(string(c.Code) + "\t" + c.Severity.String() + "\t" + c.Description + "\n")
	}
	checkGolden(t, "codes.golden", b.String())
}

// TestLexerCodesAreRegistered is the other half of the FromToken bridge: the
// lexer sets codes as plain strings so pkg/dsl/token depends on nothing, so
// nothing but a test stops the two lists drifting.
func TestLexerCodesAreRegistered(t *testing.T) {
	// Each source provokes one lexical fault. Several are kept apart because a
	// construct that runs to the end of the file swallows anything after it.
	sources := []string{
		"scenario \"x\" {\n  header \"A\" = \"bad: \\q\"\n}\n",
		"scenario \"x\" {\n  header \"B\" = /unterminated\n}\n",
		"scenario \"x\" {\n  header \"C\" = 1 ! 2\n}\n",
		"scenario \"x\" {\n  header \"D\" = \"no closing quote\n}\n",
		// No closing brace: a "}" after an unclosed "${" is read as the
		// interpolation's own, and the fault becomes an unterminated string.
		"scenario \"x\" {\n  get \"${ unclosed\n",
	}
	seen := map[Code]bool{}
	for _, src := range sources {
		for _, tk := range lexer.Lex("x.art", src) {
			if tk.Kind != token.Invalid || tk.Code == "" {
				continue
			}
			c := Code(tk.Code)
			seen[c] = true
			if !Registered(c) {
				t.Errorf("the lexer emits %q, which is not in the registry in codes.go", c)
			}
		}
	}
	for _, want := range []Code{
		UnterminatedString, UnterminatedRegex, UnclosedInterpolation,
		InvalidEscape, UnexpectedCharacter,
	} {
		if !seen[want] {
			t.Errorf("no fixture provokes %q, so nothing checks it is still the lexer's spelling", want)
		}
	}
}

// TestEveryCodeIsUsable checks the registry's own shape: a code with no
// description or a duplicate entry is a mistake in the table itself.
func TestEveryCodeIsUsable(t *testing.T) {
	seen := map[Code]bool{}
	for _, c := range registry {
		switch {
		case c.Code == "":
			t.Error("a registry entry has no code")
		case c.Description == "":
			t.Errorf("%q has no description, so nothing documents it", c.Code)
		case seen[c.Code]:
			t.Errorf("%q is registered twice", c.Code)
		case strings.ToLower(string(c.Code)) != string(c.Code):
			t.Errorf("%q is not kebab-case", c.Code)
		case strings.ContainsAny(string(c.Code), " _"):
			t.Errorf("%q is not kebab-case", c.Code)
		}
		seen[c.Code] = true
	}
}

func TestLookup(t *testing.T) {
	info, ok := Lookup(UnknownField)
	if !ok || info.Code != UnknownField || info.Severity != Error {
		t.Errorf("Lookup(unknown-field) = %+v, %v", info, ok)
	}
	if _, ok := Lookup("no-such-code"); ok {
		t.Error("Lookup invented an entry for an unregistered code")
	}
}

// TestCodesIsACopy guards the one place a caller could corrupt the registry:
// `artemis grammar --json` iterates Codes(), and a slice that aliased the
// registry would let a client mutate every later caller's view of it.
func TestCodesIsACopy(t *testing.T) {
	got := Codes()
	got[0].Description = "overwritten"
	if Codes()[0].Description == "overwritten" {
		t.Error("Codes returns the registry itself")
	}
}
