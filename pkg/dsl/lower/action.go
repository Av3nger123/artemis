package lower

import (
	"fmt"
	"net/url"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
	"artemis/pkg/eval"
	"artemis/pkg/shared/models"
)

// Request is an api step's action: `post "${url}/token" { header ... body ... }`.
//
// Method is already the verb as the file wrote it, lower-cased by the grammar
// and upper-cased here when it reaches models.Request, because net/http matches
// methods exactly.
type Request struct {
	Method  string
	URL     ast.Expr
	Headers []*Param
	Query   []*Param
	Body    ast.Expr
	Line    int
}

// Param is a named value in a block: `header "X" = v`, `query "q" = v`, or an
// `env { PGPASSWORD = pw }` setting.
//
// The two ways a name arrives are both kept. A header's and a query's name is a
// quoted key and so an expression, because `header "${name}" = v` parses; an env
// setting's name is the field name itself, an identifier, which is a name and not
// an expression -- evaluating it would look for a variable called PGPASSWORD.
// Name holds the second, Key the first, and exactly one of them is set.
type Param struct {
	Name  string
	Key   ast.Expr
	Value ast.Expr
	Line  int
}

// name is the parameter's name, evaluated when it came from a quoted key.
//
// It is trimmed either way: a space around a header or environment variable name
// is certainly a mistake, and sending `"Authorization "` is the kind of mistake
// that costs an afternoon.
func (p *Param) name(env *eval.Env) (string, error) {
	if p.Key == nil {
		return strings.TrimSpace(p.Name), nil
	}
	return trimmed(p.Key, env)
}

// Run is a terminal step's action: `run "psql" { args = [...] }`.
type Run struct {
	Command ast.Expr
	Args    ast.Expr
	Cwd     ast.Expr
	Stdin   ast.Expr
	Env     []*Param
	Line    int
}

// Act is one statement inside a `browser` block: `goto "/orders"`, `fill
// "#email" = "alice@example.com"`.
//
// Value is nil for the one-argument actions, which is the arity the checker has
// already enforced. Nothing here interprets the name: what `click` does belongs
// to pkg/steps/browserstep, and carrying the acts in source order -- then
// evaluating their arguments in Model -- is this package's whole share of it.
type Act struct {
	Name   string
	Target ast.Expr
	Value  ast.Expr
	Line   int
}

// Model evaluates the act against env and returns what browserstep reads.
//
// Both arguments go through text, which is eval.Render over the evaluated
// expression -- the same path a URL, a header value and a command name take --
// so `click "text=${plan}"` arrives as `text=pro` and a selector is a string
// and nothing else. The argument is not trimmed: leading space is meaningful in
// a `fill` value, and a selector with a stray space is a selector the browser
// will say it could not find, which is a better failure than a selector artemis
// quietly changed.
//
// The error names the action and, for the two-argument actions, which argument
// it was reading, because a browser block is many statements and "the step
// could not resolve an expression" would not say which line to go to.
func (a *Act) Model(env *eval.Env) (models.Act, error) {
	out := models.Act{Name: a.Name, Line: a.Line}

	target, err := text(a.Target, env)
	if err != nil {
		return out, fmt.Errorf("%s: %w", a.Name, err)
	}
	out.Target = target

	if a.Value != nil {
		if out.Value, err = text(a.Value, env); err != nil {
			return out, fmt.Errorf("%s %q: value: %w", a.Name, target, err)
		}
	}
	return out, nil
}

// browserModel evaluates every act of a browser step, in source order.
//
// It stops at the first act that will not resolve, rather than collecting the
// rest: the acts are a sequence with side effects, so an act after a broken one
// was never going to run, and reporting two failures would describe a step that
// got further than it did.
func browserModel(acts []*Act, env *eval.Env) (models.Browser, error) {
	out := models.Browser{}
	if len(acts) == 0 {
		return out, nil
	}
	out.Acts = make([]models.Act, 0, len(acts))
	for _, a := range acts {
		m, err := a.Model(env)
		if err != nil {
			return out, err
		}
		out.Acts = append(out.Acts, m)
	}
	return out, nil
}

// Model evaluates the request against env and returns what httpstep reads.
//
// Query parameters are folded into the URL, because models.Request has URL,
// Method, Headers and Body and no query field. They go in source order, each
// percent-escaped, introduced with `?` or `&` depending on whether the URL
// already has a query: source order rather than sorted, so a repeated `query
// "tag"` keeps both values in the order the author wrote them, and so nothing
// here reorders a file.
func (r *Request) Model(env *eval.Env) (models.Request, error) {
	out := models.Request{Method: strings.ToUpper(r.Method)}

	raw, err := text(r.URL, env)
	if err != nil {
		return out, fmt.Errorf("url: %w", err)
	}
	query, err := queryString(r.Query, env)
	if err != nil {
		return out, err
	}
	out.URL = withQuery(raw, query)

	if len(r.Headers) > 0 {
		out.Headers = make(map[string]string, len(r.Headers))
		for _, h := range r.Headers {
			name, err := h.name(env)
			if err != nil {
				return out, fmt.Errorf("header name: %w", err)
			}
			value, err := text(h.Value, env)
			if err != nil {
				return out, fmt.Errorf("header %q: %w", name, err)
			}
			out.Headers[name] = value
		}
	}

	// The body is the evaluated expression rendered, so an object literal
	// arrives as compact JSON rather than as text with values spliced into it --
	// which is the failure mode where a captured object reached a body by
	// concatenation. A step with no body sends an empty one.
	if r.Body != nil {
		if out.Body, err = text(r.Body, env); err != nil {
			return out, fmt.Errorf("body: %w", err)
		}
	}
	return out, nil
}

// queryString builds the `a=1&b=2` of a request's query parameters.
func queryString(params []*Param, env *eval.Env) (string, error) {
	if len(params) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, p := range params {
		name, err := p.name(env)
		if err != nil {
			return "", fmt.Errorf("query name: %w", err)
		}
		value, err := text(p.Value, env)
		if err != nil {
			return "", fmt.Errorf("query %q: %w", name, err)
		}
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(name))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(value))
	}
	return b.String(), nil
}

// withQuery appends query to raw.
//
// A URL that already has a `?` gets an `&`, because `get "${url}/orders?page=2"
// { query "limit" = 10 }` is a file someone will write and dropping either half
// would be the worst of the three answers. A URL ending in `?` is left as it is
// and the parameters follow it directly.
func withQuery(raw, query string) string {
	switch {
	case query == "":
		return raw
	case strings.HasSuffix(raw, "?") || strings.HasSuffix(raw, "&"):
		return raw + query
	case strings.Contains(raw, "?"):
		return raw + "&" + query
	default:
		return raw + "?" + query
	}
}

// Model evaluates the run block against env and returns what execstep reads.
//
// Env values are evaluated in source order, which is the order a failure among
// them is reported in. models.Exec's own EnvKeys() sorts the map afterwards, so
// the environment handed to the child process is built the same way on every
// run whatever order it was written in.
func (r *Run) Model(env *eval.Env) (models.Exec, error) {
	out := models.Exec{}

	var err error
	if out.Command, err = trimmed(r.Command, env); err != nil {
		return out, fmt.Errorf("command: %w", err)
	}
	if out.Cwd, err = text(r.Cwd, env); err != nil {
		return out, fmt.Errorf("cwd: %w", err)
	}
	if out.Stdin, err = text(r.Stdin, env); err != nil {
		return out, fmt.Errorf("stdin: %w", err)
	}
	if out.Args, err = args(r.Args, env); err != nil {
		return out, err
	}
	if len(r.Env) > 0 {
		out.Env = make(map[string]string, len(r.Env))
		for _, e := range r.Env {
			name, err := e.name(env)
			if err != nil {
				return out, fmt.Errorf("env name: %w", err)
			}
			value, err := text(e.Value, env)
			if err != nil {
				return out, fmt.Errorf("env %s: %w", name, err)
			}
			out.Env[name] = value
		}
	}
	return out, nil
}

// args evaluates `args = ["-f", "seed.sql"]` to the argv execstep passes to the
// command.
//
// The expression has to evaluate to an array, which the checker already insisted
// on for a literal; a var holding one arrives here. Each element is rendered,
// so `args = ["-n", count]` with a captured number gives "-n", "3" rather than
// "3.000000" -- and there is no word splitting anywhere, which is the whole
// reason args is a list.
func args(x ast.Expr, env *eval.Env) ([]string, error) {
	if x == nil {
		return nil, nil
	}
	v, err := eval.Eval(x, env)
	if err != nil {
		return nil, fmt.Errorf("args: %w", err)
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("args is %s, want an array like [\"-f\", \"seed.sql\"]", eval.Render(v))
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, eval.Render(e))
	}
	return out, nil
}

// request lowers an api step's action block.
func request(r *ast.Request) *Request {
	out := &Request{
		Method: r.Method.Value,
		URL:    r.URL,
		Line:   r.Method.Span.Line,
	}
	for _, f := range fields(r.Block) {
		if f.Name.Kind != token.Ident {
			continue
		}
		p := &Param{Key: f.Key, Value: f.Value, Line: f.Name.Span.Line}
		switch f.Name.Value {
		case "header":
			out.Headers = append(out.Headers, p)
		case "query":
			out.Query = append(out.Query, p)
		case "body":
			out.Body = f.Value
		}
		// Any other name is an unknown field the checker reported.
	}
	return out
}

// runAction lowers a terminal step's action block.
func runAction(r *ast.Run) *Run {
	out := &Run{Command: r.Command, Line: r.Keyword.Span.Line}
	for _, f := range fields(r.Block) {
		if f.Name.Kind != token.Ident {
			continue
		}
		switch f.Name.Value {
		case "args":
			out.Args = f.Value
		case "cwd":
			out.Cwd = f.Value
		case "stdin":
			out.Stdin = f.Value
		case "env":
			for _, e := range fields(f.Block) {
				if e.Name.Kind != token.Ident {
					continue
				}
				// The name is the field name itself, carried as a name
				// rather than as an expression: see Param.
				out.Env = append(out.Env, &Param{
					Name:  e.Name.Value,
					Value: e.Value,
					Line:  e.Name.Span.Line,
				})
			}
		}
	}
	return out
}

// acts lowers a browser step's actions, in source order.
func acts(b *ast.Browser) []*Act {
	out := make([]*Act, 0, len(b.Acts))
	for _, s := range b.Acts {
		a, ok := s.(*ast.BrowserAct)
		if !ok || a == nil || a.Name.Kind != token.Ident {
			continue // the parser reported an action it could not read.
		}
		out = append(out, &Act{
			Name:   a.Name.Value,
			Target: a.Target,
			Value:  a.Value,
			Line:   a.Name.Span.Line,
		})
	}
	return out
}
