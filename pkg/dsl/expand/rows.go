package expand

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
	"artemis/pkg/eval"
)

// rows makes one scenario per row of s's data: `scenario "login as
// ${user.role}" for user in json("users.json") { ... }` with two users is two
// scenarios, each opening with `var user = <its row>` and named for it.
//
// This is expansion, like a use, and for the same reason: after it the
// language is unchanged. The checker checks every row as the scenario it now
// is, lower and the reports see plain scenarios, and `artemis expand` prints
// them. The data is read here, when the file compiles, because it is in the
// scenario's own repository -- a malformed users.json is a compile error with
// its line, not a run that fails at row forty.
//
// Each row is a deep copy, so no two scenarios share a node: the checker and
// lower both key facts by node. A diagnostic the copies all share is reported
// once; pkg/dsl/front collapses the repeats.
func (e *expander) rows(s *ast.Scenario) []ast.Decl {
	f := s.For
	if f.Source == nil || f.Var.Kind != token.Ident {
		return nil // the parser reported the clause it could not read
	}
	value, source, ok := e.static(f.Source)
	if !ok {
		return nil
	}
	list, isList := value.([]any)
	if !isList {
		e.bag.Error(f.Source.Span(), diag.ForSourceNotArray,
			"a for clause needs an array of rows, and this is %s", describeValue(value)).
			Hintf("for %s in json(%q) -- a file holding [{...}, {...}]", f.Var.Value, "rows.json")
		return nil
	}
	if len(list) == 0 {
		e.bag.Error(f.Source.Span(), diag.ForNoRows,
			"a for clause over no rows makes no scenario, and a run of nothing passes")
		return nil
	}

	name, ok := e.rowName(s)
	if !ok {
		return nil
	}
	seen := map[string]int{}
	out := make([]ast.Decl, 0, len(list))
	for i, row := range list {
		title, ok := name(i, row)
		if !ok {
			return nil
		}
		if first, dup := seen[title]; dup {
			e.bag.Error(s.Name.Span, diag.DuplicateRowName,
				"rows %d and %d both name a scenario %q", first+1, i+1, title).
				Hintf("name each row by a field that differs between rows, like ${%s.id}", f.Var.Value)
			return nil
		}
		seen[title] = i

		cp := ast.Clone(s, identity)
		cp.For = nil
		cp.Row = &ast.RowOrigin{Source: source, Index: i}
		cp.Name = synth(token.String, quote(title), title, s.Name.Span)
		cp.Body = append([]ast.Decl{rowVar(f, row)}, cp.Body...)
		out = append(out, cp)
	}
	return out
}

// rowVar is `var <name> = <row>`, the declaration a row's scenario opens with.
// Every token sits on the for clause, so a diagnostic about the row points at
// the line that brought it in.
func rowVar(f *ast.ForClause, row any) *ast.VarDecl {
	at := f.Var.Span
	v := &ast.VarDecl{
		Keyword: synth(token.Ident, "var", "var", at),
		Name:    synth(token.Ident, f.Var.Value, f.Var.Value, at),
		Assign:  synth(token.Assign, "=", "", at),
		Value:   valueExpr(row, at),
	}
	if f.Secret.Text != "" {
		v.Secret = synth(token.Ident, "secret", "secret", at)
	}
	return v
}

// rowName is how each row's scenario is named: the scenario's name template
// evaluated against the row when it interpolates one, or the name with the
// row's number after it when it does not.
//
// The template may read the row and nothing else. A var is not bound yet when
// a name is made, and a call -- env(), uuid() -- would make a name the report
// could not repeat, so either is an error naming what was read.
func (e *expander) rowName(s *ast.Scenario) (func(int, any) (string, bool), bool) {
	plain := s.Name.Value
	if !strings.Contains(s.Name.Text, "${") {
		return func(i int, _ any) (string, bool) {
			return fmt.Sprintf("%s [%d]", plain, i+1), true
		}, true
	}
	x, bag := parser.ParseExpr(s.Name.Span.File, s.Name.Text)
	if bag.HasErrors() {
		return nil, false // the parser already reported the name
	}
	bad := ""
	ast.Inspect(x, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.Call:
			bad = "a call"
		case *ast.Ident:
			if n.Name() != s.For.Var.Value && bad == "" {
				bad = fmt.Sprintf("%q", n.Name())
			}
		}
	})
	if bad != "" {
		e.bag.Error(s.Name.Span, diag.BadRowName,
			"a data-driven scenario's name can read only its row, %s, and this one reads %s", s.For.Var.Value, bad).
			Hintf(`scenario "login as ${%s.role}" for %s in ...`, s.For.Var.Value, s.For.Var.Value)
		return nil, false
	}
	return func(i int, row any) (string, bool) {
		v, err := eval.Eval(x, &eval.Env{Vars: map[string]any{s.For.Var.Value: row}})
		if err != nil {
			e.bag.Error(s.Name.Span, diag.BadRowName, "row %d cannot name its scenario: %v", i+1, err)
			return "", false
		}
		return eval.Render(v), true
	}, true
}

// static is the value of a for clause's source, known before the run: an
// array or object literal, json() or csv() of a literal path, and a member or
// an index into one of those. source names where the rows came from, for the
// report.
func (e *expander) static(x ast.Expr) (value any, source string, ok bool) {
	switch x := x.(type) {
	case *ast.Paren:
		return e.static(x.X)
	case *ast.Literal:
		switch x.Kind() {
		case token.Number:
			f, err := strconv.ParseFloat(x.Tok.Value, 64)
			return f, "rows", err == nil
		case token.String:
			return x.Tok.Value, "rows", true
		case token.Bool:
			return x.Tok.Value == "true", "rows", true
		case token.Null:
			return nil, "rows", true
		}
	case *ast.Unary:
		if x.Op.Kind == token.Minus {
			if v, src, ok := e.static(x.X); ok {
				if f, isNum := v.(float64); isNum {
					return -f, src, true
				}
			}
		}
	case *ast.Array:
		out := make([]any, 0, len(x.Elems))
		for _, el := range x.Elems {
			v, _, ok := e.static(el.Value)
			if !ok {
				return nil, "", false
			}
			out = append(out, v)
		}
		return out, "rows", true
	case *ast.Object:
		out := make(map[string]any, len(x.Entries))
		for _, en := range x.Entries {
			key, isKey := en.Key.(*ast.Literal)
			if !isKey || key.Kind() != token.String {
				return e.notStatic(en.Key)
			}
			v, _, ok := e.static(en.Value)
			if !ok {
				return nil, "", false
			}
			out[key.Tok.Value] = v
		}
		return out, "rows", true
	case *ast.Call:
		return e.staticData(x)
	case *ast.Member:
		v, src, ok := e.static(x.X)
		if !ok {
			return nil, "", false
		}
		obj, isObj := v.(map[string]any)
		field, has := obj[x.Name.Value]
		if !isObj || !has {
			e.bag.Error(x.Name.Span, diag.ForSourceNotArray, "the rows' data has no field %q here", x.Name.Value)
			return nil, "", false
		}
		return field, src, true
	case *ast.Index:
		v, src, ok := e.static(x.X)
		if !ok {
			return nil, "", false
		}
		k, _, ok := e.static(x.Index)
		if !ok {
			return nil, "", false
		}
		switch at := k.(type) {
		case float64:
			if list, isList := v.([]any); isList && at >= 0 && int(at) < len(list) && at == float64(int(at)) {
				return list[int(at)], src, true
			}
		case string:
			if obj, isObj := v.(map[string]any); isObj {
				if field, has := obj[at]; has {
					return field, src, true
				}
			}
		}
		e.bag.Error(x.Index.Span(), diag.ForSourceNotArray, "the rows' data has nothing at %s", eval.Render(k))
		return nil, "", false
	}
	return e.notStatic(x)
}

func (e *expander) notStatic(x ast.Expr) (any, string, bool) {
	e.bag.Error(x.Span(), diag.ForSourceNotStatic,
		"a for clause's rows have to be known before the run: an array, or json() or csv() of a path written out").
		Hintf(`for user in json("data/users.json")`)
	return nil, "", false
}

// staticData is json() or csv() of a literal path, read through the loader so
// the MCP server's workspace bound applies, and parsed as the run would.
func (e *expander) staticData(c *ast.Call) (any, string, bool) {
	name := ""
	if c.Callee != nil {
		name = c.Callee.Name()
	}
	if (name != "json" && name != "csv") || len(c.Args) != 1 {
		return e.notStatic(c)
	}
	lit, ok := c.Args[0].Value.(*ast.Literal)
	if !ok || lit == nil || lit.Kind() != token.String {
		return e.notStatic(c.Args[0].Value)
	}
	path := lit.Tok.Value
	if e.l == nil {
		e.bag.Error(lit.Span(), diag.ImportNeedsFile,
			"the rows in %s are read from disk, and this file was read from none", path)
		return nil, "", false
	}
	_, src, err := e.l.Load(lit.Span().File, path)
	if err != nil {
		e.bag.Error(lit.Span(), diag.DataFileMissing, "%s(): there is no file %s", name, path)
		return nil, "", false
	}
	var v any
	if name == "json" {
		v, err = eval.ParseJSON([]byte(src))
	} else {
		v, err = eval.ParseCSV([]byte(src))
	}
	if err != nil {
		code := diag.DataNotJSON
		if name == "csv" {
			code = diag.DataNotCSV
		}
		e.bag.Error(lit.Span(), code, "%s(): %s is not %s: %v", name, path, strings.ToUpper(name), err)
		return nil, "", false
	}
	return v, path, true
}

// valueExpr is a row's value written as the literal that would produce it, so
// the scenario a row makes is one a person could have typed -- and is what
// `artemis expand` prints. Object keys are in order, so the print is stable.
func valueExpr(v any, at token.Span) ast.Expr {
	switch t := v.(type) {
	case nil:
		return &ast.Literal{Tok: synth(token.Null, "null", "null", at)}
	case bool:
		s := strconv.FormatBool(t)
		return &ast.Literal{Tok: synth(token.Bool, s, s, at)}
	case float64:
		s := strconv.FormatFloat(t, 'f', -1, 64)
		if t < 0 {
			return &ast.Unary{
				Op: synth(token.Minus, "-", "", at),
				X:  &ast.Literal{Tok: synth(token.Number, s[1:], s[1:], at)},
			}
		}
		return &ast.Literal{Tok: synth(token.Number, s, s, at)}
	case string:
		return &ast.Literal{Tok: synth(token.String, quote(t), t, at)}
	case []any:
		a := &ast.Array{LBracket: synth(token.LBracket, "[", "", at), RBracket: synth(token.RBracket, "]", "", at)}
		for i, el := range t {
			elem := ast.Elem{Value: valueExpr(el, at)}
			if i < len(t)-1 {
				elem.Comma = synth(token.Comma, ",", "", at)
			}
			a.Elems = append(a.Elems, elem)
		}
		return a
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		o := &ast.Object{LBrace: synth(token.LBrace, "{", "", at), RBrace: synth(token.RBrace, "}", "", at)}
		for i, k := range keys {
			en := ast.Entry{
				Key:   &ast.Literal{Tok: synth(token.String, quote(k), k, at)},
				Colon: synth(token.Colon, ":", "", at),
				Value: valueExpr(t[k], at),
			}
			if i < len(keys)-1 {
				en.Comma = synth(token.Comma, ",", "", at)
			}
			o.Entries = append(o.Entries, en)
		}
		return o
	}
	return &ast.Literal{Tok: synth(token.Null, "null", "null", at)}
}

// describeValue names a value's type for a message: "an object", "a string".
func describeValue(v any) string {
	switch v.(type) {
	case map[string]any:
		return "an object"
	case string:
		return "a string"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	}
	return "a value"
}
