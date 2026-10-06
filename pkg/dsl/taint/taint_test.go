package taint_test

import (
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/taint"
)

// secrets is the set every table below is judged against.
func secrets() map[string]bool { return map[string]bool{"pw": true, "token": true} }

func TestSecret(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		// A bare name, and a name that is not in the set.
		{`pw`, true},
		{`url`, false},

		// Interpolation, which is the case the modifier exists for.
		{`"Bearer ${token}"`, true},
		{`"Bearer ${id}"`, false},
		{`"${url}/orders?t=${pw}"`, true},

		// A call reads its arguments and not its callee.
		{`match(pw, /x(.)/)`, true},
		{`match(raw, /x(.)/)`, false},
		{`env("API_URL")`, false},

		// A member name is not a binding.
		{`body.pw`, false},
		{`body.data.token`, false},
		{`pw.anything`, true},

		// An index reads both sides.
		{`body.items[0]`, false},
		{`body.items[pw]`, true},
		{`headers["x"]`, false},

		// Composite literals, by field and by element.
		{`{"user": "alice", "pass": pw}`, true},
		{`{"user": "alice"}`, false},
		{`{"deep": {"k": pw}}`, true},
		{`[1, pw]`, true},
		{`[1, 2]`, false},

		// Operators.
		{`not pw`, true},
		{`not url`, false},
		{`pw == "x"`, true},
		{`"x" == pw`, true},
		{`url == "x"`, false},
		{`(pw)`, true},
		{`pw exists`, true},
		{`pw is string`, true},
		{`url is string`, false},

		// A plain literal.
		{`"x"`, false},
		{`200`, false},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			e := mustParseExpr(t, tc.expr)
			if got := taint.Secret(e, secrets()); got != tc.want {
				t.Errorf("Secret(%s) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// A binding may be named `env`, and a call to env() must not become secret
// because of it: a callee is a function name, not a reference to a binding.
func TestSecretIgnoresCallee(t *testing.T) {
	e := mustParseExpr(t, `env("API_URL")`)
	if taint.Secret(e, map[string]bool{"env": true}) {
		t.Error("a callee must not count as a binding reference")
	}
}

func TestSecretNilAndEmpty(t *testing.T) {
	if taint.Secret(nil, secrets()) {
		t.Error("Secret(nil) = true, want false")
	}
	if taint.Secret(mustParseExpr(t, `pw`), nil) {
		t.Error("Secret with no secrets = true, want false")
	}
}

// Every expression node that can hold a subexpression must appear in Secret's
// switch. A node that falls through returns false, which is a leak rather than
// a compile error, so this is the test that makes a new node fail loudly.
func TestSecretCoversEveryExprNode(t *testing.T) {
	// One source per node kind, each of which must come back secret.
	for _, src := range []string{
		`pw`,           // Ident
		`"a${pw}b"`,    // Interp, Segment
		`not pw`,       // Unary
		`pw == 1`,      // Binary
		`pw exists`,    // Exists
		`pw is string`, // IsType
		`pw.x`,         // Member
		`body[pw]`,     // Index
		`env(pw)`,      // Call, Arg
		`{"k": pw}`,    // Object, Entry
		`[pw]`,         // Array, Elem
		`(pw)`,         // Paren
	} {
		if !taint.Secret(mustParseExpr(t, src), map[string]bool{"pw": true}) {
			t.Errorf("Secret(%s) = false: the node is missing from the switch", src)
		}
	}
}

// mustParseExpr parses one expression and fails on any diagnostic, so a typo in
// a table row is a test failure and not a silent false.
func mustParseExpr(t *testing.T, src string) ast.Expr {
	t.Helper()
	e, bag := parser.ParseExpr("t.art", src)
	if bag != nil && len(bag.All()) != 0 {
		t.Fatalf("parsing %q: %v", src, bag.All())
	}
	return e
}
