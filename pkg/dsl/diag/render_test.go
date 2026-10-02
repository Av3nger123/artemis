package diag

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/dsl/lexer"
	"artemis/pkg/dsl/token"
)

// -update rewrites the golden files from what the renderers actually print:
//
//	go test ./pkg/dsl/diag -update
//
// or `make golden`. Read the resulting diff before committing it -- these
// goldens exist to be read as output a person would receive, and one that is
// regenerated without being read asserts nothing.
var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// renderCase is one set of diagnostics pinned to two golden files:
// testdata/<name>.golden is the terminal rendering and testdata/<name>.json is
// the structured one. Both sides of the same input, so a change to either
// renderer shows up as a diff against the other's unchanged file.
type renderCase struct {
	name string
	// files are the fixtures the renderer may echo, by name. A case with none
	// is a case about rendering a diagnostic whose source nobody has.
	files []string
	// build returns the diagnostics. It takes the loaded sources so a case can
	// run the real lexer over one.
	build func(t *testing.T, src map[string]string) []Diagnostic
}

func TestRender(t *testing.T) {
	cases := []renderCase{{
		// The design document's Diagnostics section, as a golden. If this
		// diff ever changes, docs/artemis-dsl-design.md changed with it or
		// the renderer broke its promise.
		name:  "doc_example",
		files: []string{"checkout.art"},
		build: func(t *testing.T, src map[string]string) []Diagnostic {
			b := New()
			b.Error(span("checkout.art", 12, 10, 12, 15, offsetOf(t, src, "checkout.art", 12, 10)),
				UnknownField, "unknown field %q", "statu").
				DidYouMean("statu", []string{"status", "body", "raw", "headers"})
			b.Error(span("checkout.art", 31, 10, 31, 16, offsetOf(t, src, "checkout.art", 31, 10)),
				NotInScope, "%q is not in scope in a browser step", "status").
				Hintf("a browser step binds page.url, page.title, text(), value(),\nattr(), count(), visible()")
			return b.All()
		},
	}, {
		// A whole lexical pass: five faults in one file, reported in file
		// order, through the FromToken bridge rather than by hand.
		name:  "lexical",
		files: []string{"lexical.art", "interp.art"},
		build: func(t *testing.T, src map[string]string) []Diagnostic {
			b := New()
			// Lexed in the opposite order to the one they must print in, so
			// All's sort by file then offset is what produces the golden
			// rather than the order the caller happened to use.
			b.AddTokens(lexer.Lex("lexical.art", src["lexical.art"]))
			b.AddTokens(lexer.Lex("interp.art", src["interp.art"]))
			if b.Len() < 5 {
				t.Fatalf("lexer reported %d invalid tokens, want the whole set", b.Len())
			}
			return b.All()
		},
	}, {
		// Spans that cross lines: a five-line block, and a run longer than
		// the renderer echoes, which elides its middle.
		name:  "multiline",
		files: []string{"block.art"},
		build: func(t *testing.T, src map[string]string) []Diagnostic {
			b := New()
			b.Error(span("block.art", 7, 14, 10, 8, offsetOf(t, src, "block.art", 7, 14)),
				UnknownField, "a body block is not an object literal").
				Hintf("write the fields as `body { \"sku\" = \"A-1\" }`").
				Suggest("body {\n  \"sku\" = \"A-1\"\n}")
			b.Error(span("block.art", 17, 14, 24, 8, offsetOf(t, src, "block.art", 17, 14)),
				UnknownField, "every element of args must be a string")
			return b.All()
		},
	}, {
		// Degradation, which is the part that has to not panic: a file the
		// renderer was never given, a line past the end of one it was, a
		// zero-width span at the end of a line, and a span with no position
		// at all -- what a node built in Go rather than read from a file has.
		name:  "no_source",
		files: []string{"lexical.art"},
		build: func(t *testing.T, src map[string]string) []Diagnostic {
			b := New()
			b.Error(span("absent.art", 3, 5, 3, 9, 40), UnknownField, "a file nobody read")
			b.Error(span("lexical.art", 900, 1, 900, 2, 99999), NotInScope, "a line past the end")
			b.Error(span("lexical.art", 4, 20, 4, 20, 0), UnexpectedCharacter, "a zero-width span still gets one caret")
			b.Warn(token.Span{}, NotInScope, "a diagnostic with no position at all")
			return b.All()
		},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := map[string]string{}
			files := NewFiles()
			for _, name := range c.files {
				b, err := os.ReadFile(filepath.Join("testdata", name))
				if err != nil {
					t.Fatalf("reading fixture: %v", err)
				}
				src[name] = string(b)
				files.Add(name, string(b))
			}
			diags := c.build(t, src)
			checkGolden(t, c.name+".golden", TerminalString(files, diags))
			checkGolden(t, c.name+".json", JSONString(diags))
		})
	}
}

// TestDocExampleIsVerbatim pins the first block of the design document's
// Diagnostics section as a literal, separately from the golden.
//
// The golden proves the renderer is stable; this proves it still agrees with
// the document that specified it. A golden can be regenerated by anyone; this
// has to be edited by someone who has decided to change the specified format.
func TestDocExampleIsVerbatim(t *testing.T) {
	const want = `checkout.art:12:10: unknown field "statu"
   12 |   expect statu == 200
      |          ^^^^^
   hint: did you mean "status"?
`
	src, err := os.ReadFile(filepath.Join("testdata", "checkout.art"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	files := NewFiles()
	files.Add("checkout.art", string(src))

	b := New()
	b.Error(span("checkout.art", 12, 10, 12, 15, offsetOf(t, map[string]string{"checkout.art": string(src)}, "checkout.art", 12, 10)),
		UnknownField, "unknown field %q", "statu").
		DidYouMean("statu", []string{"status", "body", "raw", "headers"})

	if got := TerminalString(files, b.All()); got != want {
		t.Errorf("terminal rendering drifted from docs/artemis-dsl-design.md\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestMessageRewordDoesNotChangeCode is the first of the two invariants: a
// message is prose and a code is an interface. Rewording one must leave the
// other alone, in both renderings.
func TestMessageRewordDoesNotChangeCode(t *testing.T) {
	first, second := New(), New()
	s := span("x.art", 1, 1, 1, 4, 0)
	first.Error(s, UnknownField, "unknown field %q", "statu")
	second.Error(s, UnknownField, "no field named %q here -- reworded for clarity", "statu")

	a, b := first.All()[0], second.All()[0]
	if a.Code != b.Code {
		t.Fatalf("rewording changed the code: %q then %q", a.Code, b.Code)
	}
	if !strings.Contains(JSONString(first.All()), `"code": "unknown-field"`) ||
		!strings.Contains(JSONString(second.All()), `"code": "unknown-field"`) {
		t.Error("the JSON rendering of a reworded message lost the code")
	}
}

// span builds a span, which the cases above do often enough to be worth one
// line rather than six fields.
func span(file string, line, col, endLine, endCol, offset int) token.Span {
	return token.Span{File: file, Line: line, Col: col, EndLine: endLine, EndCol: endCol, Offset: offset}
}

// offsetOf is the byte offset of a 1-based line and column in a fixture, so a
// hand-built span's offset agrees with its line and column -- Bag.All sorts by
// offset, and a case that got it wrong would pass while asserting the wrong
// order.
func offsetOf(t *testing.T, src map[string]string, file string, line, col int) int {
	t.Helper()
	s, ok := src[file]
	if !ok {
		t.Fatalf("offsetOf: no fixture %q", file)
	}
	off := 0
	for n := 1; n < line; n++ {
		i := strings.IndexByte(s[off:], '\n')
		if i < 0 {
			t.Fatalf("offsetOf: %s has no line %d", file, line)
		}
		off += i + 1
	}
	return off + col - 1
}

// checkGolden compares got with testdata/<name>, or rewrites it under -update.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run `go test ./pkg/dsl/diag -update` to create it)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s does not match\n got:\n%s\nwant:\n%s", path, got, want)
	}
}
