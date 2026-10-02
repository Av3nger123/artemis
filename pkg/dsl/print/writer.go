package print

import "strings"

// indentUnit and margin are canonical mode's two fixed numbers. They are
// constants rather than options on purpose: a formatter whose output depended
// on a setting would make idempotence a per-configuration claim, and `artemis
// fmt -w` would produce a different file for two people in the same repository.
const (
	indentUnit = "  "
	margin     = 100
)

// writer is canonical mode's line model.
//
// Everything is written into a current line, which is committed by endLine with
// its indentation prepended and its trailing spaces stripped -- so a construct
// that renders to nothing (a `var` whose value did not parse, a block with no
// fields left) costs no stray whitespace, and the renderers never have to think
// about when not to emit a space.
//
// A blank line is requested rather than written. blank() queues one and the next
// endLine emits it, so a blank at the start of the output, a blank just before a
// closing brace, and two blanks in a row all collapse to nothing without a
// special case at each call site.
type writer struct {
	out   strings.Builder
	line  strings.Builder
	depth int
	want  bool // a blank line is queued
	skip  bool // swallow the next requested blank line
	any   bool // at least one line has been committed
}

func newWriter() *writer { return &writer{} }

// push appends to the current line.
func (w *writer) push(s string) { w.line.WriteString(s) }

// endLine commits the current line. An empty one is dropped, which is how
// canonical mode has no trailing whitespace anywhere by construction.
func (w *writer) endLine() {
	s := trimTrail(w.line.String())
	w.line.Reset()
	if s == "" {
		return
	}
	if w.want && w.any {
		w.out.WriteByte('\n')
	}
	w.want = false
	w.skip = false
	for i := 0; i < w.depth; i++ {
		w.out.WriteString(indentUnit)
	}
	w.out.WriteString(s)
	w.out.WriteByte('\n')
	w.any = true
}

// blank queues one blank line before the next committed line.
func (w *writer) blank() {
	if w.skip {
		w.skip = false
		return
	}
	w.want = true
}

func (w *writer) in()   { w.depth++ }
func (w *writer) outd() { w.depth-- }

// width is how many columns are left on a line at the current indentation.
func (w *writer) width() int { return margin - len(indentUnit)*w.depth }

// flush commits a dangling line, for a node rendered on its own rather than as
// part of a file.
func (w *writer) flush() { w.endLine() }

func (w *writer) String() string { return w.out.String() }

// join returns the non-empty parts separated by sep, which is how every
// renderer here builds a line: a token that was never in the file, or an
// expression that did not parse, contributes an empty string and disappears
// rather than leaving a doubled space behind.
func join(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

// suppress makes the next requested blank line a no-op, for the two places
// canonical mode does not keep one: the top of a block and the line it closes
// on. It lasts exactly until the next committed line, so a blank the author
// left further down is unaffected.
func (w *writer) suppress() { w.skip = true }
