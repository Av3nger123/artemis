// Package diag is how every stage of the DSL front end reports a problem: one
// Diagnostic type, a Bag that collects all of them, and two renderers -- one
// for a terminal, one for a UI.
//
// The bar is a compiler's, not a decoder's. A diagnostic names a span that
// covers the offending token exactly, carries a stable Code a client can key
// behaviour off, and may carry a Hint for a human and Suggestions a UI can
// apply mechanically. Reporting one error and stopping is the decoder
// behaviour this package exists to replace: a Bag keeps going, so a file with
// four mistakes in it produces four diagnostics.
//
// The package does no I/O. The terminal renderer is handed source text by its
// caller (see Files), which keeps every rendering test hermetic and lets a UI
// render a diagnostic for a buffer that was never on disk.
package diag

import (
	"fmt"
	"sort"

	"artemis/pkg/dsl/token"
)

// Severity is how much a diagnostic matters. There are two: a file either
// compiles or it does not, and anything worth saying that does not stop the
// compile is a warning. No note or info level, because a level nothing emits is
// a level nothing agrees on the meaning of.
type Severity uint8

const (
	// Error means the file does not compile. Any error in a run makes the
	// process exit non-zero.
	Error Severity = iota

	// Warning means the file compiles and something is still worth saying.
	Warning
)

var severityNames = map[Severity]string{
	Error:   "error",
	Warning: "warning",
}

// String is the name used in JSON and in terminal output, so the two never
// disagree about what a severity is called.
func (s Severity) String() string {
	if n, ok := severityNames[s]; ok {
		return n
	}
	return fmt.Sprintf("severity(%d)", uint8(s))
}

// Suggestion is a mechanical fix: replace the source covered by the
// diagnostic's span with Replace. That is the whole of it, and deliberately so
// -- the design's JSON shape is `suggestions: [{replace}]`, and a suggestion
// that edited some other range would need a span of its own. Nothing needs
// that yet, and a field can be added later where one cannot be removed.
//
// Replace is source text, not a decoded value: a suggestion replacing a string
// literal includes the quotes.
type Suggestion struct {
	Replace string
}

// Diagnostic is one problem with a file.
//
// Span locates it, and is token.Span so a diagnostic serialises with the same
// six fields as every node in the tree -- no translation layer between what the
// lexer produced and what a UI reads.
//
// Code is the stable identifier. Message is prose, and may be reworded freely:
// the invariant this package tests is that doing so changes no code. Hint is a
// second line for a human -- "did you mean" or the list of names that are in
// scope -- and may contain newlines, which the terminal renderer indents to
// align under the first line.
type Diagnostic struct {
	Span        token.Span
	Code        Code
	Severity    Severity
	Message     string
	Hint        string
	Suggestions []Suggestion
}

// Bag collects diagnostics. Collecting many is the normal case: the parser
// recovers at statement boundaries and the checker visits every step, so one
// pass over a bad file appends several times.
//
// A Bag is not safe for concurrent use. Nothing in the front end is
// concurrent; a caller that becomes so should give each goroutine its own bag
// and Merge them.
type Bag struct {
	diags []Diagnostic
}

// New returns an empty Bag. A zero Bag is equally usable; this exists for
// callers that want a pointer in one expression.
func New() *Bag { return &Bag{} }

// Add appends d and returns a handle for attaching a hint or suggestions.
//
// It fills in the severity registered for d.Code when d.Severity is the zero
// value and the code's registered severity is not Error, so a caller building
// a Diagnostic by hand cannot silently downgrade a warning code to an error.
func (b *Bag) Add(d Diagnostic) *Ref {
	b.diags = append(b.diags, d)
	return &Ref{bag: b, i: len(b.diags) - 1}
}

// Error appends an error diagnostic. The message is formatted, because nearly
// every call site interpolates the name it is complaining about.
func (b *Bag) Error(span token.Span, code Code, format string, args ...any) *Ref {
	return b.Add(Diagnostic{
		Span:     span,
		Code:     code,
		Severity: Error,
		Message:  fmt.Sprintf(format, args...),
	})
}

// Warn appends a warning diagnostic.
func (b *Bag) Warn(span token.Span, code Code, format string, args ...any) *Ref {
	return b.Add(Diagnostic{
		Span:     span,
		Code:     code,
		Severity: Warning,
		Message:  fmt.Sprintf(format, args...),
	})
}

// Merge appends every diagnostic from other.
func (b *Bag) Merge(other *Bag) {
	if other != nil {
		b.diags = append(b.diags, other.diags...)
	}
}

// Len is how many diagnostics have been added, duplicates included. It is the
// cheap "did anything go wrong" check; All is what decides what gets printed.
func (b *Bag) Len() int { return len(b.diags) }

// HasErrors reports whether any diagnostic is an Error, which is what a command
// turns into a non-zero exit status.
func (b *Bag) HasErrors() bool {
	for _, d := range b.diags {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// All returns the diagnostics to report: sorted by file, then by byte offset,
// then by the order they were added, with exact duplicates removed.
//
// Sorting by offset means output is in the reading order of the file no matter
// which pass found what -- a lexical error on line 3 and a scope error on line
// 2 come out in line order. Removing exact duplicates matters because parser
// recovery genuinely re-reports the same fault: two paths reaching the same
// token produce two identical diagnostics, and a reader should see one. Two
// diagnostics differing in any field are both kept, because a token can be
// wrong in more than one way.
//
// The returned slice is a copy; the bag is unchanged.
func (b *Bag) All() []Diagnostic {
	out := make([]Diagnostic, len(b.diags))
	copy(out, b.diags)

	sort.SliceStable(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.Span.File != c.Span.File {
			return a.Span.File < c.Span.File
		}
		if a.Span.Offset != c.Span.Offset {
			return a.Span.Offset < c.Span.Offset
		}
		return false
	})

	dedup := out[:0]
	for _, d := range out {
		if len(dedup) > 0 && equal(dedup[len(dedup)-1], d) {
			continue
		}
		dedup = append(dedup, d)
	}
	return dedup
}

// equal reports whether two diagnostics say exactly the same thing. Adjacent
// comparison is enough for dedup because All has already grouped by file and
// offset, and a duplicate of a diagnostic shares its span by definition.
func equal(a, b Diagnostic) bool {
	if a.Span != b.Span || a.Code != b.Code || a.Severity != b.Severity ||
		a.Message != b.Message || a.Hint != b.Hint ||
		len(a.Suggestions) != len(b.Suggestions) {
		return false
	}
	for i := range a.Suggestions {
		if a.Suggestions[i] != b.Suggestions[i] {
			return false
		}
	}
	return true
}

// Ref points at a diagnostic already in a bag, so the fields that are optional
// can be attached in the same expression that reported it:
//
//	b.Error(span, UnknownField, "unknown field %q", name).
//		Hintf("did you mean %q?", best).
//		Suggest(best)
type Ref struct {
	bag *Bag
	i   int
}

// Hintf sets the hint. A hint may contain newlines; the terminal renderer
// aligns continuation lines under the first.
func (r *Ref) Hintf(format string, args ...any) *Ref {
	if r == nil {
		return r
	}
	r.bag.diags[r.i].Hint = fmt.Sprintf(format, args...)
	return r
}

// Suggest appends mechanical replacements for the diagnostic's span.
func (r *Ref) Suggest(replacements ...string) *Ref {
	if r == nil {
		return r
	}
	d := &r.bag.diags[r.i]
	for _, s := range replacements {
		d.Suggestions = append(d.Suggestions, Suggestion{Replace: s})
	}
	return r
}
