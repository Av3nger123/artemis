package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
)

// suggestionArt has one fault, and it is the one a UI exists to fix in a
// click: a misspelled field name, close enough to a real one that the checker
// can name the replacement.
const suggestionArt = `scenario "checkout" {
  step "login" {
    post "https://example.test/token" {
      quer "limit" = 10
    }
    expect status == 200
  }
}
`

// jsonDiag is the document as a client reads it -- declared here rather than
// imported from pkg/dsl/diag, whose own types are unexported, so that this
// test fails if a field is renamed. It is the contract, written down twice on
// purpose.
type jsonDiag struct {
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

func parseJSONOutput(t *testing.T, stdout string) jsonDiag {
	t.Helper()
	var doc jsonDiag
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	return doc
}

// A clean file still writes a document. A client that got nothing would have
// to tell "no diagnostics" apart from "the command did not run", and the empty
// list says which.
func TestParseJSONOnACleanFile(t *testing.T) {
	path := writeArt(t, "checkout.art", cleanArt)

	stdout, stderr, err := executeArgs(t, "parse", "-f", path, "--json")
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	if stdout != "{\n  \"diagnostics\": []\n}\n" {
		t.Errorf("stdout = %q, want an empty diagnostics list", stdout)
	}
	if len(parseJSONOutput(t, stdout).Diagnostics) != 0 {
		t.Error("a clean file reported a diagnostic")
	}
}

// The case the UI builder is built on: one fault, located to the column, with
// a mechanical fix attached.
func TestParseJSONCarriesSpansAndSuggestions(t *testing.T) {
	path := writeArt(t, "typo.art", suggestionArt)

	stdout, _, err := executeArgs(t, "parse", "-f", path, "--json")
	if err == nil {
		t.Fatal("Execute() = nil, want an error so the process exits non-zero")
	}

	doc := parseJSONOutput(t, stdout)
	if len(doc.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1:\n%s", len(doc.Diagnostics), stdout)
	}
	d := doc.Diagnostics[0]
	if d.Code != string(diag.UnknownField) {
		t.Errorf("code = %q, want %q", d.Code, diag.UnknownField)
	}
	if d.Severity != "error" {
		t.Errorf("severity = %q, want \"error\"", d.Severity)
	}
	if d.Span.File != path || d.Span.Line != 4 || d.Span.Col != 7 || d.Span.EndCol != 11 {
		t.Errorf("span = %+v, want %s line 4, cols 7 to 11 -- the word `quer`", d.Span, path)
	}
	if d.Span.Offset <= 0 {
		t.Errorf("span.offset = %d; an editor splicing a suggestion indexes by it", d.Span.Offset)
	}
	if !strings.Contains(d.Message, "quer") {
		t.Errorf("message = %q, want it to name the field", d.Message)
	}
	if len(d.Suggestions) != 1 || d.Suggestions[0].Replace != "query" {
		t.Errorf("suggestions = %+v, want one replacing with \"query\"", d.Suggestions)
	}
	if d.Hint == "" {
		t.Error("the diagnostic has no hint")
	}
}

// Every fault, not the first: the same promise the rendered form makes.
func TestParseJSONReportsEveryErrorInFileOrder(t *testing.T) {
	path := writeArt(t, "many_errors.art", manyErrorsArt)

	stdout, _, err := executeArgs(t, "parse", "-f", path, "--json")
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	doc := parseJSONOutput(t, stdout)
	if len(doc.Diagnostics) != 4 {
		t.Fatalf("got %d diagnostics, want 4:\n%s", len(doc.Diagnostics), stdout)
	}
	prev := 0
	for i, d := range doc.Diagnostics {
		if d.Span.Line < prev {
			t.Errorf("diagnostic %d is at line %d, after one at line %d: the list is not in file order", i, d.Span.Line, prev)
		}
		prev = d.Span.Line
		if !diag.Registered(diag.Code(d.Code)) {
			t.Errorf("diagnostic %d has the code %q, which pkg/dsl/diag does not register", i, d.Code)
		}
	}
}

// stdout carries the document and nothing else, so `artemis parse --json |
// jq` works on a broken file -- which is the only kind of file anyone pipes
// it on. The one-line count goes to stderr with the exit status.
func TestParseJSONKeepsStdoutPure(t *testing.T) {
	path := writeArt(t, "typo.art", suggestionArt)

	stdout, stderr, err := executeArgs(t, "parse", "-f", path, "--json")
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if !strings.HasPrefix(stdout, "{") || !strings.HasSuffix(stdout, "}\n") {
		t.Errorf("stdout is not exactly one document:\n%q", stdout)
	}
	if strings.Contains(stdout, "^^^") || strings.Contains(stdout, "hint:") {
		t.Errorf("the rendered report leaked into stdout:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q; --json renders nothing, and the count is on the error", stderr)
	}
	if got := err.Error(); !strings.Contains(got, "1 error in") || !strings.Contains(got, path) {
		t.Errorf("Execute() = %q, want it to name one error and the path", got)
	}
}

// A missing file is a read failure, not a diagnostic: there is no source to
// have a span in, so no document is written.
func TestParseJSONOnAMissingFile(t *testing.T) {
	stdout, _, err := executeArgs(t, "parse", "-f", "nowhere.art", "--json")
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}

// Without the flag nothing changed: the report is on stderr and the ok line on
// stdout. Asserted here because --json is a branch, and a branch that altered
// the other side would be found by a user rather than by a test.
func TestParseWithoutJSONIsUnchanged(t *testing.T) {
	path := writeArt(t, "typo.art", suggestionArt)

	stdout, stderr, err := executeArgs(t, "parse", "-f", path)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if !strings.Contains(stderr, `did you mean "query"?`) {
		t.Errorf("stderr does not carry the rendered hint:\n%s", stderr)
	}
}

// --json describes pkg/dsl/diag diagnostics, which only the .art front end
// produces. A YAML path is refused by name rather than lexed as if it were
// source, which would bury the real mistake under a diagnostic per line.
func TestParseJSONRefusesANonArtFile(t *testing.T) {
	path := writeArt(t, "scenario.yaml", "name: health\n")

	stdout, _, err := executeArgs(t, "parse", "-f", path, "--json")
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if !strings.Contains(err.Error(), ".art") {
		t.Errorf("Execute() = %q, want it to name .art", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}
