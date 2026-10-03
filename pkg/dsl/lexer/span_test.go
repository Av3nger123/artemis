package lexer

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/token"
)

// at is a span written the way a test reads it: line, col, endLine, endCol,
// offset. EndLine and EndCol are exclusive -- the position just past the last
// byte -- which is the convention the design doc's diagnostics use, where the
// caret under `statu` runs col 10 to endCol 15.
func at(line, col, endLine, endCol, offset int) token.Span {
	return token.Span{
		File: "x.art", Line: line, Col: col,
		EndLine: endLine, EndCol: endCol, Offset: offset,
	}
}

// TestSpansPerKind asserts every field of the span of at least one token of
// every kind the lexer can produce. It is the issue's acceptance test, so it
// checks the kinds off against token's own list rather than trusting the table
// below to be complete.
func TestSpansPerKind(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// index of the token to check, and the span it must have
		index int
		kind  token.Kind
		span  token.Span
	}{
		{"identifier", "  status", 0, token.Ident, at(1, 3, 1, 9, 2)},
		{"boolean", "headless = true", 2, token.Bool, at(1, 12, 1, 16, 11)},
		{"null", "x = null", 2, token.Null, at(1, 5, 1, 9, 4)},
		{"number", "times = 3", 2, token.Number, at(1, 9, 1, 10, 8)},
		{"float", "d = 2.50", 2, token.Number, at(1, 5, 1, 9, 4)},
		{"string", `get "/orders"`, 1, token.String, at(1, 5, 1, 14, 4)},
		{"regex", `matches /.+@.+/`, 1, token.Regex, at(1, 9, 1, 16, 8)},
		{"==", "status == 200", 1, token.Eq, at(1, 8, 1, 10, 7)},
		{"!=", "status != 200", 1, token.Ne, at(1, 8, 1, 10, 7)},
		{"<", "a < b", 1, token.Lt, at(1, 3, 1, 4, 2)},
		{"<=", "a <= b", 1, token.Le, at(1, 3, 1, 5, 2)},
		{">", "a > b", 1, token.Gt, at(1, 3, 1, 4, 2)},
		{">=", "a >= b", 1, token.Ge, at(1, 3, 1, 5, 2)},
		{"-", "-1", 0, token.Minus, at(1, 1, 1, 2, 0)},
		{"=", "x = 1", 1, token.Assign, at(1, 3, 1, 4, 2)},
		{"{", "step {", 1, token.LBrace, at(1, 6, 1, 7, 5)},
		{"}", "{ }", 1, token.RBrace, at(1, 3, 1, 4, 2)},
		{"(", "env(", 1, token.LParen, at(1, 4, 1, 5, 3)},
		{")", "env()", 2, token.RParen, at(1, 5, 1, 6, 4)},
		{"[", "a[0]", 1, token.LBracket, at(1, 2, 1, 3, 1)},
		{"]", "a[0]", 3, token.RBracket, at(1, 4, 1, 5, 3)},
		{",", "[1, 2]", 2, token.Comma, at(1, 3, 1, 4, 2)},
		{".", "body.id", 1, token.Dot, at(1, 5, 1, 6, 4)},
		{":", `{"a": 1}`, 2, token.Colon, at(1, 5, 1, 6, 4)},
		{"string-start", `"a${x}"`, 0, token.StringStart, at(1, 1, 1, 5, 0)},
		{"string-mid", `"${a}m${b}"`, 2, token.StringMid, at(1, 5, 1, 9, 4)},
		{"string-end", `"a${x}b"`, 2, token.StringEnd, at(1, 6, 1, 9, 5)},
		{"invalid", "a ! b", 1, token.Invalid, at(1, 3, 1, 4, 2)},
		// EOF is zero-width at the position just past the last byte, which
		// after a trailing newline is column 1 of the next line.
		{"EOF", "abc", 1, token.EOF, at(1, 4, 1, 4, 3)},
		{"EOF after a newline", "abc\n", 1, token.EOF, at(2, 1, 2, 1, 4)},
		{"EOF of an empty file", "", 0, token.EOF, at(1, 1, 1, 1, 0)},
	}

	covered := map[token.Kind]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks := Lex("x.art", c.src)
			if c.index >= len(toks) {
				t.Fatalf("Lex(%q) gave %d tokens, no index %d\n%s", c.src, len(toks), c.index, shape(toks))
			}
			got := toks[c.index]
			if got.Kind != c.kind {
				t.Fatalf("token %d of %q is %v, want %v\n%s", c.index, c.src, got.Kind, c.kind, shape(toks))
			}
			if got.Span != c.span {
				t.Errorf("span of %v in %q =\n  %+v\nwant\n  %+v", c.kind, c.src, got.Span, c.span)
			}
		})
		covered[c.kind] = true
	}

	for kind := token.EOF; kind <= token.Minus; kind++ {
		if !covered[kind] {
			t.Errorf("no span asserted for kind %v", kind)
		}
	}
}

// A string with a literal newline in it spans lines, which is the case a lexer
// that tracked only columns would get wrong.
func TestSpanOfAMultiLineString(t *testing.T) {
	src := "body = \"first\nsecond\nthird\"\nexpect status == 200"
	toks := Lex("x.art", src)

	str := find(t, toks, token.String)
	if want := at(1, 8, 3, 7, 7); str.Span != want {
		t.Errorf("string span = %+v, want %+v", str.Span, want)
	}
	if str.Span.EndLine <= str.Span.Line {
		t.Error("a multi-line string must end on a later line than it starts")
	}

	// And the token after it is located on the line a reader would say it is
	// on, which is what a lexer that lost count would break.
	next := nthIdent(t, toks, "expect", 1)
	if want := at(4, 1, 4, 7, 28); next.Span != want {
		t.Errorf("span after the multi-line string = %+v, want %+v", next.Span, want)
	}
}

// An interpolated string's pieces must tile its source exactly: no gap, no
// overlap, from the opening quote to the closing one. A gap would mean a byte
// belongs to no token, which is losslessness gone.
func TestInterpolationBoundariesAbut(t *testing.T) {
	src := `  get "${base}/users/${body.id}?q=x"`
	toks := Lex("x.art", src)

	var pieces []token.Token
	for _, tk := range toks {
		switch tk.Kind {
		case token.StringStart, token.StringMid, token.StringEnd:
			pieces = append(pieces, tk)
		}
	}
	if len(pieces) != 3 {
		t.Fatalf("got %d string pieces, want 3\n%s", len(pieces), shape(toks))
	}

	wantTexts := []string{`"${`, "}/users/${", `}?q=x"`}
	for i, p := range pieces {
		if p.Text != wantTexts[i] {
			t.Errorf("piece %d text = %q, want %q", i, p.Text, wantTexts[i])
		}
	}

	// The pieces plus the expressions between them cover [6, len(src)) with no
	// hole: each token starts where the previous one ended.
	start := pieces[0].Span.Offset
	cursor := start
	for _, tk := range toks {
		if tk.Span.Offset < start || tk.Kind == token.EOF {
			continue
		}
		if tk.Span.Offset != cursor {
			t.Fatalf("token %q starts at offset %d, want %d: the pieces do not tile",
				tk.Text, tk.Span.Offset, cursor)
		}
		cursor = tk.Span.Offset + len(tk.Text)
	}
	if cursor != len(src) {
		t.Errorf("the pieces cover up to offset %d, want %d", cursor, len(src))
	}
}

// The unclosed "${" is reported where it was written, not at end of file: the
// author has to go and look at that position, and end of file is nowhere near
// it. This is the issue's acceptance case -- a diagnostic-bearing token rather
// than a panic.
func TestUnclosedInterpolationIsReportedAtTheDollarBrace(t *testing.T) {
	src := "scenario \"x\" {\n  get \"${ base\n"
	toks := Lex("x.art", src)

	marker := find(t, toks, token.Invalid)
	if marker.Code != "unclosed-interpolation" {
		t.Fatalf("code = %q, want unclosed-interpolation\n%s", marker.Code, shape(toks))
	}
	if !marker.IsMarker() {
		t.Error("an unclosed ${ is a position-only marker: it consumes nothing")
	}
	if want := at(2, 8, 2, 10, 22); marker.Span != want {
		t.Errorf("span = %+v, want %+v (the ${ itself)", marker.Span, want)
	}
	if got := src[marker.Span.Offset : marker.Span.Offset+2]; got != "${" {
		t.Errorf("the span points at %q, not at the ${", got)
	}
	assertEndsInOneEOF(t, toks)
	assertInvariants(t, src)
}

// A dropped "}" inside an interpolation is swallowed by the next "}" in the
// file, which then ends the string instead of the block, and the error comes
// out as the unterminated string that results. This is the cost of expressions
// inside strings and it is written down here rather than discovered later: the
// lexer cannot know which "}" the author meant, and no heuristic short of
// forbidding multi-line strings recovers the better message.
func TestADroppedInterpolationBraceCascades(t *testing.T) {
	src := lines(
		`scenario "x" {`,
		`  get "${ base`,
		`}`,
	)
	toks := Lex("x.art", src)

	bad := find(t, toks, token.Invalid)
	if bad.Code != "unterminated-string" {
		t.Errorf("code = %q, want unterminated-string\n%s", bad.Code, shape(toks))
	}
	assertEndsInOneEOF(t, toks)
	assertInvariants(t, src)
}

// Every span the lexer produces must describe its token's text exactly:
// recomputing line, column and offset from the source has to give the same
// answer. This catches the whole class of off-by-one a hand-written scanner
// invites, over every input the test suite has.
func assertSpansDescribeTheirText(t *testing.T, file, src string, toks []token.Token) {
	t.Helper()
	for i, tk := range toks {
		span := tk.Span
		if span.File != file {
			t.Errorf("token %d (%v): span file = %q, want %q", i, tk.Kind, span.File, file)
		}
		if span.Offset < 0 || span.Offset > len(src) {
			t.Fatalf("token %d (%v): offset %d outside a %d-byte file", i, tk.Kind, span.Offset, len(src))
		}
		if tk.IsMarker() {
			// A marker locates a fault inside another token, so its span is
			// not its own text -- it has none -- but it still has to point
			// inside the file.
			continue
		}
		if got := src[span.Offset : span.Offset+len(tk.Text)]; got != tk.Text {
			t.Errorf("token %d (%v): source at offset %d is %q, but Text is %q",
				i, tk.Kind, span.Offset, got, tk.Text)
		}
		line, col := positionOf(src, span.Offset)
		if line != span.Line || col != span.Col {
			t.Errorf("token %d (%v, %q): span starts at %d:%d, but offset %d is %d:%d",
				i, tk.Kind, tk.Text, span.Line, span.Col, span.Offset, line, col)
		}
		endLine, endCol := positionOf(src, span.Offset+len(tk.Text))
		if endLine != span.EndLine || endCol != span.EndCol {
			t.Errorf("token %d (%v, %q): span ends at %d:%d, want %d:%d",
				i, tk.Kind, tk.Text, span.EndLine, span.EndCol, endLine, endCol)
		}
	}
}

// positionOf is the 1-based line and byte column of an offset, computed the
// slow obvious way so that it is not the lexer's own arithmetic being used to
// check the lexer's arithmetic.
func positionOf(src string, offset int) (line, col int) {
	line, col = 1, 1
	for i := 0; i < offset; i++ {
		switch {
		case src[i] == '\n':
			line++
			col = 1
		case src[i] == '\r':
			// \r\n advances the line once, on the \n. A lone \r advances it
			// here.
			if i+1 < len(src) && src[i+1] == '\n' {
				col++
			} else {
				line++
				col = 1
			}
		default:
			col++
		}
	}
	return line, col
}

func TestPositionOfMatchesTheLexerOnTheCorpus(t *testing.T) {
	for _, f := range corpus(t) {
		t.Run(f.name, func(t *testing.T) {
			assertSpansDescribeTheirText(t, f.name, f.src, Lex(f.name, f.src))
		})
	}
}

func TestSpansDescribeTheirTextOnEveryFragment(t *testing.T) {
	for _, src := range fragments {
		toks := Lex("x.art", src)
		assertSpansDescribeTheirText(t, "x.art", src, toks)
		if t.Failed() {
			t.Fatalf("while lexing %q:\n%s", src, strings.Join([]string{shape(toks)}, ""))
		}
	}
}
