package migrate

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
	"artemis/pkg/eval"
)

// A data-driven scenario, `scenario "login as ${user.role}" for user in
// json("users.json") { ... }`, ran once for each row of its data. The old
// runner made those rows when the file compiled; migrate makes them the same
// way, so each scenario it writes has the name and the value the row ran
// with.

// rows is the rows of a data-driven scenario: each one's scenario name and
// its value, read from the files in m.in.
func (m *migration) rows(key string, s *ast.Scenario) ([]row, error) {
	f := s.For
	if f.Source == nil || f.Var.Kind != token.Ident {
		return nil, fmt.Errorf("%s: scenario %s has a for clause that does not parse", at(key, s.Keyword), s.Name.Text)
	}
	r := rower{l: expand.MapLoader(m.in)}
	value, ok := r.static(f.Source)
	if !ok {
		return nil, r.err
	}
	list, isList := value.([]any)
	if !isList {
		r.fail(f.Source.Span(), "a for clause needs an array of rows, and this is %s", describeValue(value))
		return nil, r.err
	}
	name, ok := r.rowName(s)
	if !ok {
		return nil, r.err
	}
	seen := map[string]int{}
	out := make([]row, 0, len(list))
	for i, v := range list {
		title, ok := name(i, v)
		if !ok {
			return nil, r.err
		}
		if first, dup := seen[title]; dup {
			r.fail(s.Name.Span, "rows %d and %d both name a scenario %q", first+1, i+1, title)
			return nil, r.err
		}
		seen[title] = i
		out = append(out, row{title: title, value: valueExpr(v)})
	}
	return out, nil
}

// rower reads a for clause's rows, and keeps the first fault it meets.
type rower struct {
	l   expand.Loader
	err error
}

// fail records the fault at at.
func (r *rower) fail(at token.Span, format string, args ...any) {
	r.err = fmt.Errorf("%s:%d:%d: %s", at.File, at.Line, at.Col, fmt.Sprintf(format, args...))
}

// rowName is how each row's scenario is named: the scenario's name template
// evaluated against the row when it interpolates one, or the name with the
// row's number after it when it does not. The template may read the row and
// nothing else.
func (r *rower) rowName(s *ast.Scenario) (func(int, any) (string, bool), bool) {
	plain := s.Name.Value
	if !strings.Contains(s.Name.Text, "${") {
		return func(i int, _ any) (string, bool) {
			return fmt.Sprintf("%s [%d]", plain, i+1), true
		}, true
	}
	x, bag := parser.ParseExpr(s.Name.Span.File, s.Name.Text)
	if bag.HasErrors() {
		r.fail(s.Name.Span, "the name %s does not parse", s.Name.Text)
		return nil, false
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
		r.fail(s.Name.Span, "a data-driven scenario's name can read only its row, %s, and this one reads %s", s.For.Var.Value, bad)
		return nil, false
	}
	return func(i int, row any) (string, bool) {
		v, err := eval.Eval(x, &eval.Env{Vars: map[string]any{s.For.Var.Value: row}})
		if err != nil {
			r.fail(s.Name.Span, "row %d cannot name its scenario: %v", i+1, err)
			return "", false
		}
		return eval.Render(v), true
	}, true
}

// static is the value of a for clause's source, known before the run: an
// array or object literal, json() or csv() of a literal path, and a member or
// an index into one of those.
func (r *rower) static(x ast.Expr) (any, bool) {
	switch x := x.(type) {
	case *ast.Paren:
		return r.static(x.X)
	case *ast.Literal:
		switch x.Kind() {
		case token.Number:
			f, err := strconv.ParseFloat(x.Tok.Value, 64)
			if err != nil {
				return r.notStatic(x)
			}
			return f, true
		case token.String:
			return x.Tok.Value, true
		case token.Bool:
			return x.Tok.Value == "true", true
		case token.Null:
			return nil, true
		}
	case *ast.Unary:
		if x.Op.Kind == token.Minus {
			if v, ok := r.static(x.X); ok {
				if f, isNum := v.(float64); isNum {
					return -f, true
				}
			}
		}
	case *ast.Array:
		out := make([]any, 0, len(x.Elems))
		for _, el := range x.Elems {
			v, ok := r.static(el.Value)
			if !ok {
				return nil, false
			}
			out = append(out, v)
		}
		return out, true
	case *ast.Object:
		out := make(map[string]any, len(x.Entries))
		for _, en := range x.Entries {
			key, isKey := en.Key.(*ast.Literal)
			if !isKey || key.Kind() != token.String {
				return r.notStatic(en.Key)
			}
			v, ok := r.static(en.Value)
			if !ok {
				return nil, false
			}
			out[key.Tok.Value] = v
		}
		return out, true
	case *ast.Call:
		return r.data(x)
	case *ast.Member:
		v, ok := r.static(x.X)
		if !ok {
			return nil, false
		}
		obj, isObj := v.(map[string]any)
		field, has := obj[x.Name.Value]
		if !isObj || !has {
			r.fail(x.Name.Span, "the rows' data has no field %q here", x.Name.Value)
			return nil, false
		}
		return field, true
	case *ast.Index:
		v, ok := r.static(x.X)
		if !ok {
			return nil, false
		}
		k, ok := r.static(x.Index)
		if !ok {
			return nil, false
		}
		switch at := k.(type) {
		case float64:
			if list, isList := v.([]any); isList && at >= 0 && int(at) < len(list) && at == float64(int(at)) {
				return list[int(at)], true
			}
		case string:
			if obj, isObj := v.(map[string]any); isObj {
				if field, has := obj[at]; has {
					return field, true
				}
			}
		}
		r.fail(x.Index.Span(), "the rows' data has nothing at %s", eval.Render(k))
		return nil, false
	}
	return r.notStatic(x)
}

func (r *rower) notStatic(x ast.Expr) (any, bool) {
	r.fail(x.Span(), "a for clause's rows have to be known before the run: an array, or json() or csv() of a path written out")
	return nil, false
}

// data is json() or csv() of a literal path, parsed as the old run did.
func (r *rower) data(c *ast.Call) (any, bool) {
	name := ""
	if c.Callee != nil {
		name = c.Callee.Name()
	}
	if (name != "json" && name != "csv") || len(c.Args) != 1 {
		return r.notStatic(c)
	}
	lit, ok := c.Args[0].Value.(*ast.Literal)
	if !ok || lit == nil || lit.Kind() != token.String {
		return r.notStatic(c.Args[0].Value)
	}
	path := lit.Tok.Value
	_, src, err := r.l.Load(lit.Span().File, path)
	if err != nil {
		r.fail(lit.Span(), "%s(): there is no file %s", name, path)
		return nil, false
	}
	var v any
	if name == "json" {
		v, err = eval.ParseJSON([]byte(src))
	} else {
		v, err = eval.ParseCSV([]byte(src))
	}
	if err != nil {
		r.fail(lit.Span(), "%s(): %s is not %s: %v", name, path, strings.ToUpper(name), err)
		return nil, false
	}
	return v, true
}

// valueExpr is a row's value written as the literal that would produce it.
// Object keys are in order, so the print is stable.
func valueExpr(v any) ast.Expr {
	switch t := v.(type) {
	case nil:
		return &ast.Literal{Tok: lit(token.Null, "null")}
	case bool:
		return &ast.Literal{Tok: lit(token.Bool, strconv.FormatBool(t))}
	case float64:
		s := strconv.FormatFloat(t, 'f', -1, 64)
		if t < 0 {
			return &ast.Unary{Op: word(token.Minus, "-"), X: &ast.Literal{Tok: lit(token.Number, s[1:])}}
		}
		return &ast.Literal{Tok: lit(token.Number, s)}
	case string:
		return &ast.Literal{Tok: str(t)}
	case []any:
		a := &ast.Array{LBracket: word(token.LBracket, "["), RBracket: word(token.RBracket, "]")}
		for i, el := range t {
			elem := ast.Elem{Value: valueExpr(el)}
			if i < len(t)-1 {
				elem.Comma = word(token.Comma, ",")
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
		o := &ast.Object{LBrace: word(token.LBrace, "{"), RBrace: word(token.RBrace, "}")}
		for i, k := range keys {
			en := ast.Entry{
				Key:   &ast.Literal{Tok: str(k)},
				Colon: word(token.Colon, ":"),
				Value: valueExpr(t[k]),
			}
			if i < len(keys)-1 {
				en.Comma = word(token.Comma, ",")
			}
			o.Entries = append(o.Entries, en)
		}
		return o
	}
	return &ast.Literal{Tok: lit(token.Null, "null")}
}

// lit is a literal's token whose value is its text: a number, a bool, null.
func lit(kind token.Kind, text string) token.Token {
	return token.Token{Kind: kind, Text: text, Value: text}
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
