package lower

import (
	"fmt"
	"math"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/taint"
	"artemis/pkg/dsl/token"
	"artemis/pkg/eval"
	"artemis/pkg/shared/models"
)

// DB is a db step's action: `db orders_db { sql = "...", args = [...] }`.
type DB struct {
	Conn ast.Expr
	SQL  ast.Expr
	Args ast.Expr
	Line int

	// secretNames are the bindings declared `secret` and in scope for this
	// step, read by Model for the same reason Request.secretNames is.
	secretNames map[string]bool
}

// dbAction lowers a db step's action block.
func dbAction(d *ast.DB) *DB {
	out := &DB{Conn: d.Conn, Line: d.Keyword.Span.Line}
	for _, f := range fields(d.Block) {
		if f.Name.Kind != token.Ident {
			continue
		}
		switch f.Name.Value {
		case "sql":
			out.SQL = f.Value
		case "args":
			out.Args = f.Value
		}
		// Any other name is an unknown field the checker reported.
	}
	return out
}

// Model evaluates the db block against env and returns what dbstep reads.
//
// The URL is rendered and trimmed like a command; the statement is text or a
// file(); and every arg keeps its type, because a placeholder is compared
// against a typed column. See models.DB.
func (d *DB) Model(env *eval.Env) (models.DB, models.Secrets, error) {
	out := models.DB{}
	var marks models.Secrets

	var err error
	if out.URL, err = trimmed(d.Conn, env); err != nil {
		return out, marks, fmt.Errorf("connection: %w", err)
	}
	if d.SQL != nil {
		v, err := eval.Eval(d.SQL, env)
		if err != nil {
			return out, marks, fmt.Errorf("sql: %w", err)
		}
		switch t := v.(type) {
		case eval.File:
			out.SQLFile = t.Abs
		case string:
			out.SQL = t
		default:
			return out, marks, fmt.Errorf("sql is %s, want the statement as text or file(\"query.sql\")", eval.Render(v))
		}
	}
	if out.Args, err = dbArgs(d.Args, env); err != nil {
		return out, marks, err
	}

	marks.URL = taint.Secret(d.Conn, d.secretNames)
	marks.SQL = taint.Secret(d.SQL, d.secretNames)
	marks.Args = taint.Secret(d.Args, d.secretNames)
	return out, marks, nil
}

// dbArgs evaluates `args = [...]` to the values bound to the statement's
// placeholders, each keeping its type. See models.DB for what each becomes.
func dbArgs(x ast.Expr, env *eval.Env) ([]any, error) {
	if x == nil {
		return nil, nil
	}
	v, err := eval.Eval(x, env)
	if err != nil {
		return nil, fmt.Errorf("args: %w", err)
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("args is %s, want an array like [order_id, \"paid\"]", eval.Render(v))
	}
	out := make([]any, 0, len(list))
	for i, e := range list {
		a, err := dbArg(e)
		if err != nil {
			return nil, fmt.Errorf("args[%d]: %w", i, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// maxExact is the largest whole number a float64 holds exactly. A number past
// it arrived rounded already, and binding it as an int64 would present the
// rounding as the value.
const maxExact = 1 << 53

// dbArg is one value as a driver takes it.
func dbArg(v any) (any, error) {
	switch t := v.(type) {
	case nil, bool, string:
		return t, nil
	case float64:
		if t == math.Trunc(t) && math.Abs(t) <= maxExact {
			return int64(t), nil
		}
		return t, nil
	case eval.File:
		return models.FileArg(t.Abs), nil
	}
	if eval.ContainsFile(v) {
		return nil, eval.ErrFileInJSON
	}
	// An object or an array: its JSON text, which a json or jsonb column
	// takes as it is.
	return eval.Render(v), nil
}
