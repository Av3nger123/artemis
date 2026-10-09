package diag

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/lexer"
	"artemis/pkg/dsl/token"
)

// TestBagReportsEverything is the second invariant: collecting many
// diagnostics is the normal case. A bag that stopped at the first, or that
// collapsed two different faults in one file, would make the DSL a decoder
// again.
func TestBagReportsEverything(t *testing.T) {
	b := New()
	for i := 1; i <= 20; i++ {
		b.Error(span("x.art", i, 1, i, 2, i*10), UnknownField, "fault %d", i)
	}
	if got := len(b.All()); got != 20 {
		t.Fatalf("reported %d of 20 diagnostics", got)
	}
}

func TestBagOrdersByFileThenOffset(t *testing.T) {
	b := New()
	b.Error(span("b.art", 1, 1, 1, 2, 5), UnknownField, "second file")
	b.Error(span("a.art", 9, 1, 9, 2, 90), UnknownField, "later in the first file")
	b.Error(span("a.art", 2, 1, 2, 2, 20), UnknownField, "earlier in the first file")

	var got []string
	for _, d := range b.All() {
		got = append(got, d.Message)
	}
	want := []string{"earlier in the first file", "later in the first file", "second file"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("order is %v, want %v", got, want)
	}
}

// TestBagDropsExactDuplicates covers what parser recovery does: two paths
// reach the same token and report the same thing. A reader should see it once.
func TestBagDropsExactDuplicates(t *testing.T) {
	s := span("x.art", 3, 5, 3, 10, 40)
	b := New()
	b.Error(s, UnknownField, "unknown field %q", "statu").DidYouMean("statu", []string{"status"})
	b.Error(s, UnknownField, "unknown field %q", "statu").DidYouMean("statu", []string{"status"})
	if got := len(b.All()); got != 1 {
		t.Fatalf("kept %d copies of one diagnostic", got)
	}
}

// TestBagKeepsTwoFaultsAtOneSpan is the other half of dedup: a token can be
// wrong in more than one way, and only an exact match is a duplicate.
func TestBagKeepsTwoFaultsAtOneSpan(t *testing.T) {
	s := span("x.art", 3, 5, 3, 10, 40)
	b := New()
	b.Error(s, UnknownField, "unknown field %q", "statu")
	b.Error(s, NotInScope, "%q is not in scope in a browser step", "statu")
	b.Error(s, UnknownField, "unknown field %q", "statu").Hintf("did you mean %q?", "status")
	if got := len(b.All()); got != 3 {
		t.Fatalf("kept %d of 3 distinct diagnostics", got)
	}
}

// TestBagKeepsTwoUsesOfOneLine: a flow used twice expands its inner use line
// twice, so two diagnostics can share a span and a message and differ only in
// which expansion of that use line brought them in -- a difference in Via
// alone. They are two faults, and both are kept.
func TestBagKeepsTwoUsesOfOneLine(t *testing.T) {
	s := span("c.art", 3, 5, 3, 10, 40)
	use := span("c.art", 9, 3, 9, 10, 90)
	other := use
	other.Via = 2
	b := New()
	b.Add(Diagnostic{Span: s, Code: UnknownField, Message: "m", UsedFrom: []token.Span{use}})
	b.Add(Diagnostic{Span: s, Code: UnknownField, Message: "m", UsedFrom: []token.Span{other}})
	b.Add(Diagnostic{Span: s, Code: UnknownField, Message: "m", UsedFrom: []token.Span{other}})
	b.Add(Diagnostic{Span: s, Code: UnknownField, Message: "m", UsedFrom: []token.Span{other, use}})
	if got := len(b.All()); got != 3 {
		t.Fatalf("kept %d of 3 distinct diagnostics", got)
	}
}

func TestHasErrors(t *testing.T) {
	b := New()
	if b.HasErrors() {
		t.Error("an empty bag has errors")
	}
	b.Warn(span("x.art", 1, 1, 1, 2, 0), NotInScope, "only a warning")
	if b.HasErrors() {
		t.Error("a warning counted as an error, which would fail a run that passed")
	}
	b.Error(span("x.art", 2, 1, 2, 2, 10), UnknownField, "an error")
	if !b.HasErrors() {
		t.Error("an error did not count")
	}
}

func TestMergeKeepsBoth(t *testing.T) {
	a, c := New(), New()
	a.Error(span("x.art", 1, 1, 1, 2, 0), UnknownField, "from a")
	c.Error(span("x.art", 2, 1, 2, 2, 10), UnknownField, "from c")
	a.Merge(c)
	a.Merge(nil)
	if got := len(a.All()); got != 2 {
		t.Fatalf("merge produced %d diagnostics, want 2", got)
	}
}

// TestFromToken covers the bridge pkg/dsl/token deliberately left open, in
// both shapes an Invalid token comes in.
func TestFromToken(t *testing.T) {
	marker := token.Token{
		Kind: token.Invalid, Code: "invalid-escape", Message: `unknown escape "\q"`,
		Span: span("x.art", 1, 5, 1, 7, 4),
	}
	d, ok := FromToken(marker)
	if !ok {
		t.Fatal("a marker token produced no diagnostic")
	}
	if d.Code != InvalidEscape || d.Severity != Error || d.Message != `unknown escape "\q"` ||
		d.Span != marker.Span {
		t.Errorf("diagnostic does not match the token: %+v", d)
	}

	for _, tk := range []token.Token{
		{Kind: token.Ident, Text: "status"},
		{Kind: token.Invalid, Text: "!"}, // invalid, but with nothing to say
	} {
		if _, ok := FromToken(tk); ok {
			t.Errorf("%v produced a diagnostic", tk.Kind)
		}
	}
}

// TestAddTokensReportsEveryLexicalFault pins the lexer and the bag together:
// one pass over a file with several faults in it yields one diagnostic each.
func TestAddTokensReportsEveryLexicalFault(t *testing.T) {
	src := "scenario \"x\" {\n  header \"A\" = \"bad: \\q\"\n  header \"B\" = 1 ! 2\n}\n"
	b := New()
	if n := b.AddTokens(lexer.Lex("x.art", src)); n != 2 {
		t.Fatalf("reported %d faults, want 2", n)
	}
	codes := []Code{b.All()[0].Code, b.All()[1].Code}
	if codes[0] != InvalidEscape || codes[1] != UnexpectedCharacter {
		t.Errorf("codes are %v, want [invalid-escape unexpected-character]", codes)
	}
}

func TestSeverityNames(t *testing.T) {
	if Error.String() != "error" || Warning.String() != "warning" {
		t.Errorf("severity names changed: %q %q", Error, Warning)
	}
	if got := Severity(9).String(); got != "severity(9)" {
		t.Errorf("an unknown severity renders as %q", got)
	}
}

// TestSeveritiesCoversSeverityNames fails if a severity is added without
// reaching the enumerated set, which is what `artemis grammar --json` ships.
func TestSeveritiesCoversSeverityNames(t *testing.T) {
	got := Severities()
	if len(got) != len(severityNames) {
		t.Fatalf("Severities has %d entries, severityNames has %d", len(got), len(severityNames))
	}
	seen := map[Severity]bool{}
	for _, s := range got {
		if _, named := severityNames[s]; !named {
			t.Errorf("Severities has %d, which has no name", s)
		}
		if seen[s] {
			t.Errorf("Severities lists %s twice", s)
		}
		seen[s] = true
	}
}
