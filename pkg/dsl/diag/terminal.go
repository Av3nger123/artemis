package diag

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"artemis/pkg/dsl/token"
)

// Files is the source the terminal renderer echoes, keyed by the file name in a
// span. The caller fills it with text it has already read, so this package does
// no I/O: rendering tests are hermetic, and a UI can render a diagnostic for a
// buffer that was never on disk.
//
// A nil or empty Files is usable. It renders every diagnostic as its header
// line alone, which is the right answer for a span whose file nobody has.
type Files struct {
	lines map[string][]string
}

// NewFiles returns an empty Files.
func NewFiles() *Files { return &Files{lines: map[string][]string{}} }

// Add registers a file's source. The split is done once, here, because a file
// with twenty diagnostics in it would otherwise be split twenty times.
//
// Lines are split on "\n" with a trailing "\r" removed, so a CRLF file renders
// exactly like an LF one.
func (f *Files) Add(name, src string) {
	if f.lines == nil {
		f.lines = map[string][]string{}
	}
	split := strings.Split(src, "\n")
	for i, l := range split {
		split[i] = strings.TrimSuffix(l, "\r")
	}
	f.lines[name] = split
}

// Line returns a 1-based line of a file, and whether it exists. A span from a
// node built in Go rather than read from a file names no file and gets false,
// as does a line past the end of one.
func (f *Files) Line(name string, n int) (string, bool) {
	if f == nil || n < 1 {
		return "", false
	}
	ls, ok := f.lines[name]
	if !ok || n > len(ls) {
		return "", false
	}
	return ls[n-1], true
}

// maxEchoLines is how many lines of a multi-line span are echoed before the
// middle is elided. A span that covers a whole block -- an unterminated string
// swallowing the rest of a file -- would otherwise print the file.
const maxEchoLines = 6

// Terminal writes diagnostics the way a compiler does, in the format
// docs/artemis-dsl-design.md fixes:
//
//	checkout.art:12:10: unknown field "statu"
//	   12 |   expect statu == 200
//	      |          ^^^^^
//	   hint: did you mean "status"?
//
// One blank line separates consecutive diagnostics. The source line is echoed
// only when the caller gave us the file; otherwise the header stands alone, so
// a missing source degrades the output rather than losing the diagnostic.
//
// Severity appears in the header only for a warning. An error reads as
// `file:line:col: message`, which is what every tool in a terminal expects and
// what an editor's error parser matches.
func Terminal(w io.Writer, files *Files, diags []Diagnostic) error {
	for i, d := range diags {
		if i > 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		if err := writeOne(w, files, d); err != nil {
			return err
		}
	}
	return nil
}

// TerminalString is Terminal into a string, for tests and for a caller
// assembling output rather than streaming it.
func TerminalString(files *Files, diags []Diagnostic) string {
	var b strings.Builder
	// A strings.Builder never fails a write.
	_ = Terminal(&b, files, diags)
	return b.String()
}

func writeOne(w io.Writer, files *Files, d Diagnostic) error {
	var b strings.Builder

	prefix := ""
	if d.Severity != Error {
		prefix = d.Severity.String() + ": "
	}
	// A diagnostic about a node built in Go rather than read from a file has
	// no position. ":0:0:" would be a lie an editor's error parser would try
	// to follow, so such a diagnostic is just its message.
	if d.Span.File == "" && d.Span.Line == 0 {
		fmt.Fprintf(&b, "%s%s\n", prefix, d.Message)
	} else {
		fmt.Fprintf(&b, "%s:%d:%d: %s%s\n", d.Span.File, d.Span.Line, d.Span.Col, prefix, d.Message)
	}

	echo(&b, files, d.Span)
	hint(&b, d.Hint)
	usedFrom(&b, files, d.UsedFrom)

	_, err := io.WriteString(w, b.String())
	return err
}

// echo writes the gutter: each line the span covers, numbered, with a caret run
// under the part of it the span covers.
//
//	12 |   expect statu == 200
//	   |          ^^^^^
//
// The number is right-aligned in a field three wider than the widest number
// printed, and the caret line repeats the gutter with the number blanked, so
// the two pipes line up however many digits a line number has.
func echo(b *strings.Builder, files *Files, span token.Span) {
	nums := echoLines(files, span)
	if len(nums) == 0 {
		return
	}

	width := 3 + len(strconv.Itoa(nums[len(nums)-1].num))
	for _, l := range nums {
		if l.elided {
			// A gutter with no number and no pipe: it marks the gap without
			// pretending to be a line of the file.
			b.WriteString(strings.Repeat(" ", width-3) + "...\n")
			continue
		}
		num := strconv.Itoa(l.num)
		// Trailing blanks are trimmed: they are invisible, they are what an
		// editor strips on save, and a golden file that carries them is a
		// golden file that fails for no reason.
		b.WriteString(strings.TrimRight(strings.Repeat(" ", width-len(num))+num+" | "+l.text, " ") + "\n")
		if l.caretLen > 0 {
			b.WriteString(strings.Repeat(" ", width) + " | " +
				strings.Repeat(" ", l.caretCol) + strings.Repeat("^", l.caretLen) + "\n")
		}
	}
}

// echoLine is one line of the gutter: the source as it will be printed, and
// where the carets go in it, both measured in bytes.
type echoLine struct {
	num      int
	text     string
	caretCol int
	caretLen int
	elided   bool
}

// echoLines works out what the gutter shows for a span.
//
// Columns count bytes (see token.Span), so caret alignment only holds if the
// echoed line is measured in bytes too. A tab would break that -- one byte, one
// column, but eight places on screen -- so every tab in an echoed line becomes
// a single space. The line printed is then not quite the line in the file,
// which is the right trade: a caret that points at the wrong token is a worse
// lie than a tab shown as a space.
func echoLines(files *Files, span token.Span) []echoLine {
	if span.Line < 1 {
		return nil
	}
	last := span.EndLine
	if last < span.Line {
		last = span.Line
	}

	var out []echoLine
	for n := span.Line; n <= last; n++ {
		text, ok := files.Line(span.File, n)
		if !ok {
			// A span running past the end of what we were given: echo the
			// lines we have and stop, rather than inventing empty ones.
			break
		}
		text = strings.ReplaceAll(text, "\t", " ")

		// The span covers [start,end) of this line, in 1-based columns. A line
		// after the span's first is covered from its first non-blank byte:
		// the span does cover the indentation, but carets running through
		// leading whitespace read as a mistake rather than as emphasis.
		start, end := indent(text)+1, len(text)+1
		if n == span.Line {
			start = span.Col
		}
		if n == span.EndLine {
			end = span.EndCol
		}
		start, end = clampCol(start, text), clampCol(end, text)

		l := echoLine{num: n, text: text, caretCol: start - 1}
		if l.caretLen = end - start; l.caretLen < 1 {
			// A zero-width span -- end of file, an insertion point -- still
			// gets one caret, because the position is the whole of what it
			// has to say. But a blank line inside a multi-line span gets
			// none: a caret under nothing points at nothing, and the line is
			// already shown as part of the run.
			l.caretLen = 0
			if span.Line == last {
				l.caretLen = 1
			}
		}
		out = append(out, l)
	}
	return elide(out)
}

// indent is how many leading blank bytes a line has, which is where the carets
// start on every line of a multi-line span but its first.
func indent(text string) int {
	for i := 0; i < len(text); i++ {
		if text[i] != ' ' && text[i] != '\t' {
			return i
		}
	}
	return len(text)
}

// clampCol keeps a column inside the line, so a span whose end was computed
// from a different revision of a file cannot index out of it.
func clampCol(col int, text string) int {
	if col < 1 {
		return 1
	}
	if col > len(text)+1 {
		return len(text) + 1
	}
	return col
}

// elide replaces the middle of a long run of lines with a marker, keeping the
// start of the span -- where the fault is -- and its last line.
func elide(ls []echoLine) []echoLine {
	if len(ls) <= maxEchoLines {
		return ls
	}
	head := ls[:maxEchoLines-2]
	out := make([]echoLine, 0, maxEchoLines)
	out = append(out, head...)
	out = append(out, echoLine{num: ls[len(ls)-1].num, elided: true})
	return append(out, ls[len(ls)-1])
}

// usedFrom writes the use chain, innermost first, one line per use with the
// use line's source trimmed after it:
//
//	used from checkout.art:14:3: use orders.create { sku = "A-1" }
//
// A use line in a file the caller did not give is named by position alone.
func usedFrom(b *strings.Builder, files *Files, chain []token.Span) {
	for _, s := range chain {
		fmt.Fprintf(b, "   used from %s:%d:%d", s.File, s.Line, s.Col)
		if line, ok := files.Line(s.File, s.Line); ok {
			b.WriteString(": " + strings.TrimSpace(line))
		}
		b.WriteString("\n")
	}
}

// hintIndent is what a hint's continuation lines are indented by, so they align
// under the text after "hint: ".
const hintIndent = "         "

// hint writes the hint, aligning continuation lines:
//
//	hint: a browser step binds page.url, page.title, text(), value(),
//	      attr(), count(), visible()
//
// The renderer never wraps: where a hint breaks is the producer's decision,
// made with knowledge of what reads well, and wrapping at a guessed terminal
// width would make a golden file depend on the environment it ran in.
func hint(b *strings.Builder, h string) {
	if h == "" {
		return
	}
	for i, line := range strings.Split(h, "\n") {
		if i == 0 {
			b.WriteString("   hint: " + line + "\n")
			continue
		}
		b.WriteString(hintIndent + line + "\n")
	}
}
