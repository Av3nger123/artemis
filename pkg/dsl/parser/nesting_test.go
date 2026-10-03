package parser

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
)

// Nesting is the one input that defeats "nothing panics" without panicking.
//
// Recursive descent costs a stack frame per level, and when Go's stack hits
// its limit the process dies with "fatal error: stack overflow" -- which is
// not a panic, so no recover() catches it, no diagnostic is printed and the
// exit looks like a crash because it is one. A file of 100,000 open
// parentheses did exactly that until maxNesting existed.
//
// The fuzz target did not find it, and would not have: its inputs are small.
// This is what a limit is for and what tests it.

func TestDeepNestingIsReportedNotFatal(t *testing.T) {
	deep := maxNesting * 50

	cases := []struct{ name, src string }{
		{"parentheses", wrap(strings.Repeat("(", deep) + "1" + strings.Repeat(")", deep))},
		{"arrays", wrap(strings.Repeat("[", deep) + "1" + strings.Repeat("]", deep))},
		{"objects", wrap(strings.Repeat(`{"k":`, deep) + "1" + strings.Repeat("}", deep))},
		{"calls", wrap(strings.Repeat("f(", deep) + "1" + strings.Repeat(")", deep))},
		{"indexes", wrap("a" + strings.Repeat("[", deep) + "1" + strings.Repeat("]", deep))},
		{"not", wrap(strings.Repeat("not ", deep) + "true")},
		{"minus", wrap(strings.Repeat("-", deep) + "1")},
		{"unclosed parentheses", wrap(strings.Repeat("(", deep))},
		{"unclosed arrays", wrap(strings.Repeat("[", deep))},
		{"unclosed blocks", "scenario \"s\" {\n  step \"t\" {\n    get \"/x\" " + strings.Repeat("{ a ", deep)},
		{"nested field blocks", "scenario \"s\" {\n  step \"t\" {\n    get \"/x\" " + strings.Repeat("{ a ", deep) + strings.Repeat("}", deep) + "\n  }\n}\n"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, bag := Parse("deep.art", c.src)

			// Reaching here at all is most of the test: the previous
			// behaviour was that the process did not.
			if !has(codes(bag), diag.NestingTooDeep) {
				t.Errorf("codes = %v, want nesting-too-deep", dedup(codes(bag)))
			}
			// Still lossless, which is the property a bail-out is most likely
			// to break.
			assertRoundTrip(t, c.src, f)
		})
	}
}

// TestNestingLimitIsReportedOnce: a file too deep is too deep at every level
// below the limit, and 200 copies of one diagnostic is not 200 pieces of
// information.
func TestNestingLimitIsReportedOnce(t *testing.T) {
	src := wrap(strings.Repeat("(", maxNesting*10) + "1")
	_, bag := Parse("deep.art", src)

	if n := count(codes(bag), diag.NestingTooDeep); n != 1 {
		t.Errorf("nesting-too-deep reported %d times, want 1", n)
	}
	// Everything else collapses too: the unwinding frames each report the same
	// missing ")" at the same position, and diag.Bag's dedup is what keeps
	// that from reaching the terminal as a wall of text.
	if n := len(bag.All()); n > 5 {
		t.Errorf("%d diagnostics survive dedup, want a handful", n)
		for _, d := range bag.All() {
			t.Logf("  %d:%d [%s] %s", d.Span.Line, d.Span.Col, d.Code, d.Message)
		}
	}
}

// TestOrdinaryNestingIsUnaffected: the limit must be far past anything real.
// The design's worked example nests four deep.
func TestOrdinaryNestingIsUnaffected(t *testing.T) {
	src := wrap(strings.Repeat("[", 20) + "1" + strings.Repeat("]", 20))
	f, bag := Parse("fine.art", src)
	assertNoDiagnostics(t, src, bag)
	assertRoundTrip(t, src, f)
}

// wrap puts an expression in the smallest valid file that holds one.
func wrap(expr string) string {
	return "scenario \"s\" {\n  var a = " + expr + "\n}\n"
}

func dedup[T comparable](in []T) []T {
	seen := map[T]bool{}
	out := in[:0]
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
