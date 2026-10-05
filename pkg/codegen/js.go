package codegen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/dsl/token"
)

// JS is the vitest target: `artemis build --lang=js`.
//
// It emits one module per .art file, holding one test per scenario, with each
// step's action and assertions as ordered statements inside it. The libraries are
// the ones the design document names -- fetch for an api step, child_process for
// a terminal step, @playwright/test for a browser step -- and nothing else:
// there is no generated artemis runtime beyond the handful of helpers in
// jshelpers.go, because a file nobody can read is a file nobody will adopt.
//
// It is the mirror of Python, file for file, so the two can be read side by side
// and a rule in one and not the other is visible. What differs is the three
// things the languages differ about: an element read is a promise, a path read
// needs a helper because JavaScript's own does not throw, and vitest has no
// fixtures.
//
// # The one trap vitest sets
//
// Vite overwrites process.env.BASE_URL with its own base path and process.env
// .NODE_ENV with the mode it ran in, so `env("BASE_URL")` in a .art file reads
// "/" under the generated vitest and the real variable under `artemis run`. It
// cannot be worked around from here -- the name is Vite's before the module
// loads -- so it is listed in every generated file's header, and the conformance
// corpus parameterises itself with ARTEMIS_BASE_URL for the same reason.
type JS struct{}

func (JS) Name() string { return "js" }

// Generate walks the lowered scenarios and returns the one module they make.
//
// The same lower.File call Python.Generate makes, with the same nil Info for the
// same reason: the only thing lower reads off it is each expect's
// simple/complex class, which is a fact about what a form can render and nothing
// a target needs. Nothing is evaluated -- Step.Model is never called -- so every
// value position is still an ast.Expr when it reaches jsexpr.go.
func (JS) Generate(tree *ast.File) ([]GeneratedFile, error) {
	scenarios, err := lower.File(tree, nil)
	if err != nil {
		return nil, err
	}

	source := SourceName(tree)
	if source == "" {
		// A tree built in Go, or decoded from JSON, has no spans to name it.
		source = "artemis" + artExt
	}

	f := &jsFile{used: map[string]bool{}, imports: map[string]bool{}}
	// Which module `expect` comes from is a fact about the whole file, and it has
	// to be known before the first assertion is emitted -- so the scenarios are
	// asked about browser steps up front rather than discovered on the way
	// through.
	for _, sc := range scenarios {
		if hasBrowserStep(sc) {
			f.browser = true
		}
	}
	for _, sc := range scenarios {
		if err := f.scenario(sc); err != nil {
			return nil, err
		}
	}
	return []GeneratedFile{{
		Path:    jsFileName(source),
		Content: f.module(source),
	}}, nil
}

// jsFile accumulates one module: the tests, which helpers they reached for, and
// which bindings the module imports.
//
// step is the registry key of the step being emitted, "" between steps, and it
// is what makes an identifier resolve the way eval.Env.lookup resolves it.
type jsFile struct {
	out     strings.Builder
	used    map[string]bool
	imports map[string]bool
	step    string

	// browser is whether any scenario in the file holds a browser step, which is
	// what decides where `expect` is imported from.
	browser bool

	// wrote is whether the test being emitted has a body line yet, which is what
	// decides whether a step gets a blank line above it. An arrow function
	// followed by a blank line reads as a mistake.
	wrote bool
}

// use records that a helper is needed and returns its JavaScript name, so a call
// site and the definition it needs cannot get out of step.
//
// Dependencies are pulled transitively: art_contains calls art_render and
// art_eq, so using the first uses both.
func (f *jsFile) use(name string) string {
	if f.used[name] {
		return name
	}
	f.used[name] = true
	for _, m := range jsByName[name].imports {
		f.imports[m] = true
	}
	for _, need := range jsByName[name].needs {
		f.use(need)
	}
	return name
}

// need records a binding the emitted code imports for a reason that is not a
// helper: `test` for every scenario, `spawnSync` for a terminal step. The import
// block is the union of these and the helpers' own.
func (f *jsFile) need(member string) { f.imports[member] = true }

// expectName is `expect`, from vitest or from @playwright/test.
//
// Playwright's expect is a superset of the jest-compatible one, so one spelling
// serves the boolean assertions and the web-first matchers both, and a module
// never imports two bindings called `expect`. Which module it is depends on the
// file and not on the assertion, which is why f.browser is settled before the
// walk.
func (f *jsFile) expectName() string {
	if f.browser {
		f.need("@playwright/test.expect")
	} else {
		f.need("vitest.expect")
	}
	return "expect"
}

// module is the whole file: header, imports, helpers, tests.
//
// It is assembled after the walk rather than streamed, because which imports and
// which helpers the file needs is only known once every expression has been
// emitted.
func (f *jsFile) module(source string) string {
	var b strings.Builder
	b.WriteString(jsHeader(source))

	if block := f.importBlock(); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
	}

	for _, h := range jsHelpers {
		if !f.used[h.name] {
			continue
		}
		b.WriteString("\n")
		b.WriteString(h.src)
	}

	body := f.out.String()
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
	}
	return b.String()
}

// importBlock is the import statements, grouped the way a JavaScript repo groups
// them: node's own builtins first, then the packages, with a blank line between.
//
// One statement per module with its bindings sorted, so the block is a function
// of what the body used and nothing else -- which is what makes two runs over an
// unchanged file produce the same bytes.
func (f *jsFile) importBlock() string {
	var groups []string
	for _, group := range jsImportGroups {
		var lines []string
		for _, module := range group {
			var members []string
			for member := range f.imports {
				if mod, name, ok := strings.Cut(member, "."); ok && mod == module {
					members = append(members, name)
				}
			}
			if len(members) == 0 {
				continue
			}
			sort.Strings(members)
			lines = append(lines, "import { "+strings.Join(members, ", ")+
				" } from \""+module+"\";")
		}
		if len(lines) > 0 {
			groups = append(groups, strings.Join(lines, "\n"))
		}
	}
	if len(groups) == 0 {
		return ""
	}
	return strings.Join(groups, "\n\n") + "\n"
}

// jsHeader is the first thing in every generated file.
//
// It says the export is one-way, because that is the decision a reader most
// needs to know -- artemis never reads this file back, so editing it is safe and
// regenerating it is destructive -- it names the two packages the module needs,
// and it lists the three places the generated test and `artemis run` differ, so
// none of it has to be discovered.
//
// No version and no timestamp. Regenerating a scenario that has not changed
// produces the same bytes, which is what makes `artemis build` safe to run in a
// loop and a diff mean the scenario moved.
func jsHeader(source string) string {
	return "// Generated by `artemis build --lang=js` from " + comment(source) + ".\n" +
		"//\n" +
		"// This is a ONE-WAY EXPORT. Artemis does not read it back: the .art file is the\n" +
		"// source of truth, and running the command again overwrites this file. Once you\n" +
		"// decide to own this instead, edit it freely and stop generating it.\n" +
		"//\n" +
		"// Run it with `npx vitest run`. It needs `npm install -D vitest`, and\n" +
		"// `@playwright/test` as well for a scenario with a browser step.\n" +
		"//\n" +
		"// Where this differs from `artemis run` on the same scenario:\n" +
		"//   - a comparison uses JavaScript's rules, so `status == \"200\"` does not hold\n" +
		"//     against a JSON 200 the way it does under the interpreter;\n" +
		"//   - a step that wrote no `timeout` has none here; artemis applies a default of\n" +
		"//     its own per step type;\n" +
		"//   - stdout and stderr are not truncated; artemis caps them for its report;\n" +
		"//   - a variable with no value raises at the point of use. `artemis run`\n" +
		"//     reports all of them before the first step; an exported module has no\n" +
		"//     such moment;\n" +
		"//   - vite owns two environment variables, so `env(\"BASE_URL\")` reads vite's\n" +
		"//     own base path here and `env(\"NODE_ENV\")` reads the mode vitest ran in.\n" +
		"//     Name yours something else.\n"
}

// hasBrowserStep reports whether the scenario holds a step that needs a page.
func hasBrowserStep(sc *lower.Scenario) bool {
	for _, st := range sc.Steps {
		if st.Type == browserKey {
			return true
		}
	}
	return false
}

// scenario emits one test.
//
// The scenario's name goes in as a string and not as an identifier, because a
// vitest test is named by one -- so there is nothing to slug and nothing to keep
// apart: "sign in" and "sign-in" are two different tests without help.
func (f *jsFile) scenario(sc *lower.Scenario) error {
	f.need("vitest.test")
	needsPage := hasBrowserStep(sc)

	if f.out.Len() > 0 {
		f.out.WriteString("\n")
	}
	f.line(0, "// scenario "+jsQuoteComment(sc.Name)+", line "+strconv.Itoa(sc.Line))
	f.line(0, "test("+jsQuote(sc.Name)+", async () => {")

	// Every name the body assigns, declared once.
	//
	// Three facts force it. A `capture b` is legal in two steps of one scenario,
	// so a declaration per binding would be a redeclaration. `const` is
	// block-scoped where a Python assignment is function-scoped, so two api
	// steps would both declare `resp`. And a `retry` is a callback, so a capture
	// written inside one has to outlive it. The DSL's scope for a binding is the
	// scenario, and this is the faithful spelling of that.
	declared := declarations(sc)
	if len(declared) > 0 {
		f.declare(declared)
	}

	// The vars first, in source order, because a var sees the vars above it and
	// nothing else -- the checker's rule, and only meaningful if they are bound
	// in reading order. f.step is "" here: a var's value has no roots.
	f.step = ""
	for _, v := range sc.Vars {
		src, err := f.expr(v.Value, jsPrecOr)
		if err != nil {
			return fmt.Errorf("scenario %q: var %s: %w", sc.Name, v.Name, err)
		}
		f.line(1, jsLocal(v.Name)+" = "+src.src+";")
	}

	if needsPage {
		open, err := f.openPage(sc)
		if err != nil {
			return fmt.Errorf("scenario %q: %w", sc.Name, err)
		}
		f.line(1, "const page = "+open+";")
	}
	f.wrote = len(declared) > 0 || needsPage

	for _, st := range sc.Steps {
		if err := f.emitStep(st); err != nil {
			return fmt.Errorf("scenario %q: %w", sc.Name, err)
		}
	}
	if !f.wrote {
		// A scenario with no bindings and no steps is a test with an empty body,
		// written as one: an arrow function whose braces are on two lines reads
		// as a body that went missing.
		f.trimLast()
		f.out.WriteString("test(" + jsQuote(sc.Name) + ", async () => {});\n")
		return nil
	}
	f.line(0, "});")
	return nil
}

// trimLast drops the line just written, for the one place a decision is only
// made after it: a scenario whose body turned out to be empty.
func (f *jsFile) trimLast() {
	src := f.out.String()
	src = strings.TrimSuffix(src, "\n")
	if i := strings.LastIndexByte(src, '\n'); i >= 0 {
		src = src[:i+1]
	} else {
		src = ""
	}
	f.out.Reset()
	f.out.WriteString(src)
}

// declarations is every name the test's body assigns: the scenario's vars, its
// captures, and the roots and locals of each step type it uses.
//
// The order is fixed -- vars and captures in source order, then the step types
// in check.StepTypes()' order -- so the declaration line is a function of the
// scenario and not of a map walk.
func declarations(sc *lower.Scenario) []string {
	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}

	for _, v := range sc.Vars {
		add(jsLocal(v.Name))
	}
	for _, st := range sc.Steps {
		for _, c := range st.Captures {
			add(jsLocal(c.Name))
		}
	}

	used := map[string]bool{}
	for _, st := range sc.Steps {
		used[st.Type] = true
	}
	for _, t := range check.StepTypes() {
		key, ok := lower.TypeKey(t)
		if !ok || !used[key] || key == browserKey {
			// A browser step's root is `page`, which is bound once as a const
			// before the first step rather than assigned per step.
			continue
		}
		for _, root := range jsRootList[key] {
			add(root)
		}
		add(jsStepLocal(key))
	}
	return names
}

// declare writes the `let` line, filled across as many lines as it needs.
//
// Filled rather than one name per line, because the list is a list and not a
// structure: `let base, id, status, body, raw;` is one fact about the scenario
// and five lines of it would read as five.
func (f *jsFile) declare(names []string) {
	const width = 80
	line := "  let "
	for i, name := range names {
		piece := name
		if i < len(names)-1 {
			piece += ","
		} else {
			piece += ";"
		}
		switch {
		case strings.HasSuffix(line, " "):
			line += piece
		case len(line)+1+len(piece) <= width:
			line += " " + piece
		default:
			f.out.WriteString(line + "\n")
			line = "    " + piece
		}
	}
	f.out.WriteString(line + "\n")
}

// openPage is the call that gives the scenario its page.
//
// `config browser`'s two settings are expressions, so they are emitted as
// expressions: `headless = env("HEADLESS") == "1"` needs nothing special here. A
// setting the scenario did not write is left off the call, so art_browser's own
// default applies and "the scenario said nothing" stays different from "the
// scenario said false".
func (f *jsFile) openPage(sc *lower.Scenario) (string, error) {
	open := "await " + f.use(jsHelperBrowser)
	if sc.Browser == nil {
		return open + "()", nil
	}
	var args []string
	if sc.Browser.Headless != nil {
		src, err := f.expr(sc.Browser.Headless, jsPrecOr)
		if err != nil {
			return "", fmt.Errorf("config browser headless: %w", err)
		}
		args = append(args, "headless: "+src.src)
	}
	if sc.Browser.Viewport != nil {
		src, err := f.value(sc.Browser.Viewport)
		if err != nil {
			return "", fmt.Errorf("config browser viewport: %w", err)
		}
		args = append(args, "viewport: "+src.src)
	}
	if len(args) == 0 {
		return open + "()", nil
	}
	return open + "({ " + strings.Join(args, ", ") + " })", nil
}

// emitStep emits one step: a comment naming it, its action, its assertions and
// its captures, wrapped in the retry callback when it wrote one.
func (f *jsFile) emitStep(st *lower.Step) error {
	f.step = st.Type
	defer func() { f.step = "" }()

	if f.wrote {
		f.out.WriteString("\n")
	}
	f.wrote = true
	f.line(1, "// step "+jsQuoteComment(st.Name)+", line "+strconv.Itoa(st.Line))

	indent := 1
	if retried(st) {
		loop, err := f.retryCall(st)
		if err != nil {
			return fmt.Errorf("step %q: %w", st.Name, err)
		}
		f.line(1, loop)
		indent = 2
	}

	if err := f.action(st, indent); err != nil {
		return fmt.Errorf("step %q: %w", st.Name, err)
	}
	if err := f.asserts(st, indent); err != nil {
		return fmt.Errorf("step %q: %w", st.Name, err)
	}
	if err := f.captures(st, indent); err != nil {
		return fmt.Errorf("step %q: %w", st.Name, err)
	}
	if retried(st) {
		f.line(1, "});")
	}
	return nil
}

// retryCall is the `await art_retry({ ... }, async () => {` line.
//
// times and delay are expressions, so a count out of a var works; a literal
// number and a literal duration are converted here, which is what keeps
// `retry { times = 3, delay = "1s" }` reading as `{ times: 3, delay: 1 }`.
func (f *jsFile) retryCall(st *lower.Step) (string, error) {
	var args []string
	if st.Retry.Times != nil {
		src, err := f.count(st.Retry.Times)
		if err != nil {
			return "", fmt.Errorf("retry times: %w", err)
		}
		args = append(args, "times: "+src)
	}
	if st.Retry.Delay != nil {
		src, err := f.seconds(st.Retry.Delay)
		if err != nil {
			return "", fmt.Errorf("retry delay: %w", err)
		}
		args = append(args, "delay: "+src)
	}
	return "await " + f.use(jsHelperRetry) + "({ " + strings.Join(args, ", ") +
		" }, async () => {", nil
}

// action emits the step's action and binds the roots it observes.
func (f *jsFile) action(st *lower.Step, indent int) error {
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

// request emits an api step: one fetch call, then the four roots.
//
// The roots are four statements and not one destructuring, because `raw` has to
// be awaited and `body` is read off it: a fetch body is a stream that can be
// read once, where requests buffers -- so `resp.text()` happens and art_json
// parses that text rather than asking the response twice.
func (f *jsFile) request(st *lower.Step, indent int) error {
	r := st.Request

	url, err := f.value(r.URL)
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}
	target := url.src
	if len(r.Query) > 0 {
		query, err := f.query(r.Query)
		if err != nil {
			return err
		}
		target = f.use(jsHelperURL) + "(" + target + ", [" + query + "])"
	}

	options := []string{"method: " + jsQuote(strings.ToUpper(r.Method))}
	headers, err := f.params(r.Headers, true)
	if err != nil {
		return err
	}
	if headers != "" {
		options = append(options, "headers: { "+headers+" }")
	}
	if r.Body != nil {
		// The rendered expression, which is what the interpreter sends: no
		// Content-Type the scenario did not write, and no second spelling of a
		// JSON body.
		body, err := f.value(r.Body)
		if err != nil {
			return fmt.Errorf("body: %w", err)
		}
		options = append(options, "body: "+body.src)
	}
	if st.Timeout != nil {
		ms, err := f.millis(st.Timeout)
		if err != nil {
			return fmt.Errorf("timeout: %w", err)
		}
		options = append(options, "signal: AbortSignal.timeout("+ms+")")
	}

	f.optionsCall(indent, "resp = await fetch", []string{target}, options)
	f.line(indent, "raw = await resp.text();")
	f.line(indent, "status = resp.status;")
	f.line(indent, "body = "+f.use(jsHelperJSON)+"(raw);")
	f.line(indent, "headers = resp.headers;")
	return nil
}

// params emits the `"name": value` pairs of a header or env block.
//
// A header's name is trimmed, because a space around one is certainly a mistake
// and sending "Authorization " is the kind of mistake that costs an afternoon.
func (f *jsFile) params(ps []*lower.Param, trim bool) (string, error) {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		name, static, err := f.paramName(p, trim)
		if err != nil {
			return "", err
		}
		value, err := f.value(p.Value)
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		if !static {
			name = "[" + name + "]"
		}
		parts = append(parts, name+": "+value.src)
	}
	return strings.Join(parts, ", "), nil
}

// query emits the ordered pair list a request's query parameters become.
//
// A list of pairs rather than an object, because source order is kept and a
// repeated `query "tag"` keeps both values -- which an object would silently
// collapse. URLSearchParams does the percent-escaping that lower.queryString
// does by hand.
func (f *jsFile) query(ps []*lower.Param) (string, error) {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		name, _, err := f.paramName(p, true)
		if err != nil {
			return "", err
		}
		value, err := f.value(p.Value)
		if err != nil {
			return "", fmt.Errorf("query %s: %w", name, err)
		}
		parts = append(parts, "["+name+", "+value.src+"]")
	}
	return strings.Join(parts, ", "), nil
}

// paramName is a parameter's name as an expression, and whether it is a name
// written in the file: the quoted key, or the field name itself for an
// `env { ... }` setting. See lower.Param.
//
// Quoted either way, because an environment variable's name is an identifier in
// the .art file and not necessarily one in JavaScript. The second answer is what
// decides whether an object key needs computed-key brackets -- `{ "X-Trace": v }`
// where the name was written and `{ [name]: v }` where it was not.
func (f *jsFile) paramName(p *lower.Param, trim bool) (src string, static bool, err error) {
	if p.Key == nil {
		name := p.Name
		if trim {
			name = strings.TrimSpace(name)
		}
		return jsQuote(name), true, nil
	}
	if l, ok := p.Key.(*ast.Literal); ok && l.Kind() == token.String {
		value := l.Tok.Value
		if trim {
			value = strings.TrimSpace(value)
		}
		return jsQuote(value), true, nil
	}
	if trim {
		out, err := f.trimmedValue(p.Key)
		return out.src, false, err
	}
	out, err := f.value(p.Key)
	return out.src, false, err
}

// run emits a terminal step: one spawnSync call, then the three roots.
//
// spawnSync and not execFileSync, because a command that ran and exited non-zero
// is an observation rather than an error -- `expect exit_code == 0` is what
// decides whether the scenario minds, which is the same reason the Python export
// passes check=False. encoding: "utf8", because the roots are strings in the
// evaluator's domain. shell is left at its default of false: there is no word
// splitting anywhere, which is why `args` is a list.
func (f *jsFile) run(st *lower.Step, indent int) error {
	f.need("node:child_process.spawnSync")
	r := st.Run

	command, err := f.trimmedValue(r.Command)
	if err != nil {
		return fmt.Errorf("command: %w", err)
	}
	argv, err := f.argv(r)
	if err != nil {
		return err
	}

	options := []string{}
	if r.Cwd != nil {
		cwd, err := f.value(r.Cwd)
		if err != nil {
			return fmt.Errorf("cwd: %w", err)
		}
		options = append(options, "cwd: "+cwd.src)
	}
	if r.Stdin != nil {
		stdin, err := f.value(r.Stdin)
		if err != nil {
			return fmt.Errorf("stdin: %w", err)
		}
		options = append(options, "input: "+stdin.src)
	}
	if len(r.Env) > 0 {
		// Layered over the process environment, which is what the interpreter
		// does: a step's `env` adds to and overrides, it does not replace.
		env, err := f.params(r.Env, false)
		if err != nil {
			return err
		}
		options = append(options, "env: { ...process.env, "+env+" }")
	}
	options = append(options, "encoding: \"utf8\"")
	if st.Timeout != nil {
		ms, err := f.millis(st.Timeout)
		if err != nil {
			return fmt.Errorf("timeout: %w", err)
		}
		options = append(options, "timeout: "+ms)
	}

	f.optionsCall(indent, "proc = spawnSync", []string{command.src, argv}, options)
	f.line(indent, "exit_code = "+f.use(jsHelperExit)+"(proc);")
	f.line(indent, "stdout = proc.stdout ?? \"\";")
	f.line(indent, "stderr = proc.stderr ?? \"\";")
	return nil
}

// argv is the argument list spawnSync is handed.
//
// A literal `args = [...]` becomes a plain JavaScript array, which is what a
// JavaScript author would have written. An `args` that is an expression -- a var
// holding a list -- goes through art_argv, which is where "it has to be an
// array" and "every element is rendered" are enforced at run time.
func (f *jsFile) argv(r *lower.Run) (string, error) {
	switch list := r.Args.(type) {
	case nil:
		return "[]", nil
	case *ast.Array:
		parts := make([]string, 0, len(list.Elems))
		for _, e := range list.Elems {
			v, err := f.value(e.Value)
			if err != nil {
				return "", fmt.Errorf("args: %w", err)
			}
			parts = append(parts, v.src)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	default:
		src, err := f.expr(r.Args, jsPrecOr)
		if err != nil {
			return "", fmt.Errorf("args: %w", err)
		}
		return f.use(jsHelperArgv) + "(" + src.src + ")", nil
	}
}

// acts emits a browser step: the step's own timeout, then one Playwright call
// per act in source order.
//
// Source order is the only control flow a browser step has: `fill` then `click`
// is a form submitted and the reverse is not.
func (f *jsFile) acts(st *lower.Step, indent int) error {
	if st.Timeout != nil {
		// The step's `timeout` bounds Playwright's own auto-waiting, which is
		// what it does under the interpreter. Set once for the block rather than
		// per act: `timeout = "5s"` means five seconds for the step.
		ms, err := f.millis(st.Timeout)
		if err != nil {
			return fmt.Errorf("timeout: %w", err)
		}
		f.line(indent, "page.setDefaultTimeout("+ms+");")
	}
	for _, a := range st.Acts {
		line, err := f.act(a)
		if err != nil {
			return err
		}
		f.line(indent, line+";")
	}
	return nil
}

// act is one browser action as one Playwright call.
//
// Eight actions, eight calls, and no default that a .art file can reach: the
// checker rejects any other name with a did-you-mean. `press` takes a key and no
// selector, so it goes to the keyboard rather than to an element -- sending it to
// a selector would type into something other than the field a `fill` just
// filled.
func (f *jsFile) act(a *lower.Act) (string, error) {
	target, err := f.value(a.Target)
	if err != nil {
		return "", fmt.Errorf("line %d: %s: %w", a.Line, a.Name, err)
	}
	two := func() (string, error) {
		v, err := f.value(a.Value)
		if err != nil {
			return "", fmt.Errorf("line %d: %s: value: %w", a.Line, a.Name, err)
		}
		return v.src, nil
	}

	switch a.Name {
	case "goto":
		return "await " + f.use(jsHelperGoto) + "(page, " + target.src + ")", nil
	case "click":
		return "await page.click(" + target.src + ")", nil
	case "hover":
		return "await page.hover(" + target.src + ")", nil
	case "press":
		return "await page.keyboard.press(" + target.src + ")", nil
	case "fill":
		v, err := two()
		return "await page.fill(" + target.src + ", " + v + ")", err
	case "select":
		// By the option's value, which is what SPEC.md says: `select "#plan" =
		// "pro"` is the option whose value attribute is "pro", not the one whose
		// text reads "pro".
		v, err := two()
		return "await page.selectOption(" + target.src + ", { value: " + v + " })", err
	case "upload":
		v, err := two()
		return "await page.setInputFiles(" + target.src + ", " + v + ")", err
	case "wait":
		ms, err := f.millis(a.Target)
		if err != nil {
			return "", fmt.Errorf("line %d: wait: %w", a.Line, err)
		}
		return "await page.waitForTimeout(" + ms + ")", nil
	}
	return "", fmt.Errorf("line %d: %q is not a browser action artemis exports", a.Line, a.Name)
}

// asserts emits the step's expects, in source order, one assertion each.
//
// One expect is one assertion, which is the rule lower.Expect exists to make
// structural, and it survives the export. Nothing stops at the first failure
// under the interpreter and vitest does stop at the first failed assertion,
// which is the one place a run and an export read differently -- and it is the
// runner's contract, not something to work around with a helper that collects
// failures.
func (f *jsFile) asserts(st *lower.Step, indent int) error {
	for _, e := range st.Expects {
		line, err := f.assert(st, e)
		if err != nil {
			return fmt.Errorf("line %d: %w", e.Line, err)
		}
		f.line(indent, line+";")
	}
	return nil
}

// assert emits one expect.
//
// The source of the expression is carried as the assertion's message. pytest
// rewrites a bare assert and prints the operands; JavaScript has no equivalent,
// so the source text is the only way to keep "the failure names the expression",
// which is what the interpreter's own report is built on.
//
// Outside a browser step `within` is accepted and ignored, which is what the
// interpreter does: an api or terminal observation is complete when the step
// returned, so re-asking would make a failure exactly `within` slower and no
// more likely to pass.
func (f *jsFile) assert(st *lower.Step, e *lower.Expect) (string, error) {
	if st.Type != browserKey {
		src, err := f.expr(e.Value, jsPrecOr)
		if err != nil {
			return "", err
		}
		return f.boolean(src.src, e.Value), nil
	}

	ms, secs, err := f.budget(e)
	if err != nil {
		return "", err
	}
	if native, ok, err := f.jsNative(e.Value, ms); err != nil {
		return "", err
	} else if ok {
		return native, nil
	}
	src, err := f.expr(e.Value, jsPrecOr)
	if err != nil {
		return "", err
	}
	return f.boolean(
		"await "+f.use(jsHelperWithin)+"("+thunk(src)+", "+secs+")", e.Value), nil
}

// boolean is the assertion an expression with no native matcher becomes.
func (f *jsFile) boolean(src string, x ast.Expr) string {
	return f.expectName() + "(" + src + ", " + jsQuoteSingle(exprSource(x)) + ").toBe(true)"
}

// budget is the assertion's `within` as milliseconds and as seconds.
//
// lower.BrowserWithin is the default rather than a number repeated here, so the
// generated timeout and the interpreter's cannot drift.
func (f *jsFile) budget(e *lower.Expect) (ms, secs string, err error) {
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

// captures emits the step's captures, in source order, one assignment each.
//
// After the asserts, because that is the order the runner applies them in, and
// as assignments to a name declared at the top of the test -- so a capture
// written inside a retry callback is still there for the steps below it. A
// capture read in its own step is impossible: the checker refuses it.
func (f *jsFile) captures(st *lower.Step, indent int) error {
	for _, c := range st.Captures {
		src, err := f.expr(c.Value, jsPrecOr)
		if err != nil {
			return fmt.Errorf("line %d: capture %s: %w", c.Line, c.Name, err)
		}
		f.line(indent, jsLocal(c.Name)+" = "+src.src+";")
	}
	return nil
}

// value emits an expression in a position the interpreter renders to text: a
// URL, a header value, a command, a selector, a body.
//
// A string literal and an interpolated string are already JavaScript strings, so
// they are emitted as they are; anything else goes through art_render, which is
// eval.Render -- a number as written, an object as compact JSON. Wrapping a
// string literal would be noise on the most common line in the file.
func (f *jsFile) value(x ast.Expr) (jsExpr, error) {
	src, err := f.expr(x, jsPrecOr)
	if err != nil {
		return jsExpr{}, err
	}
	if staticString(x) {
		return src, nil
	}
	return atom(f.use(jsHelperRender)+"("+src.src+")", src.await), nil
}

// trimmedValue is value with the surrounding space removed, for the positions
// where a stray space is certainly a mistake: a command's name, a header's name.
func (f *jsFile) trimmedValue(x ast.Expr) (jsExpr, error) {
	if l, ok := x.(*ast.Literal); ok && l.Kind() == token.String {
		return atom(jsQuote(strings.TrimSpace(l.Tok.Value)), false), nil
	}
	src, err := f.value(x)
	if err != nil {
		return jsExpr{}, err
	}
	return atom(src.src+".trim()", src.await), nil
}

// count emits a whole-number position: `retry { times = 3 }`.
func (f *jsFile) count(x ast.Expr) (string, error) {
	if l, ok := x.(*ast.Literal); ok && l.Kind() == token.Number {
		if n, err := strconv.Atoi(l.Tok.Value); err == nil {
			return strconv.Itoa(n), nil
		}
	}
	src, err := f.expr(x, jsPrecOr)
	if err != nil {
		return "", err
	}
	return "Math.trunc(Number(" + src.src + "))", nil
}

// seconds emits a duration position as a number of seconds.
//
// A literal duration is parsed when the file is generated, so `delay = "1s"`
// reads as `delay: 1`; one that came out of an expression goes through
// art_seconds, because its text is not known until the test runs.
func (f *jsFile) seconds(x ast.Expr) (string, error) {
	if d, ok := literalDuration(x); ok {
		return formatSeconds(d), nil
	}
	src, err := f.value(x)
	if err != nil {
		return "", err
	}
	return f.use(jsHelperSeconds) + "(" + src.src + ")", nil
}

// millis is seconds in the unit Playwright, fetch and spawnSync all take.
func (f *jsFile) millis(x ast.Expr) (string, error) {
	if d, ok := literalDuration(x); ok {
		return strconv.FormatInt(d.Milliseconds(), 10), nil
	}
	src, err := f.value(x)
	if err != nil {
		return "", err
	}
	return "Math.round(" + f.use(jsHelperSeconds) + "(" + src.src + ") * 1000)", nil
}

// line writes one line of JavaScript at the given indentation level. Two spaces,
// which is what prettier would do to the file anyway.
func (f *jsFile) line(indent int, text string) {
	f.out.WriteString(strings.Repeat("  ", indent))
	f.out.WriteString(text)
	f.out.WriteString("\n")
}

// optionsCall writes a call whose last argument is an options object -- fetch and
// spawnSync are both shaped that way -- on one line when it fits, and otherwise
// with the object's settings one per line.
//
// The threshold is prettier's 80 columns, and the three forms are the three
// prettier would choose: everything on one line, the object broken out, or every
// argument broken out because the ones before the object did not fit either.
func (f *jsFile) optionsCall(indent int, callee string, args, options []string) {
	pad := strings.Repeat("  ", indent)
	object := "{}"
	if len(options) > 0 {
		object = "{ " + strings.Join(options, ", ") + " }"
	}
	if one := pad + callee + "(" + strings.Join(append(args, object), ", ") + ");"; len(one) <= 80 {
		f.out.WriteString(one + "\n")
		return
	}

	head := pad + callee + "(" + strings.Join(args, ", ")
	if len(args) > 0 {
		head += ", "
	}
	if len(head)+1 <= 80 {
		f.out.WriteString(head + "{\n")
		for _, o := range options {
			f.line(indent+1, o+",")
		}
		f.line(indent, "});")
		return
	}

	f.line(indent, callee+"(")
	for _, a := range args {
		f.line(indent+1, a+",")
	}
	if len(pad)+2+len(object)+1 <= 80 {
		f.line(indent+1, object+",")
	} else {
		f.line(indent+1, "{")
		for _, o := range options {
			f.line(indent+2, o+",")
		}
		f.line(indent+1, "},")
	}
	f.line(indent, ");")
}

// jsQuoteComment renders text for a `//` comment: control characters become
// spaces, so a scenario name with a newline escape in it cannot turn the rest of
// the comment into code.
func jsQuoteComment(text string) string { return "\"" + comment(text) + "\"" }

// exprSource is the .art source of an expression, for an assertion's message.
//
// The tokens' own text, with a single space wherever the file had any separation
// at all -- not ast.Source, whose trivia would carry a `#` comment and a newline
// into the middle of a string literal in the generated file.
func exprSource(x ast.Expr) string {
	if x == nil {
		return ""
	}
	var b strings.Builder
	for _, t := range x.Tokens(nil) {
		if b.Len() > 0 && len(t.Leading) > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(t.Text)
		if len(t.Trailing) > 0 {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
