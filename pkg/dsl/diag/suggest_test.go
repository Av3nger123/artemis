package diag

import "testing"

func TestNearest(t *testing.T) {
	scope := []string{"status", "body", "raw", "headers", "exit_code"}
	cases := []struct {
		name, word, want string
		ok               bool
	}{
		{"one deletion", "statu", "status", true},
		{"one transposition", "stauts", "status", true},
		{"one insertion", "statuss", "status", true},
		{"case only", "Status", "status", true},
		{"case only, long", "EXIT_CODE", "exit_code", true},
		// Two edits in a six-letter word is the limit, and reached.
		{"two edits", "staus", "status", true},
		// Nothing close: a guess here would be worse than saying nothing,
		// because a wrong "did you mean" sends the reader after the wrong fix.
		{"unrelated", "elephant", "", false},
		{"too short to guess", "abc", "", false},
		{"empty", "", "", false},
		// An exact match is not a near miss. The caller is reporting that the
		// name is wrong for some other reason, and "did you mean status?"
		// about `status` reads as a bug.
		{"exact match is not a suggestion", "status", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Nearest(c.word, scope)
			if got != c.want || ok != c.ok {
				t.Errorf("Nearest(%q) = %q, %v; want %q, %v", c.word, got, ok, c.want, c.ok)
			}
		})
	}
}

// TestNearestRespectsCandidateOrder matters because the checker passes the
// names in scope in a meaningful order -- a step's own bindings before the
// scenario's -- and an equal-distance tie should resolve to the nearer scope.
func TestNearestRespectsCandidateOrder(t *testing.T) {
	got, ok := Nearest("tokan", []string{"token", "tokens"})
	if !ok || got != "token" {
		t.Errorf("Nearest = %q, %v; want the first of an equal-distance tie", got, ok)
	}
}

func TestDidYouMeanAttachesHintAndFix(t *testing.T) {
	b := New()
	ref := b.Error(span("x.art", 12, 10, 12, 15, 0), UnknownField, "unknown field %q", "statu")
	if !ref.DidYouMean("statu", []string{"status", "body"}) {
		t.Fatal("DidYouMean found nothing for a one-edit miss")
	}
	d := b.All()[0]
	if d.Hint != `did you mean "status"?` {
		t.Errorf("hint is %q", d.Hint)
	}
	if len(d.Suggestions) != 1 || d.Suggestions[0].Replace != "status" {
		t.Errorf("suggestions are %+v, want one replacing with status", d.Suggestions)
	}
}

func TestDidYouMeanLeavesNothingWhenNothingIsClose(t *testing.T) {
	b := New()
	ref := b.Error(span("x.art", 1, 1, 1, 9, 0), UnknownField, "unknown field %q", "elephant")
	if ref.DidYouMean("elephant", []string{"status", "body"}) {
		t.Fatal("DidYouMean guessed")
	}
	if d := b.All()[0]; d.Hint != "" || len(d.Suggestions) != 0 {
		t.Errorf("a failed guess still wrote a hint or a suggestion: %+v", d)
	}
}

func TestDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"status", "status", 0},
		{"", "status", 6},
		{"status", "", 6},
		{"statu", "status", 1},
		{"stauts", "status", 2},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := distance(c.a, c.b); got != c.want {
			t.Errorf("distance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
