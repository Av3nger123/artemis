package encode

import (
	"fmt"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// literalKinds maps the encoding's literal names to the lexer's kinds. The
// names are token.Kind's own String() output, so the two sides of the encoding
// read one table and a kind cannot be spelled one way out and another way in.
var literalKinds = map[string]token.Kind{
	token.Number.String(): token.Number,
	token.String.String(): token.String,
	token.Regex.String():  token.Regex,
	token.Bool.String():   token.Bool,
	token.Null.String():   token.Null,
}

// symbolOps are the operators spelled as symbols. Everything else -- `and`,
// `or`, `not`, `contains`, `matches` -- arrives as an Ident, because the grammar
// has no lexical keywords and `capture contains = ...` is a file someone will
// write.
var symbolOps = map[string]token.Kind{
	"==": token.Eq,
	"!=": token.Ne,
	"<":  token.Lt,
	"<=": token.Le,
	">":  token.Gt,
	">=": token.Ge,
	"-":  token.Minus,
}

// opTok is an operator token from its spelling. The kind matters downstream --
// pkg/dsl/check asks whether a Binary's operator is a comparison, pkg/eval
// switches on it -- so it is derived from the one table above rather than left
// as Ident for everything.
func opTok(text string) token.Token {
	if kind, ok := symbolOps[text]; ok {
		return syn(kind, text)
	}
	return syn(token.Ident, text)
}

// exprOf reads an expression-valued field. required says whether its absence is
// an error; an optional one that is absent comes back nil, which is what a
// `within`-less expect and a key-less field hold.
func exprOf(f fields, key string, required bool) (ast.Expr, error) {
	o, present, err := f.child(key)
	if err != nil {
		return nil, err
	}
	if !present {
		if required {
			return nil, fmt.Errorf("%s: missing %q", f.path, key)
		}
		return nil, nil
	}
	return exprIn(o)
}

// expr builds one expression from a list element.
func expr(v value) (ast.Expr, error) {
	f, err := v.object()
	if err != nil {
		return nil, err
	}
	return exprIn(f)
}

// exprIn builds one expression from an object already read.
func exprIn(f fields) (ast.Expr, error) {
	kind, err := f.kind()
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindIdent:
		return ident(f)
	case kindLiteral:
		return literal(f)
	case kindInterp:
		return interp(f)
	case kindUnary:
		return unary(f)
	case kindBinary:
		return binary(f)
	case kindExists:
		return exists(f)
	case kindIsType:
		return isType(f)
	case kindMember:
		return member(f)
	case kindIndex:
		return index(f)
	case kindCall:
		return call(f)
	case kindObject:
		return object(f)
	case kindArray:
		return array(f)
	case kindParen:
		return paren(f)
	}
	return nil, badKind(f, kind, "an expression")
}

func ident(f fields) (ast.Expr, error) {
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	return &ast.Ident{Tok: syn(token.Ident, name)}, nil
}

// literal rebuilds a literal from its source text. The decoded value in the
// document is ignored: text is what the printer writes, and Decode's reparse
// hands the text back to the lexer, which is the one implementation of
// unescaping.
func literal(f fields) (ast.Expr, error) {
	name, err := f.str("literal")
	if err != nil {
		return nil, err
	}
	kind, ok := literalKinds[name]
	if !ok {
		return nil, fmt.Errorf("%s: %q is not a literal kind", f.at("literal"), name)
	}
	text, err := f.str("text")
	if err != nil {
		return nil, err
	}
	return &ast.Literal{Tok: syn(kind, text)}, nil
}

// interp rebuilds an interpolated string. Each segment's delimiter carries the
// literal text either side of its braces -- `"a${` then `}b${` -- so the
// segments plus the end are the string again, which is the shape the lexer
// chose for exactly this reason.
func interp(f fields) (ast.Expr, error) {
	items, err := f.list("segments")
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s: an interpolated string needs at least one segment; "+
			"a string with no holes in it is a literal", f.path)
	}
	i := &ast.Interp{}
	for n, it := range items {
		s, err := it.object()
		if err != nil {
			return nil, err
		}
		delim, err := s.str("delim")
		if err != nil {
			return nil, err
		}
		kind := token.StringMid
		if n == 0 {
			kind = token.StringStart
		}
		e, err := exprOf(s, "expr", true)
		if err != nil {
			return nil, err
		}
		i.Segments = append(i.Segments, ast.Segment{Delim: syn(kind, delim), Expr: e})
	}
	end, err := f.str("end")
	if err != nil {
		return nil, err
	}
	i.End = syn(token.StringEnd, end)
	return i, nil
}

func unary(f fields) (ast.Expr, error) {
	op, err := f.str("op")
	if err != nil {
		return nil, err
	}
	x, err := exprOf(f, "x", true)
	if err != nil {
		return nil, err
	}
	return &ast.Unary{Op: opTok(op), X: x}, nil
}

func binary(f fields) (ast.Expr, error) {
	op, err := f.str("op")
	if err != nil {
		return nil, err
	}
	x, err := exprOf(f, "x", true)
	if err != nil {
		return nil, err
	}
	y, err := exprOf(f, "y", true)
	if err != nil {
		return nil, err
	}
	return &ast.Binary{X: x, Op: opTok(op), Y: y}, nil
}

func exists(f fields) (ast.Expr, error) {
	x, err := exprOf(f, "x", true)
	if err != nil {
		return nil, err
	}
	return &ast.Exists{X: x, Op: syn(token.Ident, "exists")}, nil
}

func isType(f fields) (ast.Expr, error) {
	x, err := exprOf(f, "x", true)
	if err != nil {
		return nil, err
	}
	name, err := f.str("type")
	if err != nil {
		return nil, err
	}
	return &ast.IsType{X: x, Op: syn(token.Ident, "is"), Type: syn(token.Ident, name)}, nil
}

func member(f fields) (ast.Expr, error) {
	x, err := exprOf(f, "x", true)
	if err != nil {
		return nil, err
	}
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	return &ast.Member{X: x, Dot: syn(token.Dot, "."), Name: syn(token.Ident, name)}, nil
}

func index(f fields) (ast.Expr, error) {
	x, err := exprOf(f, "x", true)
	if err != nil {
		return nil, err
	}
	i, err := exprOf(f, "index", true)
	if err != nil {
		return nil, err
	}
	return &ast.Index{
		X:        x,
		LBracket: syn(token.LBracket, "["),
		Index:    i,
		RBracket: syn(token.RBracket, "]"),
	}, nil
}

// call rebuilds `env("API_URL")`. The callee is an identifier and nothing else:
// the grammar has no first-class functions, so there is no expression to build
// here.
func call(f fields) (ast.Expr, error) {
	callee, err := f.str("callee")
	if err != nil {
		return nil, err
	}
	items, err := f.list("args")
	if err != nil {
		return nil, err
	}
	c := &ast.Call{
		Callee: &ast.Ident{Tok: syn(token.Ident, callee)},
		LParen: syn(token.LParen, "("),
		RParen: syn(token.RParen, ")"),
	}
	for _, it := range items {
		e, err := expr(it)
		if err != nil {
			return nil, err
		}
		c.Args = append(c.Args, ast.Arg{Value: e})
	}
	return c, nil
}

func object(f fields) (ast.Expr, error) {
	items, err := f.list("entries")
	if err != nil {
		return nil, err
	}
	o := &ast.Object{LBrace: syn(token.LBrace, "{"), RBrace: syn(token.RBrace, "}")}
	for _, it := range items {
		en, err := it.object()
		if err != nil {
			return nil, err
		}
		key, err := exprOf(en, "key", true)
		if err != nil {
			return nil, err
		}
		val, err := exprOf(en, "value", true)
		if err != nil {
			return nil, err
		}
		o.Entries = append(o.Entries, ast.Entry{Key: key, Colon: syn(token.Colon, ":"), Value: val})
	}
	return o, nil
}

func array(f fields) (ast.Expr, error) {
	items, err := f.list("elems")
	if err != nil {
		return nil, err
	}
	a := &ast.Array{LBracket: syn(token.LBracket, "["), RBracket: syn(token.RBracket, "]")}
	for _, it := range items {
		e, err := expr(it)
		if err != nil {
			return nil, err
		}
		a.Elems = append(a.Elems, ast.Elem{Value: e})
	}
	return a, nil
}

// paren is kept as a node rather than folded away, in both directions: folding
// it would make `(a or b) and c` print as `a or b and c`, which is a different
// expression. A client that means parentheses has to say so, because canonical
// layout adds none.
func paren(f fields) (ast.Expr, error) {
	x, err := exprOf(f, "x", true)
	if err != nil {
		return nil, err
	}
	return &ast.Paren{LParen: syn(token.LParen, "("), X: x, RParen: syn(token.RParen, ")")}, nil
}
