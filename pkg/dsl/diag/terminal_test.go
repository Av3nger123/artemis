package diag

import (
	"errors"
	"strings"
	"testing"
)

// TestTabsAreRenderedAsOneSpace covers the trade the renderer makes: columns
// count bytes, so a tab shown as a tab would put the caret under the wrong
// token. A tab shown as a space is a line that is not quite the file; a caret
// pointing at the wrong thing is worse.
func TestTabsAreRenderedAsOneSpace(t *testing.T) {
	files := NewFiles()
	files.Add("x.art", "scenario \"x\" {\n\t\texpect statu == 200\n}\n")

	b := New()
	// Two tabs, then `expect `, so `statu` starts at byte 10 of the line.
	b.Error(span("x.art", 2, 10, 2, 15, 0), UnknownField, "unknown field %q", "statu")

	want := "x.art:2:10: unknown field \"statu\"\n" +
		"   2 |   expect statu == 200\n" +
		"     |          ^^^^^\n"
	if got := TerminalString(files, b.All()); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestCRLFRendersLikeLF: a file written on Windows should not render with a
// stray carriage return in the middle of the gutter.
func TestCRLFRendersLikeLF(t *testing.T) {
	crlf, lf := NewFiles(), NewFiles()
	crlf.Add("x.art", "scenario \"x\" {\r\n  expect statu == 200\r\n}\r\n")
	lf.Add("x.art", "scenario \"x\" {\n  expect statu == 200\n}\n")

	b := New()
	b.Error(span("x.art", 2, 10, 2, 15, 0), UnknownField, "unknown field %q", "statu")
	if got, want := TerminalString(crlf, b.All()), TerminalString(lf, b.All()); got != want {
		t.Errorf("CRLF rendered differently:\n%s\nwant:\n%s", got, want)
	}
}

func TestFilesLine(t *testing.T) {
	f := NewFiles()
	f.Add("x.art", "one\ntwo\n")
	for _, c := range []struct {
		name string
		file string
		n    int
		want string
		ok   bool
	}{
		{"first", "x.art", 1, "one", true},
		{"second", "x.art", 2, "two", true},
		// The empty string after a trailing newline is a line of the file: a
		// span at end of file points into it.
		{"after the trailing newline", "x.art", 3, "", true},
		{"past the end", "x.art", 4, "", false},
		{"zero", "x.art", 0, "", false},
		{"unknown file", "y.art", 1, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := f.Line(c.file, c.n)
			if got != c.want || ok != c.ok {
				t.Errorf("Line(%q, %d) = %q, %v; want %q, %v", c.file, c.n, got, ok, c.want, c.ok)
			}
		})
	}
}

// TestNilFilesRendersHeadersOnly: a caller with no sources to hand -- a UI
// validating a tree it built, a command rendering a diagnostic about a file it
// could not read -- still gets its diagnostics.
func TestNilFilesRendersHeadersOnly(t *testing.T) {
	b := New()
	b.Error(span("x.art", 2, 10, 2, 15, 0), UnknownField, "unknown field %q", "statu")
	want := "x.art:2:10: unknown field \"statu\"\n"
	if got := TerminalString(nil, b.All()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := TerminalString(NewFiles(), b.All()); got != want {
		t.Errorf("an empty Files rendered %q", got)
	}
}

func TestTerminalBlankLineBetweenDiagnostics(t *testing.T) {
	b := New()
	b.Error(span("x.art", 1, 1, 1, 2, 0), UnknownField, "first")
	b.Error(span("x.art", 2, 1, 2, 2, 10), UnknownField, "second")
	got := TerminalString(nil, b.All())
	if got != "x.art:1:1: first\n\nx.art:2:1: second\n" {
		t.Errorf("got %q", got)
	}
	if strings.HasSuffix(got, "\n\n") {
		t.Error("output ends in a blank line; the separator belongs between diagnostics, not after the last")
	}
}

// TestTerminalPropagatesWriteErrors: output goes to a pipe or a terminal that
// can close, and a renderer that swallowed the error would make a command exit
// zero having printed nothing.
func TestTerminalPropagatesWriteErrors(t *testing.T) {
	b := New()
	b.Error(span("x.art", 1, 1, 1, 2, 0), UnknownField, "first")
	b.Error(span("x.art", 2, 1, 2, 2, 10), UnknownField, "second")

	for _, failAfter := range []int{0, 1, 2} {
		w := &failingWriter{after: failAfter}
		if err := Terminal(w, nil, b.All()); !errors.Is(err, errClosed) {
			t.Errorf("failing on write %d returned %v", failAfter, err)
		}
	}
}

var errClosed = errors.New("closed")

// failingWriter fails the write after `after` successful ones.
type failingWriter struct {
	after int
	n     int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.n >= w.after {
		return 0, errClosed
	}
	w.n++
	return len(p), nil
}
