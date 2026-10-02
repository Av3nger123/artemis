package assert

import (
	"strings"
	"testing"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

func textCheck(op, value string) models.TextCheck {
	return models.TextCheck{Operator: op, Value: value}
}

func TestTextOperators(t *testing.T) {
	for _, tc := range []struct {
		name  string
		op    string
		value string
		text  string
		want  result.Status
	}{
		{"contains hit", "contains", "COPY 42", "ok\nCOPY 42\n", result.StatusPass},
		{"contains miss", "contains", "COPY 42", "ok\nCOPY 41\n", result.StatusFail},
		{"an absent operator is contains", "", "COPY", "COPY 42\n", result.StatusPass},
		{"equals hit", "equals", "exact", "exact", result.StatusPass},
		{"equals is exact, newline and all", "equals", "exact", "exact\n", result.StatusFail},
		{"matches hit", "matches", "^COPY [0-9]+$", "COPY 42", result.StatusPass},
		{"matches is unanchored across lines", "matches", "COPY", "a\nCOPY 42\n", result.StatusPass},
		{"matches miss", "matches", "^nope$", "COPY 42", result.StatusFail},
		{"empty on a quiet stream", "empty", "", "", result.StatusPass},
		{"empty ignores whitespace", "empty", "", " \n\t\n", result.StatusPass},
		{"empty on a loud stream", "empty", "", "warning: x", result.StatusFail},
		{"an unknown operator cannot be checked", "gt", "3", "4", result.StatusError},
		{"an uncompilable regex cannot be checked", "matches", "([", "x", result.StatusError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Text("step", "stdout", textCheck(tc.op, tc.value), tc.text)
			if got.Status != tc.want {
				t.Errorf("Text(...) status = %v, want %v (%s)", got.Status, tc.want, got.Describe())
			}
			if got.Kind != "stdout" {
				t.Errorf("Kind = %q, want the stream name", got.Kind)
			}
			if got.Step != "step" {
				t.Errorf("Step = %q, want the step name", got.Step)
			}
		})
	}
}

// An unknown operator's message has to list the ones that do work, and must not
// offer the body-only operators as if a stream could be compared with them.
func TestTheUnknownOperatorMessageListsTheTextOperators(t *testing.T) {
	got := Text("step", "stderr", textCheck("lte", "3"), "x")
	if got.Status != result.StatusError {
		t.Fatalf("status = %v, want error", got.Status)
	}
	for _, op := range TextOperators {
		if !strings.Contains(got.Error, op) {
			t.Errorf("error %q does not offer %q", got.Error, op)
		}
	}
	if strings.Contains(got.Error, OpType) {
		t.Errorf("error %q offers %q, which a text check has no use for", got.Error, OpType)
	}
}

func TestExcerptKeepsAssertionLinesShort(t *testing.T) {
	if got := Excerpt("short"); got != "short" {
		t.Errorf("Excerpt(short) = %q, want it untouched", got)
	}

	long := strings.Repeat("x", MaxActual*3)
	got := Excerpt(long)
	if len(got) >= len(long) {
		t.Errorf("Excerpt kept %d bytes of %d, want it truncated", len(got), len(long))
	}
	if !strings.HasPrefix(got, strings.Repeat("x", MaxActual)) {
		t.Errorf("Excerpt = %q, want it to start with the first %d bytes", got, MaxActual)
	}
	if !strings.Contains(got, "600 bytes in all") {
		t.Errorf("Excerpt = %q, want it to say how much there was", got)
	}
}

// The excerpt is what lands on the assertion, so a failed check against a huge
// stream is still one readable line.
func TestATextAssertionDoesNotCarryTheWholeStream(t *testing.T) {
	got := Text("step", "stdout", textCheck("contains", "needle"), strings.Repeat("y", 5000))
	actual, ok := got.Actual.(string)
	if !ok {
		t.Fatalf("Actual = %T, want a string", got.Actual)
	}
	if len(actual) > MaxActual+64 {
		t.Errorf("Actual is %d bytes, want an excerpt", len(actual))
	}
}
