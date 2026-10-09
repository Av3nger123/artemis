package diag

import (
	"encoding/json"
	"io"
	"strings"

	"artemis/pkg/dsl/token"
)

// jsonSpan is a span as a UI reads it. All six fields of token.Span, named as
// docs/artemis-dsl-design.md names them in the tree encoding -- one span shape
// across `artemis ast` and `artemis parse --json`, so a client has one span
// parser and not two. file and offset are included even though the design's
// worked example elides them: a UI holding diagnostics from several files needs
// file, and offset is what an editor applying a suggestion indexes by.
type jsonSpan struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"endLine"`
	EndCol  int    `json:"endCol"`
	Offset  int    `json:"offset"`
}

// jsonSuggestion is the design's `{"replace": "status"}`.
type jsonSuggestion struct {
	Replace string `json:"replace"`
}

// jsonDiagnostic is one diagnostic. Key order is the struct's order, which is
// the order the design documents, and usedFrom, hint and suggestions are absent
// rather than null when there are none -- a client testing truthiness and a client
// testing presence then agree.
type jsonDiagnostic struct {
	Code        Code             `json:"code"`
	Severity    string           `json:"severity"`
	Span        jsonSpan         `json:"span"`
	UsedFrom    []jsonSpan       `json:"usedFrom,omitempty"`
	Message     string           `json:"message"`
	Hint        string           `json:"hint,omitempty"`
	Suggestions []jsonSuggestion `json:"suggestions,omitempty"`
}

// jsonEnvelope is the object `--json` prints. An object and not a bare array,
// because a later field -- a count, a schema version, a summary -- can be added
// to an object without breaking a client that already parses it.
type jsonEnvelope struct {
	Diagnostics []jsonDiagnostic `json:"diagnostics"`
}

// JSON writes the structured form of diags, which is what `artemis parse
// --json` emits and the UI in subsystem D consumes:
//
//	{"diagnostics": [
//	  {"code": "unknown-field", "severity": "error",
//	   "span": {...}, "message": "unknown field \"statu\"",
//	   "hint": "did you mean \"status\"?",
//	   "suggestions": [{"replace": "status"}]}
//	]}
//
// Indented with two spaces and terminated with a newline: a diagnostic dump is
// read by people at least as often as by programs, and a golden file of it has
// to be reviewable. Output is fully determined by the input -- no map
// iteration, no timestamps -- so the same diagnostics always produce the same
// bytes.
//
// An empty slice still writes `{"diagnostics": []}` rather than null, so a
// client can skip the empty check.
func JSON(w io.Writer, diags []Diagnostic) error {
	env := jsonEnvelope{Diagnostics: make([]jsonDiagnostic, 0, len(diags))}
	for _, d := range diags {
		env.Diagnostics = append(env.Diagnostics, jsonDiagnostic{
			Code:        d.Code,
			Severity:    d.Severity.String(),
			Span:        toJSONSpan(d.Span),
			UsedFrom:    toJSONSpans(d.UsedFrom),
			Message:     d.Message,
			Hint:        d.Hint,
			Suggestions: toJSONSuggestions(d.Suggestions),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// HTML escaping would spell a "<" in a message as a \u003c escape, which is
	// noise in a file a person reads and is nothing a JSON parser needs.
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

// JSONString is JSON into a string, for tests and for a caller assembling
// output rather than streaming it.
func JSONString(diags []Diagnostic) string {
	var b strings.Builder
	// A strings.Builder never fails a write, and the input cannot fail to
	// marshal: every field is a string or an int.
	_ = JSON(&b, diags)
	return b.String()
}

func toJSONSpan(s token.Span) jsonSpan {
	return jsonSpan{
		File:    s.File,
		Line:    s.Line,
		Col:     s.Col,
		EndLine: s.EndLine,
		EndCol:  s.EndCol,
		Offset:  s.Offset,
	}
}

// toJSONSpans is a use chain on the wire: nil when empty, so the key is
// absent for source written in place.
func toJSONSpans(ss []token.Span) []jsonSpan {
	if len(ss) == 0 {
		return nil
	}
	out := make([]jsonSpan, 0, len(ss))
	for _, s := range ss {
		out = append(out, toJSONSpan(s))
	}
	return out
}

func toJSONSuggestions(ss []Suggestion) []jsonSuggestion {
	if len(ss) == 0 {
		return nil
	}
	out := make([]jsonSuggestion, 0, len(ss))
	for _, s := range ss {
		// A conversion and not a struct literal: the two shapes differ only in
		// the tag, and a field added to Suggestion should stop this compiling
		// rather than be dropped from the wire format without anyone deciding.
		out = append(out, jsonSuggestion(s))
	}
	return out
}
