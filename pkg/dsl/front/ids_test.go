package front_test

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
)

func withCode(u *front.Unit, code diag.Code) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range u.Bag.All() {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

func TestIDNotSnakeCase(t *testing.T) {
	for _, tc := range []struct {
		name, file, src, hint string
		line, col             int
	}{
		{"step", "f.art", "flow f {\n  step SignIn {\n    get \"/\"\n  }\n}\n", "write it sign_in", 2, 8},
		{"item call", "f.art", "import \"c.art\"\n\nflow f {\n  step LogIn = c.r {}\n}\n", "write it log_in", 4, 8},
		{"flow", "f.art", "flow CheckOut {\n  step a {\n    get \"/\"\n  }\n}\n", "write it check_out", 1, 6},
		{"item", "c.art", "collection c {\n  request getIt {\n    get \"/\"\n  }\n}\n", "write it get_it", 2, 11},
		{"collection", "c.art", "collection Orders {\n  request r {\n    get \"/\"\n  }\n}\n", "write it orders", 1, 12},
		{"scenario", "s.art", "flow \"f.art\"\n\nscenario Full_Price {}\n", "write it full_price", 3, 10},
	} {
		files := expand.MapLoader{
			"c.art": "collection c {\n  request r {\n    get \"/\"\n  }\n}\n",
			"f.art": "flow f {\n  step a {\n    get \"/\"\n  }\n}\n",
		}
		u := front.CompileWith(tc.file, tc.src, files)
		got := withCode(u, diag.IDNotSnakeCase)
		if len(got) != 1 {
			t.Errorf("%s: %d id-not-snake-case, want 1: %v", tc.name, len(got), u.Bag.All())
			continue
		}
		d := got[0]
		if d.Span.Line != tc.line || d.Span.Col != tc.col || !strings.Contains(d.Hint, tc.hint) {
			t.Errorf("%s: at %d:%d hint %q, want %d:%d and %q", tc.name, d.Span.Line, d.Span.Col, d.Hint, tc.line, tc.col, tc.hint)
		}
	}
}

func TestDuplicateStepIDAcrossBlocks(t *testing.T) {
	for name, src := range map[string]string{
		"setup and body": "flow f {\n  setup {\n    step a {\n      get \"/\"\n    }\n  }\n  step a {\n    get \"/\"\n  }\n}\n",
		"if branches":    "flow f {\n  var x = 1\n  if x == 1 {\n    step a {\n      get \"/\"\n    }\n  } else {\n    step a {\n      get \"/\"\n    }\n  }\n}\n",
	} {
		u := front.CompileWith("f.art", src, expand.MapLoader{})
		got := withCode(u, diag.DuplicateID)
		if len(got) != 1 {
			t.Errorf("%s: %d duplicate-id, want 1: %v", name, len(got), u.Bag.All())
			continue
		}
		if !strings.Contains(got[0].Message, "line 3") && !strings.Contains(got[0].Message, "line 4") {
			t.Errorf("%s: message %q does not name the first step's line", name, got[0].Message)
		}
	}
}

func TestDuplicateIDScopes(t *testing.T) {
	coll := "collection c {\n  request r {\n    get \"/\"\n  }\n  request r {\n    get \"/\"\n  }\n}\n"
	if got := withCode(front.CompileWith("c.art", coll, expand.MapLoader{}), diag.DuplicateID); len(got) != 1 {
		t.Errorf("two items named r: %d duplicate-id, want 1", len(got))
	}
	files := expand.MapLoader{"f.art": "flow f {\n  step a {\n    get \"/\"\n  }\n}\n"}
	scen := "flow \"f.art\"\n\nscenario s {}\nscenario s {}\n"
	if got := withCode(front.CompileWith("s.art", scen, files), diag.DuplicateID); len(got) != 1 {
		t.Errorf("two scenarios named s: %d duplicate-id, want 1", len(got))
	}
	// One step id in two flows is two scopes.
	files = expand.MapLoader{"f.art": "flow f {\n  step a {\n    get \"/\"\n  }\n}\n"}
	if got := withCode(front.CompileWith("s.art", "flow \"f.art\"\n\nscenario s {}\n", files), diag.DuplicateID); len(got) != 0 {
		t.Errorf("a step id in a flow file: %d duplicate-id, want 0", len(got))
	}
}

func TestQuotedNameIsOldForm(t *testing.T) {
	files := expand.MapLoader{
		"c.art": "collection c {\n  request r {\n    get \"/\"\n  }\n}\n",
		"f.art": "flow f {\n  step a {\n    get \"/\"\n  }\n}\n",
	}
	for _, tc := range []struct {
		name, file, src, msg string
		line, col            int
	}{
		{"step", "f.art", "flow f {\n  step \"log in\" {\n    get \"/\"\n  }\n}\n", `a step's name is a snake_case id now: step log_in as "log in"`, 2, 8},
		{"item call", "f.art", "import \"c.art\"\n\nflow f {\n  step \"log in\" = c.r {}\n}\n", `a step's name is a snake_case id now: step log_in as "log in"`, 4, 8},
		{"scenario", "s.art", "flow \"f.art\"\n\nscenario \"full price\" {}\n", `a scenario's name is a snake_case id now: scenario full_price as "full price"`, 3, 10},
		{"collection", "c.art", "collection \"Orders API\" {\n  request r {\n    get \"/\"\n  }\n}\n", `a collection's name is a snake_case id now: collection c as "Orders API"`, 1, 12},
	} {
		u := front.CompileWith(tc.file, tc.src, files)
		got := withCode(u, diag.QuotedName)
		if len(got) != 1 {
			t.Errorf("%s: %d quoted-name, want 1: %v", tc.name, len(got), u.Bag.All())
			continue
		}
		d := got[0]
		if d.Message != tc.msg || d.Span.Line != tc.line || d.Span.Col != tc.col ||
			!strings.Contains(d.Hint, "artemis migrate -f "+tc.file+" -o <dir> rewrites it") {
			t.Errorf("%s: %d:%d %q hint %q", tc.name, d.Span.Line, d.Span.Col, d.Message, d.Hint)
		}
	}
}

// A quoted name that already is an id needs no label.
func TestQuotedIDNeedsNoLabel(t *testing.T) {
	u := front.CompileWith("f.art", "flow f {\n  step \"pay\" {\n    get \"/\"\n  }\n}\n", expand.MapLoader{})
	got := withCode(u, diag.QuotedName)
	if len(got) != 1 || got[0].Message != "a step's name is a snake_case id now: step pay" {
		t.Errorf("got %v", got)
	}
}

// An id error does not hide what expansion and the checker find.
func TestAnIDErrorHidesNothingElse(t *testing.T) {
	u := front.CompileWith("f.art", "flow f {\n  step Foo {\n    get \"/\"\n  }\n\n  step b = nope.thing {}\n}\n", expand.MapLoader{})
	if len(withCode(u, diag.IDNotSnakeCase)) != 1 || len(withCode(u, diag.UnknownCollection)) != 1 {
		t.Fatalf("want id-not-snake-case and unknown-collection, got %v", u.Bag.All())
	}
}
