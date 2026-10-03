package parser

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/token"
)

// The expression grammar, loosest binding first:
//
//	Or      = And { "or" And }
//	And     = Not { "and" Not }
//	Not     = "not" Not | Cmp
//	Cmp     = Unary [ BinOp Unary | "exists" | "is" TypeName ]
//	Unary   = "-" Unary | Postfix
//	Postfix = Primary { "." Ident | "[" Expr "]" }
//	Primary = Number | String | Regex | Bool | "null" | Ident
//	        | Object | Array | Call | "(" Expr ")"
//
// `or` and `and` share one precedence-climbing loop over wordPrec. `not` and
// Cmp are separate functions because one is prefix and the other is
// non-associative, neither of which a climbing loop expresses.
//
// The one precedence fact worth stating twice: `not` sits *above* Cmp, so
// parseNot's operand is a whole comparison and `not body.x exists` is `not
// (body.x exists)` -- the only reading anyone wants. It sits *below* `and` and
// `or`, so `not a and b` is `(not a) and b`. Both fall straight out of the
// level order; neither needs a special case.

// wordPrec is the binding power of the two associative word operators, and
// zero for everything else. Higher binds tighter.
func wordPrec(t token.Token) int {
	if t.Kind != token.Ident {
		return 0
	}
	switch t.Value {
	case "or":
		return 1
	case "and":
		return 2
	}
	return 0
}

// isPredicate reports whether t opens a postfix predicate: `exists` or `is`.
func isPredicate(t token.Token) bool {
	return t.Kind == token.Ident && (t.Value == "exists" || t.Value == "is")
}

// parseExpr is the entry point for every expression position, and one of the
// four places the nesting limit is enforced. The four are parseExpr, parseNot,
// parseUnary and parseBlock, which between them sit on every path the parser
// can recurse on: brackets and calls re-enter through parseExpr, the two
// prefix operators chain through parseNot and parseUnary without passing
// through it, and a block nests through a field's block.
func (p *parser) parseExpr() ast.Expr {
	if !p.enter(p.peek()) {
		return p.recoverTo()
	}
	defer p.leave()
	return p.parseBinary(1)
}

// continues reports whether an infix or postfix operator at the cursor may
// join what came before it.
//
// It may not if a newline separates them and no bracket is open: `expect a`
// followed by a line beginning `and b` is two statements and an error, not one
// expression. Inside a bracket the rule lifts, which is what lets a
// multi-line object literal hold a multi-line expression.
func (p *parser) continues() bool {
	return p.depth > 0 || !p.peek().StartsLine()
}

// parseBinary is the climbing loop for `or` and `and`, left-associative.
func (p *parser) parseBinary(minPrec int) ast.Expr {
	x := p.parseNot()
	for {
		t := p.peek()
		prec := wordPrec(t)
		if prec == 0 || prec < minPrec || !p.continues() {
			return x
		}
		op := p.next()
		y := p.parseBinary(prec + 1)
		x = &ast.Binary{X: x, Op: op, Y: y}
	}
}

// parseNot is `"not" Not | Cmp`, right-recursive so `not not x` is legal.
func (p *parser) parseNot() ast.Expr {
	if p.atWord("not") {
		op := p.next()
		if !p.enter(op) {
			return &ast.Unary{Op: op, X: p.recoverTo()}
		}
		defer p.leave()
		return &ast.Unary{Op: op, X: p.parseNot()}
	}
	return p.parseCmp()
}

// parseCmp is `Unary [ BinOp Unary | "exists" | "is" TypeName ]`: at most one
// comparison or predicate.
//
// A second one is reported rather than folded, because `a == b == c` has two
// plausible readings and neither is what the author meant. The tokens are
// still consumed into the tree so the file round-trips and so one mistake
// yields one diagnostic instead of a cascade from the rest of the line.
func (p *parser) parseCmp() ast.Expr {
	x := p.parseUnary()
	if !p.continues() {
		return x
	}

	switch {
	case token.IsComparison(p.peek()):
		op := p.next()
		x = &ast.Binary{X: x, Op: op, Y: p.parseUnary()}
	case p.atWord("exists"):
		x = &ast.Exists{X: x, Op: p.next()}
	case p.atWord("is"):
		op := p.next()
		x = &ast.IsType{X: x, Op: op, Type: p.typeName()}
	default:
		return x
	}

	for p.continues() && (token.IsComparison(p.peek()) || isPredicate(p.peek())) {
		op := p.peek()
		p.errorf(op, diag.NonAssociativeOperator,
			"%s cannot be chained with another comparison", describe(op)).
			Hintf("an expect holds one comparison; join two with \"and\", or write two expect lines")

		switch {
		case p.atWord("exists"):
			x = &ast.Exists{X: x, Op: p.next()}
		case p.atWord("is"):
			o := p.next()
			x = &ast.IsType{X: x, Op: o, Type: p.typeName()}
		default:
			o := p.next()
			x = &ast.Binary{X: x, Op: o, Y: p.parseUnary()}
		}
	}
	return x
}

// typeName consumes the right-hand side of `is`.
//
// Five of the six type names are identifiers and the sixth is not: `null` has
// a token kind of its own, because it is also a value. Both are accepted here
// so `a is null` parses, and so does any other identifier -- a typo gets a
// did-you-mean against six short words from the checker, where a syntax error
// would have nothing to suggest.
func (p *parser) typeName() token.Token {
	if p.at(token.Ident) || p.at(token.Null) {
		return p.next()
	}
	p.errorf(p.peek(), diag.UnexpectedToken,
		"expected a type name after \"is\", found %s", describe(p.peek())).
		Hintf("the types are: %s", list(token.TypeNames))
	return token.Token{}
}

// parseUnary is `"-" Unary | Postfix`.
func (p *parser) parseUnary() ast.Expr {
	if p.at(token.Minus) {
		op := p.next()
		if !p.enter(op) {
			return &ast.Unary{Op: op, X: p.recoverTo()}
		}
		defer p.leave()
		return &ast.Unary{Op: op, X: p.parseUnary()}
	}
	return p.parsePostfix()
}

// parsePostfix is `Primary { "." Ident | "[" Expr "]" }`.
func (p *parser) parsePostfix() ast.Expr {
	x := p.parsePrimary()
	for p.continues() {
		switch {
		case p.at(token.Dot):
			dot := p.next()
			// A member name is any identifier: a JSON object's keys are not
			// the language's vocabulary, so `body.not` and `body.contains`
			// have to work.
			name, ok := p.expectIdent("a field name after \".\"")
			x = &ast.Member{X: x, Dot: dot, Name: name}
			if !ok {
				return x
			}
		case p.at(token.LBracket):
			open := p.next()
			p.depth++
			idx := p.parseExpr()
			end, ok := p.expect(token.RBracket, "\"]\"")
			p.depth--
			if !ok && p.at(token.EOF) {
				p.unclosed(open, "index")
			}
			x = &ast.Index{X: x, LBracket: open, Index: idx, RBracket: end}
			if !ok {
				return x
			}
		default:
			return x
		}
	}
	return x
}

// parsePrimary is a literal, a name, a call, a literal object or array, an
// interpolated string, or a parenthesised expression.
func (p *parser) parsePrimary() ast.Expr {
	t := p.peek()
	switch t.Kind {
	case token.Number, token.String, token.Regex, token.Bool, token.Null:
		return &ast.Literal{Tok: p.next()}

	case token.StringStart:
		return p.parseInterp()

	case token.Ident:
		// An identifier followed immediately by "(" is a call. The grammar is
		// `Call = Ident "(" ... ")"`, so there are no first-class functions
		// and no `body.f()`; which names are callable is token.Builtins and
		// the checker's business.
		name := &ast.Ident{Tok: p.next()}
		if p.at(token.LParen) && p.continues() {
			return p.parseCall(name)
		}
		return name

	case token.LBrace:
		return p.parseObject()

	case token.LBracket:
		return p.parseArray()

	case token.LParen:
		open := p.next()
		p.depth++
		x := p.parseExpr()
		end, ok := p.expect(token.RParen, "\")\"")
		p.depth--
		if !ok && p.at(token.EOF) {
			p.unclosed(open, "parenthesis")
		}
		return &ast.Paren{LParen: open, X: x, RParen: end}
	}

	// Nothing starts an expression here. The token goes into a Bad so the
	// source survives -- unless it is a token the caller is waiting for, in
	// which case it is left where it is.
	//
	// That exception is what keeps a missing right-hand side cheap. `expect a
	// ==` with nothing after it reaches here on the `}` that closes the step,
	// and an earlier version consumed it: the step then took the scenario's
	// `}` as its own and the whole file came apart from one incomplete line.
	// A closer, a comma and end of file all belong to whoever opened them.
	p.errorf(t, diag.UnexpectedToken, "expected an expression, found %s", describe(t))
	if p.atCloser() {
		return &ast.Bad{}
	}
	return &ast.Bad{Toks: []token.Token{p.consumeRaw()}}
}

// parseInterp reads an interpolated string: StringStart, an expression, then
// StringMid and another expression for as long as the string continues, then
// StringEnd.
//
// The lexer has already matched the braces -- an unclosed ${ is a marker token
// whose diagnostic is in the bag before parsing starts -- so the loop's job is
// only to keep the segments and their delimiters paired.
func (p *parser) parseInterp() ast.Expr {
	interp := &ast.Interp{}
	delim := p.next() // StringStart

	// A string cannot span lines, so a newline inside ${...} cannot happen;
	// depth is raised anyway so that the statement-boundary rule can never
	// cut an interpolated expression in half.
	p.depth++
	defer func() { p.depth-- }()

	for {
		before := p.i
		x := p.parseExpr()
		interp.Segments = append(interp.Segments, ast.Segment{Delim: delim, Expr: x})

		// The delimiter has been spent. A further segment gets one only if a
		// StringMid supplies it below -- an earlier version left delim set,
		// and a recovery that went round the loop twice put the same
		// StringStart token into two segments, which the round trip caught as
		// a duplicated `"${`.
		delim = token.Token{}

		switch {
		case p.at(token.StringMid):
			delim = p.next()
		case p.at(token.StringEnd):
			interp.End = p.next()
			return interp
		default:
			p.errorf(p.peek(), diag.UnclosedInterpolationExpr,
				"expected \"}\" to close the interpolation, found %s", describe(p.peek())).
				Hintf("an interpolated string is \"...${ expr }...\"")
			// Progress or stop. parseExpr consumes at least one token for any
			// token that is not EOF, so standing still means the stream is
			// exhausted and the loop has to end.
			if p.i == before || p.at(token.EOF) {
				return interp
			}
		}
	}
}

// parseCall reads the argument list of a call. Trailing commas are legal.
func (p *parser) parseCall(callee *ast.Ident) ast.Expr {
	call := &ast.Call{Callee: callee, LParen: p.next()}
	p.depth++
	defer func() { p.depth-- }()

	for !p.at(token.RParen) && !p.at(token.EOF) {
		before := p.i
		arg := ast.Arg{Value: p.parseExpr()}
		arg.Comma = p.eatComma()
		call.Args = append(call.Args, arg)

		if arg.Comma.Span.IsZero() {
			break // no comma: the list is over, and `)` had better be next
		}
		if p.i == before {
			break
		}
	}

	end, ok := p.expect(token.RParen, "\")\" to close the argument list")
	if !ok && p.at(token.EOF) {
		p.unclosed(call.LParen, "argument list")
	}
	call.RParen = end
	return call
}

// parseObject reads an object literal. Entries are `"key": value` separated by
// commas, with a trailing comma allowed.
//
// A request body is this rather than a string with placeholders spliced in,
// which is what removes the failure mode where a captured object reached a
// body by text concatenation.
func (p *parser) parseObject() ast.Expr {
	obj := &ast.Object{LBrace: p.next()}
	p.depth++
	defer func() { p.depth-- }()

	for !p.at(token.RBrace) && !p.at(token.EOF) {
		before := p.i

		// The key is an Expr because the grammar says String and an
		// interpolated string is one: `{"${k}": v}` parses. Insisting on a
		// literal string is the checker's rule, where it can say so by name.
		entry := ast.Entry{Key: p.parseExpr()}
		if colon, ok := p.expect(token.Colon, "\":\" after the key"); ok {
			entry.Colon = colon
			entry.Value = p.parseExpr()
		}
		entry.Comma = p.eatComma()
		obj.Entries = append(obj.Entries, entry)

		if entry.Comma.Span.IsZero() {
			break
		}
		if p.i == before {
			break
		}
	}

	end, ok := p.expect(token.RBrace, "\"}\" to close the object")
	if !ok && p.at(token.EOF) {
		p.unclosed(obj.LBrace, "object literal")
	}
	obj.RBrace = end
	return obj
}

// parseArray reads an array literal, with a trailing comma allowed.
func (p *parser) parseArray() ast.Expr {
	arr := &ast.Array{LBracket: p.next()}
	p.depth++
	defer func() { p.depth-- }()

	for !p.at(token.RBracket) && !p.at(token.EOF) {
		before := p.i
		elem := ast.Elem{Value: p.parseExpr()}
		elem.Comma = p.eatComma()
		arr.Elems = append(arr.Elems, elem)

		if elem.Comma.Span.IsZero() {
			break
		}
		if p.i == before {
			break
		}
	}

	end, ok := p.expect(token.RBracket, "\"]\" to close the array")
	if !ok && p.at(token.EOF) {
		p.unclosed(arr.LBracket, "array literal")
	}
	arr.RBracket = end
	return arr
}
