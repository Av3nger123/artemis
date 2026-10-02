package lexer

import (
	"os"
	"path/filepath"
	"testing"

	"artemis/pkg/dsl/token"
)

// The invariant this whole package exists to keep:
//
//	token.Source(Lex(file, src)) == src
//
// for every input. It is asserted here over a corpus of files and a long list
// of fragments, from every table-driven test in the package, and from the
// fuzzer. It is tested now rather than in ART-34 because a lexer that drops a
// byte cannot be fixed later without being rewritten, and pkg/dsl/print, the
// `ast --from-json` round trip and subsystem D's UI all rest on it.
func assertInvariants(t *testing.T, src string) {
	t.Helper()
	toks := Lex("x.art", src)

	if got := token.Source(toks); got != src {
		t.Errorf("round trip lost bytes:\n got %q\nwant %q\n%s", got, src, shape(toks))
	}
	assertEndsInOneEOF(t, toks)
	assertSpansDescribeTheirText(t, "x.art", src, toks)
}

// fragments is every lexical shape worth putting through the invariants at
// once: the valid, the invalid, the empty and the merely strange. New cases go
// here, where all three checks see them, rather than into a test of their own.
var fragments = []string{
	// Nothing, and nothing but trivia.
	"",
	" ",
	"\n",
	"\n\n\n",
	"\t  \t",
	"#",
	"# a comment with no newline",
	"# a comment\n",
	"\n# indented after a blank line\n  # and another\n",
	"\ufeff",
	"\ufeff# a comment after a byte-order mark\n",
	"\r",
	"\r\n",
	"a\rb",
	"a\r\nb",
	"a\n\rb",

	// Whole declarations.
	`scenario "x" {}`,
	"scenario \"x\" {\n  var a = 1\n}\n",
	"scenario \"x\" { # trailing\n}\n",
	`config browser { headless = true, viewport = "1280x720" }`,
	`var pw = env("API_PASSWORD")`,
	`step "login" { post "/token" { body = {"u": "a"} } }`,
	`run "psql" { args = ["-f", "seed.sql"], cwd = "db", env { PGPASSWORD = pw } }`,
	`browser { goto "/x"` + "\n" + `fill "#email" = "a@b.c"` + "\n" + `click "text=Sign in" }`,
	`retry { times = 3, delay = "2s" }`,
	`timeout = "5s"`,
	`expect page.url contains "/billing" within "10s"`,
	`expect not body.x exists`,
	`expect body.count is number`,
	`expect body.roles contains "admin" and status == 200`,
	`capture token = body.data.access_token`,

	// Literals.
	"0", "42", "3.5", "1e9", "1E-9", "1.", "1e", "0.0.0",
	"-1", "- 1", "--1",
	"true", "false", "null", "truely", "nullable",
	`""`, `"a"`, `" "`, `"\n"`, `"\\"`, `"\""`, `"\/"`, `"\$"`, `"\t\r"`,
	`"café"`, `"\ud800"`, `"\u0041"`,
	"\"multi\nline\"",
	"\"multi\r\nline\"",
	`"$"`, `"$$"`, `"$5.00"`, `"a$"`,
	`"${x}"`, `"a${x}"`, `"${x}b"`, `"a${x}b"`, `"${a}${b}"`, `"${a}m${b}"`,
	`"${ body.data.id }"`,
	`"${ env("X") }"`,
	`"${ text("${sel}") }"`,
	`"${ f({"a": [1, 2]}) }"`,
	`"${ "${ "${deep}" }" }"`,
	"\"${\n x \n}\"",
	`/./`, `//`, `/a\/b/`, `/[a-z]+@[a-z]+/`, `/\d{3}/`,

	// Operators and delimiters, alone and jammed together.
	"== != <= >= < > = - . , : { } ( ) [ ]",
	"==!=<=>=<>=-.,:{}()[]",
	"a==b", "a!=b", "a<=b", "a>=b", "a<b", "a>b",
	"body.data[0].id",
	`f(1, "a", /re/, true, null, {"k": []})`,

	// Invalid: each error, and errors next to each other.
	`"`,
	`"abc`,
	`"abc\`,
	`"a\q"`,
	`"\u"`, `"\u1"`, `"\u12g4"`,
	`"${`,
	`"${ foo`,
	`"a${x}b`,
	`"${ "nested`,
	"/", "/abc", "/abc\n", `/a\`,
	"!", "a ! b", "!!", "§", "€uro", "\x00", "\x01\x02",
	"${x}",
	"@#$%",
	"}",
	"}}}",
	`"${ } }"`,
	`"${ {} }"`,
	"\xff\xfe",
	"a\xffb",
}

func TestRoundTripOverFragments(t *testing.T) {
	for _, src := range fragments {
		t.Run(src, func(t *testing.T) { assertInvariants(t, src) })
	}
}

type corpusFile struct {
	name string
	src  string
}

// corpus is every .art file in testdata: the design document's worked example
// verbatim, a file that is nothing but awkward trivia, one that exercises
// interpolation, and one that is a pile of lexical errors.
func corpus(t *testing.T) []corpusFile {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "*.art"))
	if err != nil {
		t.Fatalf("glob testdata: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no .art files in testdata")
	}
	files := make([]corpusFile, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		files = append(files, corpusFile{name: filepath.Base(p), src: string(b)})
	}
	return files
}

func TestRoundTripOverCorpus(t *testing.T) {
	for _, f := range corpus(t) {
		t.Run(f.name, func(t *testing.T) {
			toks := Lex(f.name, f.src)
			if got := token.Source(toks); got != f.src {
				t.Errorf("round trip of %s lost bytes", f.name)
				reportFirstDifference(t, f.src, got)
			}
			assertEndsInOneEOF(t, toks)
			assertSpansDescribeTheirText(t, f.name, f.src, toks)
		})
	}
}

// The worked example from the design document must lex clean. If the grammar
// and the lexer disagree about what the language looks like, this is where it
// shows, and the file is a verbatim copy so it cannot quietly drift.
func TestTheWorkedExampleHasNoLexicalErrors(t *testing.T) {
	src := readCorpusFile(t, "checkout.art")
	for _, tk := range Lex("checkout.art", src) {
		if tk.Kind == token.Invalid {
			t.Errorf("%s:%d:%d: %s (%s)", "checkout.art", tk.Span.Line, tk.Span.Col, tk.Message, tk.Code)
		}
	}
}

func TestTheInterpolationCorpusHasNoLexicalErrors(t *testing.T) {
	src := readCorpusFile(t, "interpolation.art")
	for _, tk := range Lex("interpolation.art", src) {
		if tk.Kind == token.Invalid {
			t.Errorf("interpolation.art:%d:%d: %s (%s)", tk.Span.Line, tk.Span.Col, tk.Message, tk.Code)
		}
	}
}

// The invalid corpus pins which codes a file of mistakes produces, in order.
// Recovery is the point: one error must not swallow the ones after it.
func TestTheInvalidCorpusReportsEveryError(t *testing.T) {
	src := readCorpusFile(t, "invalid.art")
	var codes []string
	for _, tk := range Lex("invalid.art", src) {
		if tk.Kind == token.Invalid {
			codes = append(codes, tk.Code)
		}
	}
	want := []string{
		"invalid-escape",
		"unterminated-regex",
		"unexpected-character",
		"unclosed-interpolation",
	}
	if len(codes) != len(want) {
		t.Fatalf("codes = %q, want %q", codes, want)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Errorf("code %d = %q, want %q", i, codes[i], want[i])
		}
	}
}

func readCorpusFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// reportFirstDifference keeps a lost-byte failure readable when the file is
// fifty lines long: the offset and a window either side, not two whole files.
func reportFirstDifference(t *testing.T, want, got string) {
	t.Helper()
	for i := 0; i < len(want) && i < len(got); i++ {
		if want[i] != got[i] {
			lo := i - 20
			if lo < 0 {
				lo = 0
			}
			hi := i + 20
			t.Errorf("first difference at offset %d:\n want %q\n  got %q",
				i, want[lo:min(hi, len(want))], got[lo:min(hi, len(got))])
			return
		}
	}
	t.Errorf("one is a prefix of the other: want %d bytes, got %d", len(want), len(got))
}
