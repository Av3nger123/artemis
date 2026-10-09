// Package parser turns the tokens pkg/dsl/lexer produces into the concrete
// tree pkg/dsl/ast defines: recursive descent for blocks, precedence climbing
// for expressions.
//
// Three properties hold for every input, valid or not, and the tests here
// assert all three against a native fuzz target:
//
//  1. Nothing panics. A malformed file produces diagnostics and a non-zero
//     exit, never a stack trace.
//  2. A tree always comes back. The parser recovers at statement boundaries,
//     so one bad line does not swallow the file and the checker can still
//     report on the parts that parsed.
//  3. ast.Source(tree) == src, byte for byte, because every non-marker token
//     is held by exactly one node.
//
// The division of labour with pkg/dsl/check is worth stating, because it is
// what keeps diagnostics good: the parser is **shape-driven** and the checker
// is **name-driven**. A field position accepts any identifier, so `statu = 3`
// parses into an ast.Field and becomes "unknown field, did you mean status"
// with a mechanical fix. Rejecting it here would turn a hintable name error
// into a bare syntax error with nothing to suggest. The parser reports only
// what the grammar's shape requires: a missing `=`, an unclosed brace, a token
// where an expression was wanted.
package parser

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/lexer"
	"artemis/pkg/dsl/token"
)

// Parse lexes and parses src. It returns a non-nil tree and a bag holding
// every lexical and syntactic diagnostic, in file order.
//
// The bag is the caller's signal: bag.HasErrors() is what a command turns into
// a non-zero exit status. The tree comes back either way, because ART-33 can
// still resolve names in the parts of a broken file that parsed, and reporting
// a scope error alongside a syntax error is strictly more useful than
// reporting the syntax error alone.
func Parse(file, src string) (*ast.File, *diag.Bag) {
	toks := lexer.Lex(file, src)
	bag := diag.New()
	bag.AddTokens(toks)
	return newParser(toks, bag).parseFile(), bag
}

// ParseExpr parses src as a single expression, for a UI validating one field
// live -- a URL box holds `"${base}/users/${id}"`, which is an expression --
// and for tests that want one node without a scenario around it.
//
// Trailing tokens after the expression are a diagnostic, not silence, so a
// field containing `a b` is reported rather than half-accepted.
func ParseExpr(file, src string) (ast.Expr, *diag.Bag) {
	toks := lexer.Lex(file, src)
	bag := diag.New()
	bag.AddTokens(toks)

	p := newParser(toks, bag)
	x := p.parseExpr()
	if !p.at(token.EOF) {
		p.errorf(p.peek(), diag.UnexpectedToken,
			"unexpected %s after the expression", describe(p.peek()))
	}
	return x, bag
}

// parser is a cursor over the token stream plus the bag it reports into.
type parser struct {
	toks []token.Token
	i    int
	bag  *diag.Bag

	// nest is how deep the descent currently is, counting both expression
	// nesting and nested blocks. See enter.
	nest int

	// tooDeep records that the nesting limit was already reported, so an
	// absurd file produces one diagnostic about it rather than one per level.
	tooDeep bool

	// depth counts the brackets open around the cursor: (, [, and a literal {.
	// Newlines separate statements only at depth zero, which is what lets
	// `body = {` open a multi-line object literal while `expect a` followed by
	// a line starting `and b` stays two statements. See atBoundary.
	depth int

	// useLines counts the use and in blocks open around the cursor, where a
	// comma between lines is the usual spelling: a missing separator's hint
	// offers it there.
	useLines int
}

func newParser(toks []token.Token, bag *diag.Bag) *parser {
	p := &parser{toks: toks, bag: bag}
	p.skipMarkers()
	return p
}

// peek is the token at the cursor. The stream always ends in exactly one EOF,
// so this is never out of range -- a guarantee lexer.Lex makes and its fuzz
// target holds it to.
func (p *parser) peek() token.Token {
	if p.i >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.i]
}

// next returns the token at the cursor and advances past it. At EOF it returns
// EOF without advancing, which is what makes every loop in the parser
// terminate: a caller that cannot make progress has to check at(token.EOF),
// and all of them do.
func (p *parser) next() token.Token {
	t := p.peek()
	if t.Kind != token.EOF {
		p.i++
		p.skipMarkers()
	}
	return t
}

// skipMarkers advances past position-only Invalid tokens.
//
// A marker has empty Text and only a position -- an unclosed ${, a bad escape
// inside an otherwise fine string -- and its diagnostic is already in the bag
// from AddTokens. It contributes nothing to the source, which is why dropping
// it from the tree keeps the round trip exact. A *consuming* Invalid token is
// not skipped: it read real bytes, so it has to land in an ast.Bad.
func (p *parser) skipMarkers() {
	for p.i < len(p.toks) && p.toks[p.i].IsMarker() {
		p.i++
	}
}

func (p *parser) at(k token.Kind) bool { return p.peek().Kind == k }

// atWord reports whether the cursor is on an identifier with this text.
//
// The grammar has no lexical keywords: `scenario`, `get`, `body`, `contains`
// and `within` are all legal names, because `capture body = ...` and `var
// status = env("S")` are files someone will write. So every keyword match in
// this package goes through the token's text, and the enumerable word sets
// live in token/tables.go rather than being spelled out here.
func (p *parser) atWord(word string) bool {
	t := p.peek()
	return t.Kind == token.Ident && t.Value == word
}

// peekWordAt reports whether the nth token after the cursor is an identifier
// with this text, counting the cursor itself as zero.
//
// One token of lookahead is what a positional modifier costs. `secret` opens a
// declaration only when `var` or `capture` follows it, and `var secret = "x"`
// has to keep parsing, so no dispatch can decide on the cursor alone. Markers
// are skipped the way next does, because a marker is not a word.
func (p *parser) peekWordAt(n int, word string) bool {
	seen, i := 0, p.i
	for ; i < len(p.toks); i++ {
		if p.toks[i].IsMarker() {
			continue
		}
		if seen == n {
			break
		}
		seen++
	}
	if i >= len(p.toks) {
		return false
	}
	t := p.toks[i]
	return t.Kind == token.Ident && t.Value == word
}

// peekKindAt reports whether the nth token after the cursor is of kind k,
// counting the cursor itself as zero and skipping markers as peekWordAt does.
func (p *parser) peekKindAt(n int, k token.Kind) bool {
	seen, i := 0, p.i
	for ; i < len(p.toks); i++ {
		if p.toks[i].IsMarker() {
			continue
		}
		if seen == n {
			break
		}
		seen++
	}
	if i >= len(p.toks) {
		return false
	}
	return p.toks[i].Kind == k
}

// atBoundary reports whether the cursor is at a statement boundary: a newline
// separated it from the previous token, or the enclosing block ends here, or
// the file does.
//
// This is the grammar's `Sep = "," | newline` seen from the parser's side.
// Newlines are trivia rather than tokens -- making them tokens would give a
// newline two possible homes and that ambiguity is what makes a lossless
// printer fragile -- so the question is asked of the token's leading trivia.
func (p *parser) atBoundary() bool {
	t := p.peek()
	return t.Kind == token.EOF || t.Kind == token.RBrace || t.StartsLine()
}

// eatComma consumes a separating comma if there is one. A trailing comma is
// legal everywhere a comma separates, so this never insists on one; it belongs
// to the item before it rather than floating between two, which is what keeps
// recovery from desynchronising a list.
func (p *parser) eatComma() token.Token {
	if p.at(token.Comma) {
		return p.next()
	}
	return token.Token{}
}

// expect consumes a token of kind k, or reports and returns a zero token
// without consuming anything.
//
// Not consuming on failure is deliberate: the caller is about to recover, and
// recovery needs the offending token to still be there so its skip has
// something to skip.
//
// what names what was wanted in the grammar's own vocabulary -- "a scenario
// name", not "a string token" -- because the author is reading the language,
// not the lexer.
func (p *parser) expect(k token.Kind, what string) (token.Token, bool) {
	if p.at(k) {
		return p.next(), true
	}
	p.errorf(p.peek(), diag.UnexpectedToken, "expected %s, found %s", what, describe(p.peek()))
	return token.Token{}, false
}

// expectIdent consumes an identifier, or reports. what is the role the name
// plays -- "a variable name", "a type name" -- because "expected identifier"
// says nothing an author can act on.
func (p *parser) expectIdent(what string) (token.Token, bool) {
	return p.expect(token.Ident, what)
}

// errorf reports a diagnostic at a token's span and returns the handle, so a
// hint or a suggestion can be attached in the same expression.
func (p *parser) errorf(at token.Token, code diag.Code, format string, args ...any) *diag.Ref {
	return p.bag.Error(at.Span, code, format, args...)
}

// describe names a token for a diagnostic: its source text in quotes, or its
// kind where it has no text worth printing.
//
// An author reads `found "}"`, not `found right-brace`, so the text wins
// wherever there is any. EOF has none, and neither does a marker.
func describe(t token.Token) string {
	switch t.Kind {
	case token.EOF:
		return "end of file"
	case token.Invalid:
		if t.Text == "" {
			return "an invalid token"
		}
		return "invalid input " + quote(t.Text)
	}
	if t.Text == "" {
		return t.Kind.String()
	}
	return quote(t.Text)
}

// quote wraps s in double quotes, escaping what has to be escaped so a
// diagnostic about a string literal does not itself look unterminated. It is
// strconv.Quote with one difference: a quote is the only thing escaped besides
// a backslash, because the text being described is source an author wrote and
// re-escaping their escapes would make the message harder to match to the
// file.
func quote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		case '\t':
			out = append(out, '\\', 't')
		case '\r':
			out = append(out, '\\', 'r')
		default:
			out = append(out, s[i])
		}
	}
	return string(append(out, '"'))
}

// recoverTo skips tokens until the next statement boundary and returns them as
// an ast.Bad, so the source they cover survives in the tree.
//
// This is what "recovery at statement boundaries" means concretely. The skip
// tracks bracket depth, so a `}` closing an object literal inside the bad line
// is not mistaken for the `}` closing the block -- without that, one bad line
// containing `body = {` would eat the rest of the scenario, which is the
// failure this whole mechanism exists to prevent.
//
// It always consumes at least one token when there is one. A recovery that
// consumed nothing would let the caller's loop spin forever, and a hang is a
// fuzz failure as surely as a panic is.
func (p *parser) recoverTo() *ast.Bad {
	bad := &ast.Bad{}

	// The token that caused the error is taken first, since the cursor is
	// sitting on it and it is a statement boundary as often as not -- but not
	// if it is a closer the enclosing block is waiting for. Eating that would
	// make one bad line take the rest of the block with it, which is the
	// whole fault this mechanism exists to prevent.
	if !p.atCloser() {
		bad.Toks = append(bad.Toks, p.consumeRaw())
	}

	for !p.at(token.EOF) {
		t := p.peek()
		if t.Kind == token.RBrace && p.depth == 0 {
			break
		}
		if t.StartsLine() && p.depth == 0 {
			break
		}
		bad.Toks = append(bad.Toks, p.consumeRaw())
	}
	return bad
}

// maxNesting is how deep the parser will descend.
//
// Recursive descent costs a stack frame per level and Go's stacks grow until
// the runtime gives up, at which point the process dies with "fatal error:
// stack overflow" -- not a panic, so no recover() catches it and no diagnostic
// is ever printed. A file of 100,000 open parentheses did exactly that before
// this limit existed. Since the front end's promise is that a malformed file
// produces diagnostics and never a stack trace, the limit is part of keeping
// it.
//
// 200 is far past anything a person or a code generator writes -- the design's
// worked example nests four deep -- and far short of what a stack cannot hold.
const maxNesting = 200

// enter descends one level and reports whether there was room.
//
// A caller that gets false must not recurse. It reports once per file, because
// a file that is too deep is too deep at every level below the limit and 200
// copies of the same diagnostic is not 200 pieces of information.
func (p *parser) enter(at token.Token) bool {
	p.nest++
	if p.nest <= maxNesting {
		return true
	}
	if !p.tooDeep {
		p.tooDeep = true
		p.errorf(at, diag.NestingTooDeep,
			"this file nests more than %d levels deep", maxNesting).
			Hintf("brackets, blocks and prefix operators each count as a level")
	}
	return false
}

// leave ascends one level.
func (p *parser) leave() { p.nest-- }

// atCloser reports whether the token at the cursor belongs to an enclosing
// construct rather than to the one being parsed: end of file, the "}" of the
// block around us, or a separating comma.
//
// Nothing in error handling may consume one of these. A "}" eaten during
// recovery is how a parser turns one incomplete line into a file that fails
// from there to the end.
func (p *parser) atCloser() bool {
	switch p.peek().Kind {
	case token.EOF, token.Comma:
		return true
	case token.RBrace:
		return p.depth == 0
	case token.RParen, token.RBracket:
		return p.depth > 0
	}
	return false
}

// consumeRaw takes the token at the cursor and keeps the parser's bracket
// depth up to date. Only recovery uses it: the ordinary parse functions know
// which bracket they are opening and adjust depth around a whole construct.
func (p *parser) consumeRaw() token.Token {
	t := p.next()
	switch t.Kind {
	case token.LBrace, token.LParen, token.LBracket:
		p.depth++
	case token.RBrace, token.RParen, token.RBracket:
		if p.depth > 0 {
			p.depth--
		}
	}
	return t
}

// endStatement ends an item that sits in a brace-delimited list -- a field, a
// browser action, an expect, a capture. It takes a separating comma if there
// is one and returns it for the node to store, and reports if what follows is
// neither a boundary nor a comma.
//
// Two statements on one line is the fault this catches. The report does not
// recover, because the next statement is perfectly parseable -- the author
// only forgot to separate them -- and skipping it would turn one small
// mistake into a missing step.
func (p *parser) endStatement(what string) token.Token {
	comma := p.eatComma()
	if !comma.Span.IsZero() {
		return comma
	}
	p.requireBoundary(what)
	return token.Token{}
}

// endDecl ends a construct that has nowhere to put a comma: a scenario-body
// declaration, or a step's action block.
//
// The grammar's `Sep = "," | newline` is a property of a *list* -- the
// settings in a block, the entries of an object, the elements of an array --
// and a declaration is not in one, so ast.VarDecl and ast.Request have no
// Comma field. A comma here is therefore reported and *left where it is* for
// the enclosing loop's recovery to collect.
//
// Consuming it instead is the bug this function exists to prevent, and the
// fuzz target is what found it: endStatement ate the comma in `get "/x" { }
// ,0`, the Request had no field to hold it, and the token vanished from the
// tree. A parser may drop a token from its *parse*; it may never drop one from
// the source it reproduces.
func (p *parser) endDecl(what string) {
	if p.at(token.Comma) {
		p.errorf(p.peek(), diag.UnexpectedToken, "unexpected \",\" after %s", what).
			Hintf("%s ends at the end of its line; a comma separates fields inside a block", what)
		return
	}
	p.requireBoundary(what)
}

// requireBoundary reports if the cursor is not at a statement boundary.
func (p *parser) requireBoundary(what string) {
	if p.atBoundary() {
		return
	}
	hint := "statements separate by newline; there are no semicolons"
	if p.useLines > 0 {
		hint = "separate with a comma or a newline"
	}
	p.errorf(p.peek(), diag.MissingSeparator,
		"expected a newline after %s, found %s", what, describe(p.peek())).
		Hintf("%s", hint)
}

// unclosed reports a bracket the file ended without closing, pointing at the
// opener rather than at end of file, because the opener is what the author has
// to go and look at.
func (p *parser) unclosed(open token.Token, what string) {
	// Nothing to add once the nesting limit has tripped. The bail-out there
	// skips to end of file, so every frame below it unwinds finding its own
	// bracket unclosed -- two hundred true statements, each at a different
	// offset so the bag cannot dedup them, all explained by the one
	// diagnostic that already said the file is too deep.
	if p.tooDeep {
		return
	}
	p.errorf(open, diag.UnclosedBlock, "unclosed %s", what).
		Hintf("this %q is never closed", open.Text)
}
