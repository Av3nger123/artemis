package encode

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// The encoders and builders for flow files and scenario files: a flow, a
// scenario file's header, and a data scenario's sets.

const (
	kindFlowDecl = "flowDecl" // `flow checkout { ... }`; "flow" is a block's kind
	kindFlowRef  = "flowRef"
	kindSet      = "set"
)

func (e *enc) flowDecl(f *ast.Flow) *obj {
	return start(kindFlowDecl, f).
		set("name", f.Name.Text).
		set("body", e.list(toNodes(f.Body))).
		span().
		comments(toks(f.Keyword, f.Name), f.LBrace, f.RBrace).done()
}

func (e *enc) flowRef(r *ast.FlowRef) *obj {
	return start(kindFlowRef, r).
		set("path", r.Path.Value).
		span().
		comments(r.Tokens(nil), zero, zero).done()
}

// setLine is one `name = value` of a data scenario. skip is the ident skip,
// as the parser reads it, so it needs no field of its own.
func (e *enc) setLine(s *ast.Set) *obj {
	return start(kindSet, s).
		set("name", s.Name.Text).
		set("value", e.node(s.Value)).
		span().
		comments(s.Tokens(nil), zero, zero).done()
}

func flowOf(f fields) (*ast.Flow, error) {
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	fl := &ast.Flow{
		Keyword: syn(token.Ident, "flow"),
		Name:    syn(token.Ident, name),
		LBrace:  syn(token.LBrace, "{"),
		RBrace:  syn(token.RBrace, "}"),
	}
	body, err := f.list("body")
	if err != nil {
		return nil, err
	}
	if fl.Body, err = decls(body); err != nil {
		return nil, err
	}
	return fl, braced(f, &fl.Keyword, &fl.LBrace, &fl.RBrace)
}

func flowRefOf(f fields) (*ast.FlowRef, error) {
	path, err := f.str("path")
	if err != nil {
		return nil, err
	}
	r := &ast.FlowRef{Keyword: syn(token.Ident, "flow"), Path: syn(token.String, quoteString(path))}
	return r, plain(f, &r.Keyword)
}

func setOf(f fields) (*ast.Set, error) {
	name, err := f.str("name")
	if err != nil {
		return nil, err
	}
	s := &ast.Set{Name: syn(token.Ident, name), Assign: syn(token.Assign, "=")}
	if s.Value, err = exprOf(f, "value", true); err != nil {
		return nil, err
	}
	return s, plain(f, &s.Name)
}
