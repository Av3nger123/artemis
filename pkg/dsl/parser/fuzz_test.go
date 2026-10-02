package parser

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
)

// FuzzParse asserts the four things that have to hold for arbitrary bytes
// rather than for the inputs someone thought to write down. The first is the
// issue's acceptance criterion and the rest are what make it meaningful:
//
//  1. Nothing panics. A malformed scenario file produces diagnostics and a
//     non-zero exit, never a stack trace -- the value ART-6 and ART-7
//     established, continued into the front end.
//  2. Nothing hangs. A recursive-descent parser's other failure mode is a loop
//     that makes no progress, and a hang in CI is worse than a crash because
//     nothing says what happened. Every loop here is guarded; this is what
//     holds them to it.
//  3. The round trip is exact, so no input exists that the parser silently
//     loses a byte of.
//  4. Every diagnostic is renderable: a registered code, and a span inside the
//     file. A diagnostic whose span ran past the end of the source would make
//     the terminal renderer slice out of range, which would turn the thing
//     that reports the user's mistake into a crash of its own.
//
// The lexer has its own target in pkg/dsl/lexer; this one starts where that
// one leaves off.
func FuzzParse(f *testing.F) {
	for _, src := range seeds {
		f.Add(src)
	}

	f.Fuzz(func(t *testing.T, src string) {
		tree, bag := Parse("fuzz.art", src)

		if tree == nil {
			t.Fatalf("nil tree for %q", src)
		}
		if got := ast.Source(tree); got != src {
			t.Fatalf("round trip lost bytes:\n got %q\nwant %q", got, src)
		}

		for _, d := range bag.All() {
			if !diag.Registered(d.Code) {
				t.Fatalf("diagnostic code %q is not registered: %q", d.Code, src)
			}
			if d.Message == "" {
				t.Fatalf("diagnostic %q has no message: %q", d.Code, src)
			}
			if d.Span.Offset < 0 || d.Span.Offset > len(src) {
				t.Fatalf("span offset %d outside a %d-byte file: %q", d.Span.Offset, len(src), src)
			}
			if d.Span.Line < 1 || d.Span.Col < 1 {
				t.Fatalf("span %+v is not 1-based: %q", d.Span, src)
			}
			if d.Span.EndLine < d.Span.Line {
				t.Fatalf("span %+v ends before it starts: %q", d.Span, src)
			}
		}

		// Walking the tree is what every stage after this one does, so a node
		// the walk cannot reach or a child list that lies is a fault worth
		// finding here rather than in the checker.
		ast.Inspect(tree, func(n ast.Node) {
			if n.Span().IsZero() && ast.Source(n) != "" {
				t.Fatalf("node %T covers %q but has no span: %q", n, ast.Source(n), src)
			}
		})
	})
}

// FuzzParseExpr fuzzes the expression entry point directly, because a UI
// validating one field live calls it with whatever the user has typed so far
// -- which is the most hostile input in the system, arriving on every
// keystroke.
func FuzzParseExpr(f *testing.F) {
	for _, src := range exprSeeds {
		f.Add(src)
	}

	f.Fuzz(func(t *testing.T, src string) {
		x, bag := ParseExpr("field.art", src)
		if got := ast.Source(x); got != "" && !strings.HasPrefix(src, got) {
			t.Fatalf("expression source %q is not a prefix of %q", got, src)
		}
		for _, d := range bag.All() {
			if !diag.Registered(d.Code) {
				t.Fatalf("unregistered code %q for %q", d.Code, src)
			}
		}
	})
}

// seeds are the whole-file seeds: every production, the fixtures, and the
// shapes that have historically broken a parser -- unbalanced brackets at
// every depth, a statement cut off mid-expression, nesting deep enough to
// matter.
var seeds = append(fileSeeds(), expandSeeds()...)

func fileSeeds() []string {
	return []string{
		"",
		"\n",
		"#",
		"# comment\n",
		"scenario",
		`scenario "s"`,
		`scenario "s" {`,
		`scenario "s" {}`,
		"scenario \"s\" {\n  var a = 1\n}\n",
		"scenario \"s\" {\n  config browser { headless = true }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    get \"/x\"\n  }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    run \"x\" { args = [1] }\n  }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    browser { goto \"/\" }\n  }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    get \"/x\"\n    expect not a exists\n  }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    get \"/x\"\n    retry { times = 1, delay = \"1s\" }\n    timeout = \"1s\"\n  }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    get \"/x\"\n    capture k = body.a[0].b\n    expect k is number within \"1s\"\n  }\n}\n",

		// Unbalanced at every depth the grammar has.
		"scenario \"s\" { step \"t\" { get \"/x\" { body = { \"a\": [ ( 1",
		"}}}}]]]])))",
		"{{{{[[[[((((",
		`scenario "s" { step "t" { browser { fill`,

		// Cut off mid-expression, which is how a parser loses a closer.
		"scenario \"s\" {\n  var a = \n}\n",
		"scenario \"s\" {\n  var a = 1 ==\n}\n",
		"scenario \"s\" {\n  var a = \"${\n}\n",
		"scenario \"s\" {\n  var a = \"${b\n}\n",
		"scenario \"s\" {\n  var a = /unterminated\n}\n",
		"scenario \"s\" {\n  var a = \"unterminated\n}\n",

		// The shape faults this issue reports.
		"scenario \"s\" {\n  step \"t\" {\n  }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    run \"a\"\n    run \"b\"\n  }\n}\n",
		"scenario \"s\" {\n  step \"t\" {\n    expect a\n    run \"a\"\n  }\n}\n",
		"scenario \"s\" {\n  var a = 1 var b = 2\n}\n",
		"scenario \"s\" {\n  var a = b == c == d\n}\n",

		// Non-ASCII and control bytes, which spans count in bytes.
		"scenario \"é\" {\n  var a = \"日本\"\n}\n",
		"scenario \"s\" {\r\n\tvar a = 1\r\n}\r\n",
		"\ufeffscenario \"s\" {}", // a byte-order mark, which the lexer keeps as whitespace
		"scenario \"s\" { var a = \x00 }",
	}
}

// expandSeeds adds every expression seed wrapped in a step, so the expression
// grammar is fuzzed in the position it is actually written in as well as on
// its own.
func expandSeeds() []string {
	out := make([]string, 0, len(exprSeeds))
	for _, e := range exprSeeds {
		out = append(out, "scenario \"s\" {\n  step \"t\" {\n    get \"/x\"\n    expect "+e+"\n  }\n}\n")
	}
	return out
}

// exprSeeds is one of every expression form, plus the half-typed states a UI
// field passes through on the way to each.
var exprSeeds = []string{
	"a",
	"1",
	"-1",
	"--1",
	"true",
	"null",
	`"s"`,
	"/r/",
	"a.b",
	"a.b.c",
	"a[0]",
	"a[0][1]",
	`a["k"]`,
	"f()",
	"f(1)",
	"f(1, 2)",
	"f(1,)",
	"{}",
	`{"k": 1}`,
	`{"k": 1,}`,
	`{"k": {"j": [1]}}`,
	"[]",
	"[1]",
	"[1,]",
	"[[[[1]]]]",
	"(a)",
	"((a))",
	"a == 1",
	"a != 1",
	"a < 1",
	"a <= 1",
	"a > 1",
	"a >= 1",
	`a contains "x"`,
	"a matches /x/",
	"a exists",
	"a is number",
	"a is null",
	"not a",
	"not not a",
	"not a exists",
	"not a is number",
	"a and b",
	"a or b",
	"a or b and c",
	"not a and b",
	`"${a}"`,
	`"${a}b${c}"`,
	`"${f(1)}"`,
	`"${"${a}"}"`,

	// Half-typed, which is the common case for a live-validated field.
	"",
	"a.",
	"a[",
	"a[0",
	"f(",
	"f(1,",
	"{",
	`{"k"`,
	`{"k":`,
	"[",
	"[1,",
	"(",
	"a ==",
	"not",
	"a is",
	"a and",
	`"${`,
	`"${a`,
	"a exists exists",
	"== a",
	"and a",
}
