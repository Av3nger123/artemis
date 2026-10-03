package lexer

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/token"
)

// shape renders a token stream as one "kind text" line per token, which is what
// a failing kind test should print: the kinds and the exact text, and nothing
// else to read past.
func shape(toks []token.Token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.Kind.String())
		if t.Text != "" {
			b.WriteString(" ")
			b.WriteString(strings.ReplaceAll(t.Text, "\n", `\n`))
		}
		if t.Code != "" {
			b.WriteString(" [" + t.Code + "]")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func lines(ss ...string) string { return strings.Join(ss, "\n") + "\n" }

func TestLexKinds(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"empty", "", lines("EOF")},
		{"only trivia", "# nothing but a comment\n", lines("EOF")},
		{
			"identifiers are not keywords",
			"scenario step get body contains within",
			lines(
				"identifier scenario", "identifier step", "identifier get",
				"identifier body", "identifier contains", "identifier within", "EOF"),
		},
		{
			"only true false and null get their own kinds",
			"true false null nullish",
			lines("boolean true", "boolean false", "null null", "identifier nullish", "EOF"),
		},
		{
			"underscores and digits in a name",
			"_x exit_code x2",
			lines("identifier _x", "identifier exit_code", "identifier x2", "EOF"),
		},
		{
			"numbers",
			"0 42 3.5 1e9 1E+9 2.5e-3",
			lines(
				"number 0", "number 42", "number 3.5", "number 1e9",
				"number 1E+9", "number 2.5e-3", "EOF"),
		},
		{
			// A `.` with no digit after it is the postfix operator, so the
			// lexer stops the number before it rather than inventing an error.
			"a trailing dot is the postfix operator",
			"1.",
			lines("number 1", ". .", "EOF"),
		},
		{
			// Same for an exponent with no digits: `e` is left to be an
			// identifier and the parser says what it expected.
			"an exponent with no digits is not consumed",
			"1e",
			lines("number 1", "identifier e", "EOF"),
		},
		{
			"a plain string is one token",
			`"hello, world"`,
			lines(`string "hello, world"`, "EOF"),
		},
		{
			"a lone dollar needs no escape",
			`"$5.00 and $ alone"`,
			lines(`string "$5.00 and $ alone"`, "EOF"),
		},
		{
			"an interpolated string is three tokens",
			`"a${x}b"`,
			lines(`string-start "a${`, "identifier x", `string-end }b"`, "EOF"),
		},
		{
			"two interpolations",
			`"${a}/${b}"`,
			lines(`string-start "${`, "identifier a", "string-mid }/${", "identifier b", `string-end }"`, "EOF"),
		},
		{
			"adjacent interpolations with no literal between them",
			`"${a}${b}"`,
			lines(`string-start "${`, "identifier a", "string-mid }${", "identifier b", `string-end }"`, "EOF"),
		},
		{
			"an interpolation is the whole string",
			`"${body.data.id}"`,
			lines(`string-start "${`, "identifier body", ". .", "identifier data", ". .", "identifier id", `string-end }"`, "EOF"),
		},
		{
			// The object literal's braces must not be mistaken for the
			// interpolation's closing brace.
			"an object literal inside an interpolation",
			`"${ f({"a": 1}) }"`,
			lines(
				`string-start "${`, "identifier f", "( (", "{ {", `string "a"`, ": :",
				"number 1", "} }", ") )", `string-end }"`, "EOF"),
		},
		{
			// The nested string pushes its own frame, so the stack is what
			// makes this work rather than a single open flag.
			"a string with an interpolation inside an interpolation",
			`"${ text("${sel}") }"`,
			lines(
				`string-start "${`, "identifier text", "( (", `string-start "${`,
				"identifier sel", `string-end }"`, ") )", `string-end }"`, "EOF"),
		},
		{
			"a string spanning lines is one token",
			"\"first\nsecond\"",
			lines(`string "first\nsecond"`, "EOF"),
		},
		{
			"regex",
			`/.+@.+/`,
			lines("regex /.+@.+/", "EOF"),
		},
		{
			"an escaped slash does not end a regex",
			`/a\/b/`,
			lines(`regex /a\/b/`, "EOF"),
		},
		{
			"an empty regex",
			`//`,
			lines("regex //", "EOF"),
		},
		{
			"comparison operators",
			"== != < <= > >= -",
			lines("== ==", "!= !=", "< <", "<= <=", "> >", ">= >=", "- -", "EOF"),
		},
		{
			"delimiters",
			"{}()[],.:=",
			lines("{ {", "} }", "( (", ") )", "[ [", "] ]", ", ,", ". .", ": :", "= =", "EOF"),
		},
		{
			"a whole assertion",
			`expect body.data.count > 0`,
			lines("identifier expect", "identifier body", ". .", "identifier data", ". .",
				"identifier count", "> >", "number 0", "EOF"),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shape(Lex("x.art", c.src))
			if got != c.want {
				t.Errorf("Lex(%q) =\n%s\nwant\n%s", c.src, got, c.want)
			}
			assertInvariants(t, c.src)
		})
	}
}

// TestLexValues covers the decoded Value, which is what the parser puts in the
// tree -- escapes resolved, delimiters gone.
func TestLexValues(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string // one per token, EOF excluded
	}{
		{"plain", `"abc"`, []string{"abc"}},
		{"empty", `""`, []string{""}},
		{"escaped quote", `"say \"hi\""`, []string{`say "hi"`}},
		{"escaped backslash", `"a\\b"`, []string{`a\b`}},
		{"escaped slash", `"a\/b"`, []string{"a/b"}},
		{"escaped dollar", `"\${not an interpolation}"`, []string{"${not an interpolation}"}},
		{"control escapes", `"a\nb\tc\rd"`, []string{"a\nb\tc\rd"}},
		{"unicode escape", `"café"`, []string{"café"}},
		{"a lone surrogate becomes the replacement rune", `"\ud800"`, []string{"�"}},
		{"a literal newline is itself", "\"a\nb\"", []string{"a\nb"}},
		{"interpolation pieces hold only their literal text", `"a${x}b${y}c"`,
			[]string{"a", "x", "b", "y", "c"}},
		{"an interpolation at each end", `"${x}mid${y}"`, []string{"", "x", "mid", "y", ""}},
		{"regex keeps its own escapes", `/a\d\/b/`, []string{`a\d/b`}},
		{"numbers and names are their text", `42 name`, []string{"42", "name"}},
		{"booleans and null are their text", `true null`, []string{"true", "null"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks := Lex("x.art", c.src)
			var got []string
			for _, tk := range toks {
				if tk.Kind == token.EOF {
					continue
				}
				got = append(got, tk.Value)
			}
			if len(got) != len(c.want) {
				t.Fatalf("Lex(%q) gave %d tokens %q, want %d %q", c.src, len(got), got, len(c.want), c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("token %d Value = %q, want %q", i, got[i], c.want[i])
				}
			}
			assertInvariants(t, c.src)
		})
	}
}

func TestLexErrors(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		codes []string
		// text is the offending token's text, empty for a marker.
		text string
	}{
		{"unclosed interpolation at end of file", `"${ foo`, []string{"unclosed-interpolation"}, ""},
		{"unclosed interpolation with nothing in it", `"${`, []string{"unclosed-interpolation"}, ""},
		{"unterminated string", `"abc`, []string{"unterminated-string"}, `"abc`},
		{"unterminated string after an interpolation", `"a${x}b`, []string{"unterminated-string"}, "}b"},
		{"unterminated regex at a newline", "/abc\n", []string{"unterminated-regex"}, "/abc"},
		{"unterminated regex at end of file", "/abc", []string{"unterminated-regex"}, "/abc"},
		{"unknown escape", `"a\qb"`, []string{"invalid-escape"}, ""},
		{"a backslash at end of file", `"a\`, []string{"invalid-escape", "unterminated-string"}, ""},
		{"short unicode escape", `"\u12"`, []string{"invalid-escape"}, ""},
		{"stray character", "a ! b", []string{"unexpected-character"}, "!"},
		{"stray non-ascii character", "a § b", []string{"unexpected-character"}, "§"},
		{"a bare dollar brace outside a string", "${x}", []string{"unexpected-character"}, "$"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks := Lex("x.art", c.src)

			var codes []string
			var first *token.Token
			for i, tk := range toks {
				if tk.Kind == token.Invalid {
					codes = append(codes, tk.Code)
					if first == nil {
						first = &toks[i]
					}
				}
			}
			if strings.Join(codes, ",") != strings.Join(c.codes, ",") {
				t.Fatalf("Lex(%q) gave codes %q, want %q\n%s", c.src, codes, c.codes, shape(toks))
			}
			if first.Message == "" {
				t.Error("an Invalid token must carry a message for a human")
			}
			if first.Text != c.text {
				t.Errorf("Invalid token text = %q, want %q", first.Text, c.text)
			}

			// However broken the input, the stream still ends in exactly one
			// EOF and still reproduces the source.
			assertEndsInOneEOF(t, toks)
			assertInvariants(t, c.src)
		})
	}
}

// A lexer that stopped at the first error would make "all errors in a file are
// reported" impossible, so recovery is asserted directly: real tokens after the
// fault.
func TestLexRecoversAndKeepsGoing(t *testing.T) {
	src := lines(
		`header "A" = "bad escape: \q"`,
		`header "B" = /unterminated`,
		`header "C" = 1 ! 2`,
		`header "D" = 3`,
	)
	toks := Lex("x.art", src)

	var codes, names []string
	for _, tk := range toks {
		if tk.Kind == token.Invalid {
			codes = append(codes, tk.Code)
		}
		if tk.Kind == token.Ident {
			names = append(names, tk.Value)
		}
	}
	wantCodes := []string{"invalid-escape", "unterminated-regex", "unexpected-character"}
	if strings.Join(codes, ",") != strings.Join(wantCodes, ",") {
		t.Errorf("codes = %q, want %q", codes, wantCodes)
	}
	if want := 4; len(names) != want {
		t.Errorf("got %d identifiers %q, want %d: the lexer stopped early", len(names), names, want)
	}
	assertEndsInOneEOF(t, toks)
	assertInvariants(t, src)
}

func TestLexTriviaAttachment(t *testing.T) {
	src := lines(
		`# leading comment`,
		``,
		`scenario "x" {  # trailing comment`,
		`  var a = 1`,
		``,
		`  var b = 2`,
		`}`,
	)
	toks := Lex("x.art", src)

	// The file-opening comment can only belong to the first token.
	first := toks[0]
	if first.Value != "scenario" {
		t.Fatalf("first token = %q, want scenario\n%s", first.Text, shape(toks))
	}
	if got, want := triviaShape(first.Leading), "comment newline newline"; got != want {
		t.Errorf("leading trivia of `scenario` = %q, want %q", got, want)
	}
	// The comment and blank line above it count as a separator, which is what
	// StartsLine means. A file whose very first byte is a token has nothing to
	// be separated from and reports false; see the method's doc comment.
	if !first.StartsLine() {
		t.Error("a newline does separate `scenario` from the comment above it")
	}
	if Lex("x.art", "scenario")[0].StartsLine() {
		t.Error("the first token of a file has nothing before it to be separated from")
	}

	// A comment after `{` on the same line trails the `{`.
	brace := find(t, toks, token.LBrace)
	if got, want := triviaShape(brace.Trailing), "whitespace comment"; got != want {
		t.Errorf("trailing trivia of `{` = %q, want %q", got, want)
	}

	// The blank line between the two vars shows up as two newlines on the
	// second one, which is how a printer knows to keep the gap.
	second := nthIdent(t, toks, "var", 2)
	if got, want := triviaShape(second.Leading), "newline newline whitespace"; got != want {
		t.Errorf("leading trivia of the second `var` = %q, want %q", got, want)
	}
	if !second.StartsLine() {
		t.Error("the second `var` starts a line")
	}

	assertInvariants(t, src)
}

// Trivia at the end of a file has only the EOF token to live on, which is the
// reason EOF carries trivia at all.
func TestEOFCarriesTrailingTrivia(t *testing.T) {
	src := "scenario\n\n# the last word\n"
	toks := Lex("x.art", src)
	eof := toks[len(toks)-1]
	if eof.Kind != token.EOF {
		t.Fatalf("last token is %v", eof.Kind)
	}
	if got, want := triviaShape(eof.Leading), "newline newline comment newline"; got != want {
		t.Errorf("leading trivia of EOF = %q, want %q", got, want)
	}
	assertInvariants(t, src)
}

func TestLineEndings(t *testing.T) {
	// CRLF is one terminator, so the same file with either ending has the same
	// blank lines -- and both round-trip.
	for _, src := range []string{"a\n\nb\n", "a\r\n\r\nb\r\n", "a\r\rb\r"} {
		toks := Lex("x.art", src)
		b := nthIdent(t, toks, "b", 1)
		if got, want := triviaShape(b.Leading), "newline newline"; got != want {
			t.Errorf("src %q: leading trivia of b = %q, want %q", src, got, want)
		}
		assertInvariants(t, src)
	}
}

func TestByteOrderMarkIsTrivia(t *testing.T) {
	src := "\ufeffscenario \"x\" {}"
	toks := Lex("x.art", src)
	if got, want := triviaShape(toks[0].Leading), "whitespace"; got != want {
		t.Errorf("leading trivia = %q, want %q", got, want)
	}
	if toks[0].Value != "scenario" {
		t.Errorf("first token = %q, want scenario", toks[0].Text)
	}
	assertInvariants(t, src)
}

// -- helpers --

func triviaShape(trivia []token.Trivia) string {
	var parts []string
	for _, tr := range trivia {
		parts = append(parts, tr.Kind.String())
	}
	return strings.Join(parts, " ")
}

func find(t *testing.T, toks []token.Token, kind token.Kind) token.Token {
	t.Helper()
	for _, tk := range toks {
		if tk.Kind == kind {
			return tk
		}
	}
	t.Fatalf("no %v token in the stream", kind)
	return token.Token{}
}

func nthIdent(t *testing.T, toks []token.Token, name string, n int) token.Token {
	t.Helper()
	seen := 0
	for _, tk := range toks {
		if tk.Kind == token.Ident && tk.Value == name {
			seen++
			if seen == n {
				return tk
			}
		}
	}
	t.Fatalf("fewer than %d %q identifiers in the stream", n, name)
	return token.Token{}
}

func assertEndsInOneEOF(t *testing.T, toks []token.Token) {
	t.Helper()
	if len(toks) == 0 {
		t.Fatal("empty token stream")
	}
	for i, tk := range toks {
		if tk.Kind == token.EOF && i != len(toks)-1 {
			t.Errorf("EOF at index %d of %d", i, len(toks))
		}
	}
	if last := toks[len(toks)-1]; last.Kind != token.EOF {
		t.Errorf("stream ends in %v, not EOF", last.Kind)
	}
}
