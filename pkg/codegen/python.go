package codegen

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/dsl/token"
)

// Python is the pytest target: `artemis build --lang=python`.
//
// It emits one module per .art file, holding one test function per scenario,
// with each step's action and assertions as ordered statements inside it. The
// libraries are the ones the design document names -- requests for an api step,
// subprocess for a terminal step, playwright.sync_api for a browser step -- and
// nothing else: there is no generated artemis runtime beyond the handful of
// helpers in helpers.go, because a file nobody can read is a file nobody will
// adopt.
type Python struct{}

func (Python) Name() string { return "python" }

// Generate walks the lowered scenarios and returns the one module they make.
//
// lower.File is the whole reason this is additive. It gives the scenarios, their
// vars, their steps in source order, each step's inferred type and each step's
// action, expects and captures -- and it leaves every value position as an
// ast.Expr, because it evaluates nothing until Step.Model, which is never called
// here. So the interpreter and this target share the front end and the lowering
// and differ only in what they do with an expression.
//
// A nil Info is passed deliberately: the only thing lower reads off it is each
// expect's simple/complex class, which is a fact about what a form can render
// and nothing a target needs.
func (Python) Generate(tree *ast.File) ([]GeneratedFile, error) {
	scenarios, err := lower.File(tree, nil)
	if err != nil {
		return nil, err
	}

	source := SourceName(tree)
	if source == "" {
		// A tree built in Go, or decoded from JSON, has no spans to name it.
		source = "artemis" + artExt
	}

	f := &pyFile{used: map[string]bool{}, mods: map[string]bool{}, taken: map[string]bool{}}
	for _, sc := range scenarios {
		if err := f.scenario(sc); err != nil {
			return nil, err
		}
	}
	return []GeneratedFile{{
		Path:    fileName(source),
		Content: f.module(source),
	}}, nil
}

// artExt is the DSL's extension, for the fallback name above. pkg/cli has its
// own copy for dispatching on a path; this one is three characters and importing
// the CLI from here to share it would be the wrong direction.
const artExt = ".art"

// pyFile accumulates one module: the test functions, which helpers they reached
// for, and the names already taken.
//
// step is the registry key of the step being emitted, "" between steps, and it
// is what makes an identifier resolve the way eval.Env.lookup resolves it.
type pyFile struct {
	out   strings.Builder
	used  map[string]bool
	mods  map[string]bool
	step  string
	taken map[string]bool

	// wrote is whether the test function being emitted has a body line yet,
	// which is what decides whether a step gets a blank line above it. A `def`
	// followed by a blank line reads as a mistake.
	wrote bool
}

// use records that a helper is needed and returns its Python name, so a call
// site and the definition it needs cannot get out of step.
//
// Dependencies are pulled transitively: art_contains calls art_render, so using
// the first uses the second.
func (f *pyFile) use(name string) string {
	if f.used[name] {
		return name
	}
	f.used[name] = true
	for _, m := range byName[name].imports {
		f.mods[m] = true
	}
	for _, need := range byName[name].needs {
		f.use(need)
	}
	return name
}

// need records a module the emitted code imports for a reason that is not a
// helper: requests for an api step, subprocess for a terminal one, os for an
// env() call. The import list is the union of these and the helpers' own.
func (f *pyFile) need(module string) { f.mods[module] = true }

// module is the whole file: header, imports, helpers, test functions.
//
// It is assembled after the walk rather than streamed, because which imports and
// which helpers the file needs is only known once every expression has been
// emitted.
func (f *pyFile) module(source string) string {
	var b strings.Builder
	b.WriteString(header(source))

	var groups []string
	for _, group := range importGroups {
		var lines []string
		for _, m := range group {
			if f.mods[m] {
				lines = append(lines, importLines[m])
			}
		}
		if len(lines) > 0 {
			groups = append(groups, strings.Join(lines, "\n"))
		}
	}
	if len(groups) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(groups, "\n\n"))
		b.WriteString("\n")
	}

	for _, h := range helpers {
		if !f.used[h.name] {
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(h.src)
	}

	body := f.out.String()
	if body != "" {
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	return b.String()
}

// header is the first thing in every generated file.
//
// It says the export is one-way, because that is the decision a reader most
// needs to know -- artemis never reads this file back, so editing it is safe and
// regenerating it is destructive -- and it lists the three places the generated
// test and `artemis run` differ, so neither has to be discovered.
//
// No version and no timestamp. Regenerating a scenario that has not changed
// produces the same bytes, which is what makes `artemis build` safe to run in a
// loop and a diff mean the scenario moved.
func header(source string) string {
	return "# Generated by `artemis build --lang=python` from " + comment(source) + ".\n" +
		"#\n" +
		"# This is a ONE-WAY EXPORT. Artemis does not read it back: the .art file is the\n" +
		"# source of truth, and running the command again overwrites this file. Once you\n" +
		"# decide to own this instead, edit it freely and stop generating it.\n" +
		"#\n" +
		"# Where this differs from `artemis run` on the same scenario:\n" +
		"#   - a comparison uses Python's rules, so `status == \"200\"` does not hold\n" +
		"#     against a JSON 200 the way it does under the interpreter;\n" +
		"#   - a step that wrote no `timeout` has none here; artemis applies a default of\n" +
		"#     its own per step type;\n" +
		"#   - stdout and stderr are not truncated; artemis caps them for its report;\n" +
		"#   - a variable with no value raises at the point of use. `artemis run`\n" +
		"#     reports all of them before the first step; an exported module has no\n" +
		"#     such moment.\n"
}

// scenario emits one test function.
func (f *pyFile) scenario(sc *lower.Scenario) error {
	name := unique(testName(sc.Name), f.taken)

	needsPage := false
	for _, st := range sc.Steps {
		if st.Type == browserKey {
			needsPage = true
		}
	}

	params := ""
	if needsPage {
		params = helperBrowser
		f.use(helperBrowser)
	}

	if f.out.Len() > 0 {
		f.out.WriteString("\n\n")
	}
	f.line(0, "# scenario "+pyQuoteComment(sc.Name)+", line "+strconv.Itoa(sc.Line))
	f.line(0, "def "+name+"("+params+"):")

	// The vars first, in source order, because a var sees the vars above it and
	// nothing else -- which is the checker's rule and is only meaningful if they
	// are bound in reading order. f.step is "" here: a var's value has no roots.
	f.step = ""
	for _, v := range sc.Vars {
		src, err := f.expr(v.Value, precOr)
		if err != nil {
			return fmt.Errorf("scenario %q: var %s: %w", sc.Name, v.Name, err)
		}
		f.line(1, local(v.Name)+" = "+src)
	}

	if needsPage {
		open, err := f.openPage(sc)
		if err != nil {
			return fmt.Errorf("scenario %q: %w", sc.Name, err)
		}
		f.line(1, "page = "+open)
	}
	f.wrote = len(sc.Vars) > 0 || needsPage

	if len(sc.Vars) == 0 && !needsPage && len(sc.Steps) == 0 {
		// A scenario with no body at all still has to be a function.
		f.line(1, "pass")
		return nil
	}

	for _, st := range sc.Steps {
		if err := f.emitStep(st); err != nil {
			return fmt.Errorf("scenario %q: %w", sc.Name, err)
		}
	}
	return nil
}

// openPage is the fixture call that gives the scenario its page.
//
// `config browser`'s two settings are expressions, so they are emitted as
// expressions: `headless = env("HEADLESS") == "1"` needs nothing special here.
// A setting the scenario did not write is left off the call, so the fixture's own
// default applies and "the scenario said nothing" stays different from "the
// scenario said false".
func (f *pyFile) openPage(sc *lower.Scenario) (string, error) {
	if sc.Browser == nil {
		return helperBrowser + "()", nil
	}
	var args []string
	if sc.Browser.Headless != nil {
		src, err := f.expr(sc.Browser.Headless, precOr)
		if err != nil {
			return "", fmt.Errorf("config browser headless: %w", err)
		}
		args = append(args, "headless="+src)
	}
	if sc.Browser.Viewport != nil {
		src, err := f.value(sc.Browser.Viewport)
		if err != nil {
			return "", fmt.Errorf("config browser viewport: %w", err)
		}
		args = append(args, "viewport="+src)
	}
	return helperBrowser + "(" + strings.Join(args, ", ") + ")", nil
}

// emitStep emits one step: a comment naming it, its action, its assertions and
// its captures, wrapped in the retry loop when it wrote one.
func (f *pyFile) emitStep(st *lower.Step) error {
	f.step = st.Type
	defer func() { f.step = "" }()

	if f.wrote {
		f.out.WriteString("\n")
	}
	f.wrote = true
	f.line(1, "# step "+pyQuoteComment(st.Name)+", line "+strconv.Itoa(st.Line))

	indent := 1
	if retried(st) {
		loop, err := f.retryLoop(st)
		if err != nil {
			return fmt.Errorf("step %q: %w", st.Name, err)
		}
		f.line(1, loop)
		f.line(2, "with attempt:")
		indent = 3
	}

	if err := f.action(st, indent); err != nil {
		return fmt.Errorf("step %q: %w", st.Name, err)
	}
	if err := f.asserts(st, indent); err != nil {
		return fmt.Errorf("step %q: %w", st.Name, err)
	}
	return f.captures(st, indent)
}

// retried reports whether the step wrote a `retry` block worth looping on.
func retried(st *lower.Step) bool { return st.Retry.Times != nil || st.Retry.Delay != nil }

// retryLoop is the `for attempt in art_retry(...)` line.
//
// times and delay are expressions, so a count out of a var works; a literal
// number and a literal duration are converted here, which is what keeps
// `retry { times = 3, delay = "1s" }` reading as `times=3, delay=1.0`.
func (f *pyFile) retryLoop(st *lower.Step) (string, error) {
	var args []string
	if st.Retry.Times != nil {
		src, err := f.count(st.Retry.Times)
		if err != nil {
			return "", fmt.Errorf("retry times: %w", err)
		}
		args = append(args, "times="+src)
	}
	if st.Retry.Delay != nil {
		src, err := f.seconds(st.Retry.Delay)
		if err != nil {
			return "", fmt.Errorf("retry delay: %w", err)
		}
		args = append(args, "delay="+src)
	}
	return "for attempt in " + f.use(helperRetry) + "(" + strings.Join(args, ", ") + "):", nil
}

// action emits the step's action and binds the roots it observes.
func (f *pyFile) action(st *lower.Step, indent int) error {
	switch {
	case st.Request != nil:
		return f.request(st, indent)
	case st.Run != nil:
		return f.run(st, indent)
	case st.Acts != nil:
		return f.acts(st, indent)
	}
	// lower.TypeKey already refused a step with no action, so this is a step
	// built in Go with nothing to do.
	return fmt.Errorf("has no action to export")
}

// request emits an api step: one requests call, then the four roots.
func (f *pyFile) request(st *lower.Step, indent int) error {
	f.need("requests")
	r := st.Request

	url, err := f.value(r.URL)
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}

	args := []string{url}
	headers, err := f.params(r.Headers, true)
	if err != nil {
		return err
	}
	if headers != "" {
		args = append(args, "headers={"+headers+"}")
	}
	query, err := f.query(r.Query)
	if err != nil {
		return err
	}
	if query != "" {
		args = append(args, "params=["+query+"]")
	}
	if r.Body != nil {
		// data= and not json=: the interpreter sends the body as the rendered
		// expression and sets no header the scenario did not write, and json=
		// would add a Content-Type of its own.
		body, err := f.value(r.Body)
		if err != nil {
			return fmt.Errorf("body: %w", err)
		}
		args = append(args, "data="+body)
	}
	if st.Timeout != nil {
		secs, err := f.seconds(st.Timeout)
		if err != nil {
			return fmt.Errorf("timeout: %w", err)
		}
		args = append(args, "timeout="+secs)
	}

	f.callLine(indent, "resp = requests."+r.Method, args)
	f.line(indent, "status, body, raw, headers = (")
	f.line(indent+1, "resp.status_code, "+f.use(helperJSON)+"(resp), resp.text, resp.headers)")
	return nil
}

// params emits the `"name": value` pairs of a header block.
//
// A name is trimmed, because a space around a header name is certainly a mistake
// and sending "Authorization " is the kind of mistake that costs an afternoon.
func (f *pyFile) params(ps []*lower.Param, trim bool) (string, error) {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		name, err := f.paramName(p, trim)
		if err != nil {
			return "", err
		}
		value, err := f.value(p.Value)
		if err != nil {
			return "", fmt.Errorf("header %s: %w", name, err)
		}
		parts = append(parts, name+": "+value)
	}
	return strings.Join(parts, ", "), nil
}

// query emits the ordered pair list a request's query parameters become.
//
// A list of pairs rather than a dict, because source order is kept and a
// repeated `query "tag"` keeps both values -- which a dict would silently
// collapse. requests does the percent-escaping that lower.queryString does by
// hand.
func (f *pyFile) query(ps []*lower.Param) (string, error) {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		name, err := f.paramName(p, true)
		if err != nil {
			return "", err
		}
		value, err := f.value(p.Value)
		if err != nil {
			return "", fmt.Errorf("query %s: %w", name, err)
		}
		parts = append(parts, "("+name+", "+value+")")
	}
	return strings.Join(parts, ", "), nil
}

// paramName is a parameter's name: the quoted key evaluated, or the field name
// itself for an `env { ... }` setting. See lower.Param.
func (f *pyFile) paramName(p *lower.Param, trim bool) (string, error) {
	if p.Key == nil {
		name := p.Name
		if trim {
			name = strings.TrimSpace(name)
		}
		return quote(name), nil
	}
	if trim {
		return f.trimmedValue(p.Key)
	}
	return f.value(p.Key)
}

// run emits a terminal step: one subprocess call, then the three roots.
//
// check=False, because a command that ran and exited non-zero is an observation
// rather than an error -- `expect exit_code == 0` is what decides whether the
// scenario minds. text=True, because the roots are strings in the evaluator's
// domain.
func (f *pyFile) run(st *lower.Step, indent int) error {
	f.need("subprocess")
	r := st.Run

	argv, err := f.argv(r)
	if err != nil {
		return err
	}
	args := []string{argv}
	if r.Cwd != nil {
		cwd, err := f.value(r.Cwd)
		if err != nil {
			return fmt.Errorf("cwd: %w", err)
		}
		args = append(args, "cwd="+cwd)
	}
	if r.Stdin != nil {
		stdin, err := f.value(r.Stdin)
		if err != nil {
			return fmt.Errorf("stdin: %w", err)
		}
		args = append(args, "input="+stdin)
	}
	if len(r.Env) > 0 {
		f.need("os")
		// Layered over the process environment, which is what the interpreter
		// does: a step's `env` adds to and overrides, it does not replace.
		env, err := f.params(r.Env, false)
		if err != nil {
			return err
		}
		args = append(args, "env={**os.environ, "+env+"}")
	}
	args = append(args, "capture_output=True", "text=True", "check=False")
	if st.Timeout != nil {
		secs, err := f.seconds(st.Timeout)
		if err != nil {
			return fmt.Errorf("timeout: %w", err)
		}
		args = append(args, "timeout="+secs)
	}

	f.callLine(indent, "proc = subprocess.run", args)
	f.line(indent, "exit_code, stdout, stderr = proc.returncode, proc.stdout, proc.stderr")
	return nil
}

// argv is the list subprocess runs.
//
// A literal `args = [...]` becomes a plain Python list with the command in front
// of it, which is what a Python author would have written. An `args` that is an
// expression -- a var holding a list -- goes through art_argv, which is where
// "it has to be an array" and "every element is rendered" are enforced at run
// time.
func (f *pyFile) argv(r *lower.Run) (string, error) {
	command, err := f.trimmedValue(r.Command)
	if err != nil {
		return "", fmt.Errorf("command: %w", err)
	}
	switch list := r.Args.(type) {
	case nil:
		return "[" + command + "]", nil
	case *ast.Array:
		parts := []string{command}
		for _, e := range list.Elems {
			v, err := f.value(e.Value)
			if err != nil {
				return "", fmt.Errorf("args: %w", err)
			}
			parts = append(parts, v)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	default:
		src, err := f.expr(r.Args, precOr)
		if err != nil {
			return "", fmt.Errorf("args: %w", err)
		}
		return f.use(helperArgv) + "(" + command + ", " + src + ")", nil
	}
}

// acts emits a browser step: the step's own timeout, then one Playwright call
// per act in source order.
//
// Source order is the only control flow a browser step has: `fill` then `click`
// is a form submitted and the reverse is not.
func (f *pyFile) acts(st *lower.Step, indent int) error {
	if st.Timeout != nil {
		// The step's `timeout` bounds Playwright's own auto-waiting, which is
		// what it does under the interpreter. Set once for the block rather than
		// per act: `timeout = "5s"` means five seconds for the step.
		ms, err := f.millis(st.Timeout)
		if err != nil {
			return fmt.Errorf("timeout: %w", err)
		}
		f.line(indent, "page.set_default_timeout("+ms+")")
	}
	for _, a := range st.Acts {
		line, err := f.act(a)
		if err != nil {
			return err
		}
		f.line(indent, line)
	}
	return nil
}

// act is one browser action as one Playwright call.
//
// Eight actions, eight calls, and no default that a .art file can reach: the
// checker rejects any other name with a did-you-mean. `press` takes a key and no
// selector, so it goes to the keyboard rather than to an element -- sending it to
// a selector would type into something other than the field a `fill` just filled.
func (f *pyFile) act(a *lower.Act) (string, error) {
	target, err := f.value(a.Target)
	if err != nil {
		return "", fmt.Errorf("line %d: %s: %w", a.Line, a.Name, err)
	}
	two := func() (string, error) {
		v, err := f.value(a.Value)
		if err != nil {
			return "", fmt.Errorf("line %d: %s: value: %w", a.Line, a.Name, err)
		}
		return v, nil
	}

	switch a.Name {
	case "goto":
		return f.use(helperGoto) + "(page, " + target + ")", nil
	case "click":
		return "page.click(" + target + ")", nil
	case "hover":
		return "page.hover(" + target + ")", nil
	case "press":
		return "page.keyboard.press(" + target + ")", nil
	case "fill":
		v, err := two()
		return "page.fill(" + target + ", " + v + ")", err
	case "select":
		// By the option's value, which is what SPEC.md says: `select "#plan" =
		// "pro"` is the option whose value attribute is "pro", not the one whose
		// text reads "pro".
		v, err := two()
		return "page.select_option(" + target + ", value=" + v + ")", err
	case "upload":
		v, err := two()
		return "page.set_input_files(" + target + ", " + v + ")", err
	case "wait":
		ms, err := f.millis(a.Target)
		if err != nil {
			return "", fmt.Errorf("line %d: wait: %w", a.Line, err)
		}
		return "page.wait_for_timeout(" + ms + ")", nil
	}
	return "", fmt.Errorf("line %d: %q is not a browser action artemis exports", a.Line, a.Name)
}

// asserts emits the step's expects, in source order, one assert each.
//
// One expect is one assertion, which is the rule lower.Expect exists to make
// structural, and it survives the export: two assertions means two expect lines
// and two asserts. Nothing stops at the first failure under the interpreter and
// pytest does stop at the first assert, which is the one place a run and an
// export read differently -- and it is Python's contract, not something to work
// around with a helper that collects failures.
func (f *pyFile) asserts(st *lower.Step, indent int) error {
	for _, e := range st.Expects {
		line, err := f.assert(st, e)
		if err != nil {
			return fmt.Errorf("line %d: %w", e.Line, err)
		}
		f.line(indent, line)
	}
	return nil
}

// assert emits one expect.
//
// Outside a browser step `within` is accepted and ignored, which is what the
// interpreter does: an api or terminal observation is complete when the step
// returned, so re-asking would make a failure exactly `within` slower and no more
// likely to pass.
func (f *pyFile) assert(st *lower.Step, e *lower.Expect) (string, error) {
	if st.Type != browserKey {
		src, err := f.expr(e.Value, precOr)
		if err != nil {
			return "", err
		}
		return "assert " + src, nil
	}

	ms, secs, err := f.budget(e)
	if err != nil {
		return "", err
	}
	if native, ok, err := f.native(e.Value, ms); err != nil {
		return "", err
	} else if ok {
		return native, nil
	}
	src, err := f.expr(e.Value, precOr)
	if err != nil {
		return "", err
	}
	return "assert " + f.use(helperWithin) + "(lambda: " + src + ", " + secs + ")", nil
}

// budget is the assertion's `within` as milliseconds and as seconds.
//
// lower.BrowserWithin is the default rather than a number repeated here, so the
// generated timeout and the interpreter's cannot drift.
func (f *pyFile) budget(e *lower.Expect) (ms, secs string, err error) {
	if e.Budget == nil {
		d := e.Default
		if d == 0 {
			d = lower.BrowserWithin
		}
		return strconv.FormatInt(d.Milliseconds(), 10), formatSeconds(d), nil
	}
	if ms, err = f.millis(e.Budget); err != nil {
		return "", "", fmt.Errorf("within: %w", err)
	}
	secs, err = f.seconds(e.Budget)
	if err != nil {
		return "", "", fmt.Errorf("within: %w", err)
	}
	return ms, secs, nil
}

// captures emits the step's captures, in source order, one local each.
//
// After the asserts, because that is the order the runner applies them in, and
// as plain assignments because a capture is visible to the steps below it and a
// local is visible to the statements below it -- the same thing written in
// Python. A capture read in its own step is impossible: the checker refuses it.
func (f *pyFile) captures(st *lower.Step, indent int) error {
	for _, c := range st.Captures {
		src, err := f.expr(c.Value, precOr)
		if err != nil {
			return fmt.Errorf("line %d: capture %s: %w", c.Line, c.Name, err)
		}
		f.line(indent, local(c.Name)+" = "+src)
	}
	return nil
}

// value emits an expression in a position the interpreter renders to text: a
// URL, a header value, a command, a selector, a body.
//
// A string literal and an interpolated string are already Python strings, so
// they are emitted as they are; anything else goes through art_render, which is
// eval.Render -- a number as written, an object as compact JSON. Wrapping a
// string literal would be noise on the most common line in the file.
func (f *pyFile) value(x ast.Expr) (string, error) {
	src, err := f.expr(x, precOr)
	if err != nil {
		return "", err
	}
	if staticString(x) {
		return src, nil
	}
	return f.use(helperRender) + "(" + src + ")", nil
}

// trimmedValue is value with the surrounding space removed, for the positions
// where a stray space is certainly a mistake: a command's name, a header's name.
func (f *pyFile) trimmedValue(x ast.Expr) (string, error) {
	if l, ok := x.(*ast.Literal); ok && l.Kind() == token.String {
		return quote(strings.TrimSpace(l.Tok.Value)), nil
	}
	src, err := f.value(x)
	if err != nil {
		return "", err
	}
	return src + ".strip()", nil
}

// staticString reports whether x is certainly a string already: a string literal
// or an interpolation, both of which emit a Python str.
func staticString(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Literal:
		return x.Kind() == token.String
	case *ast.Interp:
		return true
	}
	return false
}

// count emits a whole-number position: `retry { times = 3 }`.
func (f *pyFile) count(x ast.Expr) (string, error) {
	if l, ok := x.(*ast.Literal); ok && l.Kind() == token.Number {
		if n, err := strconv.Atoi(l.Tok.Value); err == nil {
			return strconv.Itoa(n), nil
		}
	}
	src, err := f.expr(x, precOr)
	if err != nil {
		return "", err
	}
	return "int(" + src + ")", nil
}

// seconds emits a duration position as a number of seconds.
//
// A literal duration is parsed when the file is generated, so `timeout = "10s"`
// reads as `timeout=10.0`; one that came out of an expression goes through
// art_seconds, because its text is not known until the test runs.
func (f *pyFile) seconds(x ast.Expr) (string, error) {
	if d, ok := literalDuration(x); ok {
		return formatSeconds(d), nil
	}
	src, err := f.value(x)
	if err != nil {
		return "", err
	}
	return f.use(helperSeconds) + "(" + src + ")", nil
}

// millis is seconds in the unit Playwright takes.
func (f *pyFile) millis(x ast.Expr) (string, error) {
	if d, ok := literalDuration(x); ok {
		return strconv.FormatInt(d.Milliseconds(), 10), nil
	}
	src, err := f.value(x)
	if err != nil {
		return "", err
	}
	return "int(" + f.use(helperSeconds) + "(" + src + ") * 1000)", nil
}

// literalDuration reads a literal duration, and whether x was one. An
// interpolation is not: its text is only known at run time.
func literalDuration(x ast.Expr) (time.Duration, bool) {
	l, ok := x.(*ast.Literal)
	if !ok || l.Kind() != token.String {
		return 0, false
	}
	d, err := time.ParseDuration(l.Tok.Value)
	if err != nil || d < 0 {
		// The checker already rejected a literal that is not a duration, so this
		// is a tree that skipped it; art_seconds reports it at run time with the
		// text in the message.
		return 0, false
	}
	return d, true
}

// formatSeconds renders a duration as a Python float, always with a decimal
// point so a reader can see the unit is seconds and not milliseconds.
func formatSeconds(d time.Duration) string {
	s := strconv.FormatFloat(d.Seconds(), 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// line writes one line of Python at the given indentation level.
func (f *pyFile) line(indent int, text string) {
	f.out.WriteString(strings.Repeat("    ", indent))
	f.out.WriteString(text)
	f.out.WriteString("\n")
}

// callLine writes a call, on one line when it fits and one argument per line when it
// does not. The threshold is PEP 8's 88 columns, which is what black would do to
// the file anyway.
func (f *pyFile) callLine(indent int, callee string, args []string) {
	pad := strings.Repeat("    ", indent)
	oneLine := pad + callee + "(" + strings.Join(args, ", ") + ")"
	if len(oneLine) <= 88 && !strings.Contains(oneLine, "\n") {
		f.out.WriteString(oneLine + "\n")
		return
	}
	f.line(indent, callee+"(")
	for _, a := range args {
		f.line(indent+1, a+",")
	}
	f.line(indent, ")")
}

// pyQuoteComment renders text for a `#` comment: control characters become
// spaces, so a scenario name with a newline escape in it cannot turn the rest of
// the comment into code.
func pyQuoteComment(text string) string { return "\"" + comment(text) + "\"" }

func comment(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r < 0x20 {
			return ' '
		}
		return r
	}, text)
}
