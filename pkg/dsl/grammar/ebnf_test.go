package grammar

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// terminalsInEBNF are the quoted literals of grammar.ebnf. EBNF comments are
// stripped first so a word inside (* ... *) cannot be mistaken for one.
func terminalsInEBNF(t *testing.T) map[string]bool {
	t.Helper()
	src := regexp.MustCompile(`(?s)\(\*.*?\*\)`).ReplaceAllString(EBNF(), "")
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(src, -1) {
		out[m[1]] = true
	}
	return out
}

// wordTables are the token tables whose every entry has to appear in the EBNF
// as a terminal. Comparisons is here too, and it is the one whose entries are
// mostly symbols -- which is the point: == is as much a thing the grammar has
// to spell as `contains` is.
//
// The map is keyed by the table's Go name so a failure names the variable to
// go and look at.
func wordTables() map[string][]string {
	return map[string][]string{
		"Methods":             token.Methods,
		"Actions":             token.Actions,
		"BrowserActions":      token.BrowserActions,
		"TypeNames":           token.TypeNames,
		"WordOperators":       token.WordOperators,
		"Comparisons":         token.Comparisons,
		"Blocks":              token.Blocks,
		"StepStatements":      token.StepStatements,
		"RequestFields":       token.RequestFields,
		"RunFields":           token.RunFields,
		"RetryFields":         token.RetryFields,
		"StepFields":          token.StepFields,
		"ConfigBlocks":        token.ConfigBlocks,
		"BrowserConfigFields": token.BrowserConfigFields,
		"Builtins":            token.Builtins,
		"Reserved":            token.Reserved,
	}
}

// TestEveryTableWordIsATerminal is half of the issue's drift gate. A browser
// action or a comparison operator added to a token table and not written into
// the grammar would ship an EBNF that is quietly incomplete -- and the EBNF is
// the mitigation for "a model has never seen this syntax", so incomplete means
// the model writes a file artemis rejects.
func TestEveryTableWordIsATerminal(t *testing.T) {
	terminals := terminalsInEBNF(t)
	for table, words := range wordTables() {
		for _, w := range words {
			if !terminals[w] {
				t.Errorf("token.%s has %q, which appears in no production of grammar.ebnf", table, w)
			}
		}
	}
}

// TestEveryTerminalIsInATable is the other direction: a word spelled in the
// grammar that no table holds is a word the parser and the checker have never
// heard of, so the grammar is describing a language artemis does not accept.
//
// Only word terminals are reconciled. The punctuation -- braces, brackets,
// "=", ":", ".", "-" -- is structure rather than vocabulary and belongs to no
// table, and the comparison symbols reach a table through Comparisons.
func TestEveryTerminalIsInATable(t *testing.T) {
	word := regexp.MustCompile(`^[a-z][a-z_]+$`)
	inSomeTable := map[string]string{}
	for table, words := range wordTables() {
		for _, w := range words {
			inSomeTable[w] = table
		}
	}
	var orphans []string
	for term := range terminalsInEBNF(t) {
		if !word.MatchString(term) {
			continue
		}
		if _, ok := inSomeTable[term]; !ok {
			orphans = append(orphans, term)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("grammar.ebnf spells %v, which no pkg/dsl/token table holds", orphans)
	}
}

// TestDocumentIsSelfContained pins what `artemis grammar` has to carry for the
// text to be worth pasting into a prompt: the productions, the lexical tokens
// the productions treat as atoms, the precedence the productions only imply,
// the post-parse rules, the scopes, and an example.
func TestDocumentIsSelfContained(t *testing.T) {
	doc := Document()
	for _, want := range []string{
		"GRAMMAR", "File          = { Scenario } ;",
		"LEXICAL TOKENS", "SEPARATORS", "PRECEDENCE", "WHAT THE GRAMMAR DOES NOT SAY",
		"SCOPES", "EXAMPLE",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("artemis grammar does not mention %q", want)
		}
	}
	if !strings.HasSuffix(doc, "\n") {
		t.Error("the document does not end in a newline")
	}
}

// TestScopesSectionComesFromTheChecker fails if the generated scopes drift
// from check's answer -- which they cannot, since they are read from it, but
// the assertion is what says so to the next reader.
func TestScopesSectionComesFromTheChecker(t *testing.T) {
	doc := Document()
	for _, st := range check.StepTypes() {
		for _, root := range check.Roots(st) {
			if !strings.Contains(doc, root) {
				t.Errorf("%s step binds %q, which the document never names", st, root)
			}
		}
		for _, fn := range check.Functions(st) {
			if !strings.Contains(doc, fn+"()") {
				t.Errorf("%s step may call %s(), which the document never names", st, fn)
			}
		}
	}
}

// TestTheExampleCompiles is the claim the example makes by being in the
// document: this is a file artemis accepts. A worked example that does not
// parse is worse than none, because it is what a model will imitate.
func TestTheExampleCompiles(t *testing.T) {
	tree, bag := parser.Parse("example.art", example)
	_, checked := check.Check(tree)
	bag.Merge(checked)
	for _, d := range bag.All() {
		t.Errorf("example.art: %s:%d:%d: %s: %s", d.Span.File, d.Span.Line, d.Span.Col, d.Code, d.Message)
	}
}

// TestTheExampleUsesTheLanguage keeps the example from decaying into a
// three-line scenario: it has to show all three step types and the statements
// a reader needs to see used, because a grammar plus a trivial example is how
// a model ends up writing only the trivial case.
func TestTheExampleUsesTheLanguage(t *testing.T) {
	for _, want := range []string{
		"config browser", "var ", "capture ", "retry {", "timeout =", "within ",
		"${", "env(", "match(", "text(", "visible(", "#", " is ", " exists", " not ",
		"get ", "post ", "run \"", "browser {",
	} {
		if !strings.Contains(example, want) {
			t.Errorf("the example never shows %q", want)
		}
	}
}
