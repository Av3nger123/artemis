package diag

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestJSONShape pins the contract a UI parses, field by field, against the
// example in docs/artemis-dsl-design.md. The golden in render_test.go holds
// whole documents; this says what each field is, so a change to one is a change
// to an assertion about it rather than a line in a file.
func TestJSONShape(t *testing.T) {
	b := New()
	b.Error(span("checkout.art", 12, 10, 12, 15, 387), UnknownField, "unknown field %q", "statu").
		DidYouMean("statu", []string{"status"})

	var got struct {
		Diagnostics []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
			Span     struct {
				File    string `json:"file"`
				Line    int    `json:"line"`
				Col     int    `json:"col"`
				EndLine int    `json:"endLine"`
				EndCol  int    `json:"endCol"`
				Offset  int    `json:"offset"`
			} `json:"span"`
			Message     string `json:"message"`
			Hint        string `json:"hint"`
			Suggestions []struct {
				Replace string `json:"replace"`
			} `json:"suggestions"`
		} `json:"diagnostics"`
	}
	out := JSONString(b.All())
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(got.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics", len(got.Diagnostics))
	}
	d := got.Diagnostics[0]
	switch {
	case d.Code != "unknown-field":
		t.Errorf("code = %q", d.Code)
	case d.Severity != "error":
		t.Errorf("severity = %q", d.Severity)
	case d.Span.File != "checkout.art" || d.Span.Line != 12 || d.Span.Col != 10 ||
		d.Span.EndLine != 12 || d.Span.EndCol != 15 || d.Span.Offset != 387:
		t.Errorf("span = %+v", d.Span)
	case d.Message != `unknown field "statu"`:
		t.Errorf("message = %q", d.Message)
	case d.Hint != `did you mean "status"?`:
		t.Errorf("hint = %q", d.Hint)
	case len(d.Suggestions) != 1 || d.Suggestions[0].Replace != "status":
		t.Errorf("suggestions = %+v", d.Suggestions)
	}
}

// TestJSONOmitsEmptyOptionalFields: a client testing for presence and a client
// testing for truthiness have to agree, which they do not if an absent hint is
// spelled `"hint": ""` by one renderer and left out by another.
func TestJSONOmitsEmptyOptionalFields(t *testing.T) {
	b := New()
	b.Error(span("x.art", 1, 1, 1, 2, 0), UnknownField, "no hint, no fix")
	out := JSONString(b.All())
	if strings.Contains(out, `"hint"`) || strings.Contains(out, `"suggestions"`) {
		t.Errorf("an empty hint or suggestion list was emitted:\n%s", out)
	}
	for _, want := range []string{`"code"`, `"severity"`, `"span"`, `"message"`} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is missing from\n%s", want, out)
		}
	}
}

// TestJSONEmptyIsAnEmptyList, not null: `artemis parse --json` on a clean file
// still prints an envelope a client can iterate without a nil check.
func TestJSONEmptyIsAnEmptyList(t *testing.T) {
	if got := JSONString(nil); got != "{\n  \"diagnostics\": []\n}\n" {
		t.Errorf("empty rendering is %q", got)
	}
}

// TestJSONDoesNotEscapeHTML: a message or a suggestion quoting a selector --
// `text("div > p")` -- should read as itself in a file a person opens.
func TestJSONDoesNotEscapeHTML(t *testing.T) {
	b := New()
	b.Error(span("x.art", 1, 1, 1, 2, 0), UnknownField, `no element matches "div > p"`)
	if got := JSONString(b.All()); !strings.Contains(got, "div > p") {
		t.Errorf("a > was escaped:\n%s", got)
	}
}

// TestJSONIsDeterministic: two renderings of one input are byte-identical, so
// a golden file is a golden file and a UI can diff two runs.
func TestJSONIsDeterministic(t *testing.T) {
	b := New()
	b.Error(span("x.art", 1, 1, 1, 2, 0), UnknownField, "one").Suggest("a", "b")
	b.Warn(span("x.art", 2, 1, 2, 2, 10), NotInScope, "two").Hintf("a hint")
	// Two separate calls, held in two variables: the comparison is the point,
	first, again := JSONString(b.All()), JSONString(b.All())
	if first != again {
		t.Errorf("two renderings of the same diagnostics differ:\n%s\n%s", first, again)
	}
}
