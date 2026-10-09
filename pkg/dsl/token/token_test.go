package token

import "testing"

func TestSourceIsTokenAndTrivia(t *testing.T) {
	tok := Token{
		Kind: Ident,
		Text: "status",
		Leading: []Trivia{
			{Kind: Newline, Text: "\n"},
			{Kind: Whitespace, Text: "  "},
		},
		Trailing: []Trivia{
			{Kind: Whitespace, Text: " "},
			{Kind: Comment, Text: "# the response code"},
		},
	}
	want := "\n  status # the response code"
	if got := tok.Source(); got != want {
		t.Errorf("Source() = %q, want %q", got, want)
	}
}

func TestSourceOverAStream(t *testing.T) {
	toks := []Token{
		{Kind: Ident, Text: "var", Trailing: []Trivia{{Kind: Whitespace, Text: " "}}},
		{Kind: Ident, Text: "x"},
		{Kind: EOF, Leading: []Trivia{{Kind: Newline, Text: "\n"}}},
	}
	if got, want := Source(toks), "var x\n"; got != want {
		t.Errorf("Source() = %q, want %q", got, want)
	}
}

func TestStartsLine(t *testing.T) {
	cases := []struct {
		name    string
		leading []Trivia
		want    bool
	}{
		{"no trivia", nil, false},
		{"only spaces", []Trivia{{Kind: Whitespace, Text: " "}}, false},
		{"a newline", []Trivia{{Kind: Newline, Text: "\n"}}, true},
		{"newline then indent", []Trivia{{Kind: Newline, Text: "\n"}, {Kind: Whitespace, Text: "  "}}, true},
		{"a comment on the same line", []Trivia{{Kind: Comment, Text: "# hi"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (Token{Leading: c.leading}).StartsLine(); got != c.want {
				t.Errorf("StartsLine() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsMarker(t *testing.T) {
	marker := Token{Kind: Invalid, Code: "unclosed-interpolation"}
	if !marker.IsMarker() {
		t.Error("a text-less Invalid token should be a marker")
	}
	consuming := Token{Kind: Invalid, Text: "!", Code: "unexpected-character"}
	if consuming.IsMarker() {
		t.Error("an Invalid token that consumed source should not be a marker")
	}
	if (Token{Kind: Ident, Text: "x"}).IsMarker() {
		t.Error("an Ident should not be a marker")
	}
}

func TestSpanIsZero(t *testing.T) {
	if !(Span{}).IsZero() {
		t.Error("the zero Span should be zero")
	}
	if (Span{Line: 1, Col: 1}).IsZero() {
		t.Error("a located Span should not be zero")
	}
}

func TestViaDoesNotMakeASpanNonZero(t *testing.T) {
	if !(Span{Via: 3}).IsZero() {
		t.Fatal("a span with only Via set locates nothing and must stay zero")
	}
}

func TestUseLinesAreTheOverrideWords(t *testing.T) {
	for _, w := range []string{"header", "query", "body", "drop", "expect", "in"} {
		if !IsUseLine(w) {
			t.Errorf("IsUseLine(%q) = false", w)
		}
	}
	if IsUseLine("user") {
		t.Error("IsUseLine(\"user\") = true; an argument name is not a use line")
	}
}

func TestKindStringsAreUnique(t *testing.T) {
	// A duplicated name would make a test failure name the wrong kind, which is
	// the only thing these strings are for.
	seen := map[string]Kind{}
	for kind, name := range names {
		if other, dup := seen[name]; dup {
			t.Errorf("kinds %d and %d are both %q", kind, other, name)
		}
		seen[name] = kind
	}
	if got := Minus.String(); got != "-" {
		t.Errorf("Minus.String() = %q", got)
	}
	if got := Kind(200).String(); got != "kind(200)" {
		t.Errorf("unnamed kind = %q", got)
	}
}

// The tables and their lookups are two spellings of one set, so a word in one
// and not the other is the bug worth testing for.
func TestLookupsAgreeWithTheirTables(t *testing.T) {
	cases := []struct {
		name  string
		words []string
		in    func(string) bool
	}{
		{"Methods", Methods, IsMethod},
		{"Actions", Actions, IsAction},
		{"BrowserActions", BrowserActions, IsBrowserAction},
		{"TypeNames", TypeNames, IsTypeName},
		{"WordOperators", WordOperators, IsWordOperator},
		{"Builtins", Builtins, IsBuiltin},
		{"Reserved", Reserved, IsReserved},
		{"UseLines", UseLines, IsUseLine},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if len(c.words) == 0 {
				t.Fatal("empty table")
			}
			for _, w := range c.words {
				if !c.in(w) {
					t.Errorf("%q is in %s but its lookup says no", w, c.name)
				}
			}
			if c.in("definitely_not_a_keyword") {
				t.Errorf("%s matched a word that is not in it", c.name)
			}
		})
	}
}

func TestReservedDoesNotCollideWithTheLanguage(t *testing.T) {
	// A word cannot be both reserved for a later tier and part of the grammar
	// today: the checker rejects reserved words, so a collision would reject
	// valid files.
	live := [][]string{
		Methods, Actions, BrowserActions, TypeNames, WordOperators,
		Blocks, StepStatements, RequestFields, RunFields, RetryFields,
		StepFields, ConfigBlocks, BrowserConfigFields, Builtins,
		CollectionItems,
	}
	for _, table := range live {
		for _, w := range table {
			if IsReserved(w) {
				t.Errorf("%q is both reserved and part of the grammar", w)
			}
		}
	}
}

func TestTriviaKindStrings(t *testing.T) {
	cases := map[TriviaKind]string{
		Whitespace: "whitespace",
		Newline:    "newline",
		Comment:    "comment",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Errorf("TriviaKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
	if got := TriviaKind(9).String(); got != "trivia(9)" {
		t.Errorf("unnamed trivia kind = %q", got)
	}
}

// TestIsComparisonAcceptsEveryComparison walks Comparisons rather than listing
// the operators, so an operator added to the table and not taught to the
// lexer, the parser or the checker fails here first.
func TestIsComparisonAcceptsEveryComparison(t *testing.T) {
	for _, c := range Comparisons {
		var tok Token
		if k, ok := kindOf(c); ok {
			tok = Token{Kind: k, Text: c}
		} else {
			tok = Token{Kind: Ident, Text: c, Value: c}
		}
		if !IsComparison(tok) {
			t.Errorf("IsComparison(%q) = false, want true: it is in Comparisons", c)
		}
	}
}

// TestComparisonsAreSymbolsOrWordOperators is the other half: an entry of
// Comparisons that is neither a Kind the lexer produces nor a word operator
// would be unreachable, because nothing would ever lex it.
func TestComparisonsAreSymbolsOrWordOperators(t *testing.T) {
	for _, c := range Comparisons {
		if _, ok := kindOf(c); ok {
			continue
		}
		if !IsWordOperator(c) {
			t.Errorf("Comparisons has %q, which is neither a token kind's spelling nor a word operator, so nothing can lex it", c)
		}
	}
}

// TestIsComparisonRejectsTheRest pins what is deliberately not a BinOp: the
// logical and postfix word operators, and `=`, which is assignment.
func TestIsComparisonRejectsTheRest(t *testing.T) {
	for _, w := range []string{"and", "or", "not", "exists", "is"} {
		if IsComparison(Token{Kind: Ident, Text: w, Value: w}) {
			t.Errorf("IsComparison(%q) = true; it is a word operator but not a comparison", w)
		}
	}
	for _, k := range []Kind{Assign, Minus, Dot, Colon, Comma, Ident, Number, String} {
		if IsComparison(Token{Kind: k}) {
			t.Errorf("IsComparison(%s) = true, want false", k)
		}
	}
}
