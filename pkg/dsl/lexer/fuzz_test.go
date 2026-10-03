package lexer

import (
	"testing"

	"artemis/pkg/dsl/token"
)

// FuzzLex asserts the two things that have to hold for arbitrary bytes, not
// just for the inputs someone thought to write down:
//
//  1. nothing panics -- a malformed scenario file produces diagnostics and a
//     non-zero exit, never a stack trace;
//  2. the round trip is exact, so no input exists that the lexer silently
//     loses a byte of.
//
// The parser gets its own fuzz target in ART-31. This one is the lexer's, and
// it is cheap to leave running because every seed is a one-line string.
func FuzzLex(f *testing.F) {
	for _, src := range fragments {
		f.Add(src)
	}
	for _, name := range []string{"checkout.art", "trivia.art", "interpolation.art", "invalid.art"} {
		f.Add(readCorpusFile(&testing.T{}, name))
	}

	f.Fuzz(func(t *testing.T, src string) {
		toks := Lex("fuzz.art", src)

		if got := token.Source(toks); got != src {
			t.Fatalf("round trip lost bytes:\n got %q\nwant %q", got, src)
		}

		if len(toks) == 0 || toks[len(toks)-1].Kind != token.EOF {
			t.Fatalf("stream does not end in EOF: %q", src)
		}
		for i, tk := range toks {
			if tk.Kind == token.EOF && i != len(toks)-1 {
				t.Fatalf("EOF at index %d of %d: %q", i, len(toks), src)
			}
			if tk.Kind == token.Invalid && tk.Message == "" {
				t.Fatalf("Invalid token with no message: %q", src)
			}
			// An offset past the end of the file would make a diagnostic
			// renderer slice out of range.
			if tk.Span.Offset < 0 || tk.Span.Offset+len(tk.Text) > len(src) {
				t.Fatalf("token %d span %+v outside a %d-byte file: %q", i, tk.Span, len(src), src)
			}
			if tk.Span.Line < 1 || tk.Span.Col < 1 {
				t.Fatalf("token %d span %+v is not 1-based: %q", i, tk.Span, src)
			}
		}
	})
}
