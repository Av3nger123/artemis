package print

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/parser"
)

// A block's comments survive both modes: above a clause, beside a brace,
// inside a body, between a closing brace and the clause after it, and after
// a repeat's until. Canonical mode keeps `} else {` on one line unless a
// comment sits between them, and then keeps the comment.
func TestBlockCommentsSurvive(t *testing.T) {
	src := `scenario "s" {
  # above the parallel
  parallel { # beside its brace
    # inside it
    step "a" {
      run "true"
    }
  }

  if 1 == 1 {
    step "b" {
      run "true"
    }
  }
  # between the brace and the else
  else {
    step "c" {
      run "true"
    }
  }

  try {
    step "d" {
      run "true"
    }
  } catch { # beside catch
    step "e" {
      run "true"
    }
  }

  repeat max 3 {
    step "f" {
      run "echo"
      capture out = stdout
    }
  } until out == "x" # after until
}
`
	tree, bag := parser.Parse("s.art", src)
	if bag.HasErrors() {
		t.Fatal(bag.All())
	}
	if got := Preserving(tree); got != src {
		t.Errorf("preserving mode changed the file:\n%s", got)
	}
	canon := Canonical(tree)
	for _, c := range []string{
		"# above the parallel", "# beside its brace", "# inside it",
		"# between the brace and the else", "# beside catch", "# after until",
	} {
		if !strings.Contains(canon, c) {
			t.Errorf("canonical mode dropped %q:\n%s", c, canon)
		}
	}
	again, bag := parser.Parse("s.art", canon)
	if bag.HasErrors() {
		t.Fatalf("canonical output does not parse: %v\n%s", bag.All(), canon)
	}
	if Canonical(again) != canon {
		t.Errorf("canonical mode is not stable:\n%s", Canonical(again))
	}
	if !strings.Contains(canon, "  } catch { # beside catch\n") {
		t.Errorf("catch did not join its try's brace:\n%s", canon)
	}
}
