package check

import (
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
)

// TestClassify pins the simple/complex label, which is a *contract* rather
// than an opinion: it is carried in the tree's JSON encoding, so a UI renders
// three widgets or one raw field on the strength of it, and moving a case
// across the line changes what a form looks like.
//
// The four simple shapes are exactly the design document's list. Everything
// below the divider is there because someone will ask why it is not simple,
// and the answer should be written down next to the case.
func TestClassify(t *testing.T) {
	cases := []struct {
		expr string
		want Class
	}{
		// `<path> <op> <literal>`, over every operator the grammar has.
		{`status == 200`, Simple},
		{`status != 500`, Simple},
		{`body.data.count > 0`, Simple},
		{`body.data.count >= 1`, Simple},
		{`body.data.count < 100`, Simple},
		{`body.data.count <= 99`, Simple},
		{`body.data.roles contains "admin"`, Simple},
		{`body.data.email matches /.+@.+/`, Simple},

		// A path is a root with any chain of members and literal indexes.
		{`body.data.items[0].name == "first"`, Simple},
		{`headers["content-type"] contains "json"`, Simple},

		// An element function with literal arguments is a path: a picker whose
		// entries are the element functions with a selector field is exactly
		// the widget for a browser assertion.
		{`text("[role=status]") contains "Pro"`, Simple},
		{`attr("#link", "href") contains "/pro"`, Simple},
		{`count(".invoice") == 1`, Simple},

		// The two postfix predicates.
		{`body.data.token exists`, Simple},
		{`body.data.count is number`, Simple},

		// `not` in front of each of the three.
		{`not body.data.deleted exists`, Simple},
		{`not body.data.count is string`, Simple},
		{`not status == 500`, Simple},

		// A negative number is two tokens and one value, which a form's number
		// field holds.
		{`body.delta == -1`, Simple},

		// An unresolvable type name is still the simple shape. How a form
		// renders an expression and whether the expression is correct are
		// different questions, and the diagnostic answers the second.
		{`body.x is numbr`, Simple},

		// ---------------------------------------------------------------
		// Complex, and why.

		// An `and` chain is one assertion and is not three widgets.
		{`status == 200 and body.data.count > 0`, Complex},
		{`status == 200 or status == 201`, Complex},
		{`(status == 200 or status == 201) and body.ok`, Complex},
		{`not (status == 500)`, Complex},
		{`not not body.ok`, Complex},

		// A bare path with no operator. The design's list of simple shapes
		// does not include it, and a form that renders an operator dropdown
		// for an expression with no operator is worse than one that offers a
		// text field.
		{`visible(".invoice-preview")`, Complex},
		{`body.ok`, Complex},

		// An interpolated right-hand side is a small expression language, not
		// a literal, which is exactly what the complex rendering is for.
		{`body.data.url == "${base}/orders"`, Complex},

		// A literal on the left is not a path.
		{`200 == status`, Complex},

		// Two paths: there is no value field to put the second one in.
		{`body.a == body.b`, Complex},

		// A call whose argument is not a literal cannot be a selector widget.
		{`text(sel) contains "Pro"`, Complex},

		// A computed index is not a path a picker can walk.
		{`body.items[n] == 1`, Complex},

		// `-x` is arithmetic, not an assertion shape.
		{`-status`, Complex},
	}

	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			src := step("t", `get "/x"`, "expect "+tc.expr)
			tree, _ := parser.Parse("t.art", src)
			info, _ := Check(tree)

			var e *ast.Expect
			ast.Inspect(tree, func(n ast.Node) {
				if x, ok := n.(*ast.Expect); ok {
					e = x
				}
			})
			if e == nil {
				t.Fatalf("no expect parsed out of %q", src)
			}
			if got := info.Class(e); got != tc.want {
				t.Errorf("expect %s is %s, want %s", tc.expr, got, tc.want)
			}
		})
	}
}

// TestEveryExpectIsClassified is the property behind the label: it is a
// documented property of *the tree*, so every expect in a file has one, not
// just the ones a client thought to ask about.
func TestEveryExpectIsClassified(t *testing.T) {
	src := readFixture(t, "exprs.art")
	tree, _ := parser.Parse("exprs.art", src)
	info, _ := Check(tree)

	total, simple := 0, 0
	ast.Inspect(tree, func(n ast.Node) {
		e, ok := n.(*ast.Expect)
		if !ok {
			return
		}
		total++
		if _, classified := info.expects[e]; !classified {
			t.Errorf("the expect at %d:%d has no class", e.Span().Line, e.Span().Col)
		}
		if info.Class(e) == Simple {
			simple++
		}
	})

	if total == 0 {
		t.Fatal("exprs.art has no expects, so this test proves nothing")
	}
	// Not a pinned number -- that would fail every time the fixture gains a
	// line -- but the shape of the answer: a file of mostly ordinary
	// assertions is mostly simple, which is the claim that makes a form worth
	// building at all.
	if simple*2 <= total {
		t.Errorf("%d of %d expects in exprs.art are simple; a file of ordinary "+
			"assertions should be mostly simple, or the form is not worth building",
			simple, total)
	}
}
