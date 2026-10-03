// Package migrate converts a YAML scenario into Artemis DSL source.
//
// It is the adoption path, and the reason YAML is replaced rather than
// deleted: a suite converts once, in one command, and the output is a file
// `artemis fmt` would not change.
//
//	artemis migrate -f old.yaml [-o new.art]
//
// # How the output is produced
//
// Migration writes near-canonical .art *text* and then hands it to
// pkg/dsl/parser and pkg/dsl/print. The printed source is what a caller gets,
// so R3's "through the canonical printer" is true by construction rather than
// by this package reimplementing layout.
//
// Emitting text rather than building an ast.File is a deliberate trade. A
// concrete tree needs a synthetic token.Token for every brace, comma and `=`,
// and an encode document needs a hand-built object per node kind; text is a
// fraction of either. The round trip is also a real check: a construct this
// package spells wrongly fails to parse, which is a test failure naming the
// source, where a hand-built tree would have printed something plausible.
//
// # What it refuses
//
// A YAML construct with no DSL spelling is an error naming the step and the
// construct: a wildcard, descent or filter in a JSON path, `operator: empty` on
// a stream, an unclosed `{{`, a step type nothing registers. A migrator that
// half-translates a file hands back a scenario that compiles and asserts
// something else, which is worse than one that will not convert.
//
// # What it drops
//
// Comments above the document and above each step carry (see Comments).
// `type: functional` has no DSL counterpart and is dropped -- the language has
// no scenario type. Everything else is translated.
package migrate

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"
	"artemis/pkg/shared/models"
)

// The step types migration knows how to translate, spelled as pkg/steps spells
// them. They are written out rather than imported from httpstep and execstep so
// that the YAML reader does not grow a dependency on the executors; the corpus
// test migrates every registered type, which is what keeps the two in step.
const (
	apiStep  = "api"
	execStep = "exec"
)

// Source is config as .art source.
//
// comments is what ReadComments found in the same file, and may be the zero
// value -- a scenario built in Go has none.
//
// An error means the scenario holds something with no DSL spelling, and names
// the step it was in. A file that errors is not partially written: the caller
// gets no source at all.
func Source(config models.Config, comments Comments) (string, error) {
	w := &writer{}
	w.comment(comments.File)
	w.line("scenario " + quote(config.Name) + " {")
	w.indent++

	for _, v := range config.Variables {
		value, err := templated(v.Value)
		if err != nil {
			return "", fmt.Errorf("variable %q: %w", v.Name, err)
		}
		w.line("var " + v.Name + " = " + value)
	}

	for i := range config.Steps {
		if err := step(w, config.Steps[i], i, comments.stepComment(i)); err != nil {
			return "", err
		}
	}

	w.indent--
	w.line("}")

	return canonical(w.String())
}

// canonical parses the emitted source and prints it canonically.
//
// A parse error here is a bug in this package and nothing the author of the
// YAML file can act on, so it says so and shows the source: the alternative is
// writing a .art file that does not parse and letting the next command find
// out. The checker is deliberately not run -- `bad_retry.yaml`'s `delay:
// "soon"` and `template_error.yaml`'s `{{tokn}}` migrate to files that parse
// clean and fail the checker, which is the design moving two run-time failures
// to compile time, and refusing to write them would hide the very thing
// migration is meant to surface.
func canonical(src string) (string, error) {
	tree, bag := parser.Parse("migrated.art", src)
	if bag.HasErrors() {
		return "", fmt.Errorf("migration produced source that does not parse, which is a bug in artemis: %v\n%s",
			bag.All()[0], src)
	}
	return print.Canonical(tree), nil
}

// step writes one step: its action, its fields, its expects, its captures.
//
// The order is the order a .art file reads in -- what it does, how long it may
// take, what is expected of it, what is kept from it -- and is the order ART-38
// wrote the hand-written fixtures in.
func step(w *writer, s models.Step, i int, comment string) error {
	where := fmt.Sprintf("step %d %q", i+1, s.Name)

	w.comment(comment)
	w.line("step " + quote(s.Name) + " {")
	w.indent++

	if err := action(w, s); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if s.Timeout != "" {
		w.line("timeout = " + quote(s.Timeout))
	}
	retry(w, s.Retry)
	if err := expects(w, s); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if err := captures(w, s); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}

	w.indent--
	w.line("}")
	return nil
}

// action writes the step's action block, which is what gives the step its type:
// an HTTP verb for an `api` step, `run` for an `exec` one. There is no type key
// in the DSL, so `type:` becomes this line and disappears.
func action(w *writer, s models.Step) error {
	switch s.Type {
	case apiStep:
		return request(w, s.Request)
	case execStep:
		return run(w, s.Exec)
	default:
		return fmt.Errorf("step type %q has no DSL action block", s.Type)
	}
}

// request writes `post "${base}/items" { ... }`.
//
// An empty `method:` becomes `get`, because that is what net/http does with
// one: http.NewRequestWithContext("") sends a GET, so a scenario that left the
// method off was making a GET and the migrated file has to make the same one.
func request(w *writer, r models.Request) error {
	verb := strings.ToLower(strings.TrimSpace(r.Method))
	if verb == "" {
		verb = "get"
	}
	if !token.IsMethod(verb) {
		return fmt.Errorf("method %q is not one the DSL knows", r.Method)
	}
	url, err := templated(r.URL)
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}

	fields := make([]string, 0, len(r.Headers)+1)
	for _, key := range sortedKeys(r.Headers) {
		value, err := templated(r.Headers[key])
		if err != nil {
			return fmt.Errorf("header %q: %w", key, err)
		}
		fields = append(fields, "header "+quote(key)+" = "+value)
	}
	if r.Body != "" {
		body, err := requestBody(r.Body)
		if err != nil {
			return fmt.Errorf("body: %w", err)
		}
		fields = append(fields, "body = "+body)
	}

	w.block(verb+" "+url, fields)
	return nil
}

// run writes `run "sh" { args = [...] }`.
func run(w *writer, e models.Exec) error {
	command, err := templated(e.Command)
	if err != nil {
		return fmt.Errorf("command: %w", err)
	}

	var fields []string
	if len(e.Args) > 0 {
		args := make([]string, 0, len(e.Args))
		for i, a := range e.Args {
			arg, err := templated(a)
			if err != nil {
				return fmt.Errorf("args[%d]: %w", i, err)
			}
			args = append(args, arg)
		}
		fields = append(fields, "args = ["+strings.Join(args, ", ")+"]")
	}
	if e.Cwd != "" {
		cwd, err := templated(e.Cwd)
		if err != nil {
			return fmt.Errorf("cwd: %w", err)
		}
		fields = append(fields, "cwd = "+cwd)
	}
	if e.Stdin != "" {
		stdin, err := templated(e.Stdin)
		if err != nil {
			return fmt.Errorf("stdin: %w", err)
		}
		fields = append(fields, "stdin = "+stdin)
	}
	if len(e.Env) > 0 {
		settings := make([]string, 0, len(e.Env))
		for _, key := range e.EnvKeys() {
			value, err := templated(e.Env[key])
			if err != nil {
				return fmt.Errorf("env %q: %w", key, err)
			}
			settings = append(settings, key+" = "+value)
		}
		fields = append(fields, "env { "+strings.Join(settings, ", ")+" }")
	}

	w.block("run "+command, fields)
	return nil
}

// requestBody is a request body as a DSL value.
//
// A body that parses as JSON becomes an object or array literal, which is what
// the design asks for and what removes the failure mode where a captured object
// reached a body by text concatenation. One that does not is a string literal,
// which is still legal and is what a form-encoded or plain-text payload needs.
//
// The placeholders are rewritten inside the parsed JSON's strings, so
// `'{"token": "{{tokn}}"}'` is valid JSON the whole way through and lands as
// `body = {"token": "${tokn}"}`. A body with a placeholder in a *value*
// position -- `'{"n": {{count}}}'` -- is not JSON at all and stays a string,
// which is exactly what the YAML runtime rendered it as.
func requestBody(body string) (string, error) {
	if v, ok := parseJSON(body); ok {
		return literal(v, templated)
	}
	return templated(body)
}

// retry writes `retry { times = 3, delay = "50ms" }`, and nothing at all when
// the step asked for neither. Only the fields the scenario wrote are emitted:
// `times` alone is `retry { times = 3 }`, because `delay = ""` is not a
// duration the DSL accepts.
func retry(w *writer, r models.Retry) {
	var fields []string
	if r.Times != 0 {
		fields = append(fields, "times = "+strconv.Itoa(r.Times))
	}
	if r.Delay != "" {
		fields = append(fields, "delay = "+quote(r.Delay))
	}
	if len(fields) == 0 {
		return
	}
	w.line("retry { " + strings.Join(fields, ", ") + " }")
}

// expects writes the step's assertions, in the order the YAML runtime made
// them.
//
// The status and the exit code are asserted unconditionally by their executors
// -- `statusOK := resp.status == step.Response.StatusCode` runs whether or not
// the scenario wrote a `status_code:` -- so a step with no `response:` block
// migrates to `expect status == 0`. That assertion always fails, which is what
// the YAML run did; migration reproduces behaviour and does not fix scenarios.
func expects(w *writer, s models.Step) error {
	switch s.Type {
	case apiStep:
		w.line("expect status == " + strconv.Itoa(s.Response.StatusCode))
		for i, check := range s.Response.Body {
			lines, err := bodyExpects(check)
			if err != nil {
				return fmt.Errorf("body check %d: %w", i+1, err)
			}
			for _, line := range lines {
				w.line(line)
			}
		}
	case execStep:
		w.line("expect exit_code == " + strconv.Itoa(s.Expect.ExitCode))
		for _, stream := range []struct {
			name   string
			checks []models.TextCheck
		}{{"stdout", s.Expect.Stdout}, {"stderr", s.Expect.Stderr}} {
			for i, check := range stream.checks {
				line, err := textExpect(stream.name, check)
				if err != nil {
					return fmt.Errorf("%s check %d: %w", stream.name, i+1, err)
				}
				w.line(line)
			}
		}
	}
	return nil
}

// captures writes one `capture` per entry, in sorted key order -- the order
// models.Step.CaptureKeys reads them in, which is what keeps two migrations of
// the same file identical.
//
// The root a capture reads from is the step type's: an api step's JSON comes
// out of `body` and its text out of `raw`, and an exec step's both come out of
// `stdout`. A bare `$` is refused on an exec step: `$` is the whole parsed
// document, and `stdout` is the unparsed text, so there is no expression that
// means the same thing.
func captures(w *writer, s models.Step) error {
	jsonRoot, textRoot := "body", "raw"
	if s.Type == execStep {
		jsonRoot, textRoot = "stdout", "stdout"
	}

	for _, name := range s.CaptureKeys() {
		c := s.Capture[name]
		var value string
		switch {
		case c.Regex != "":
			lit, err := regexLiteral(c.Regex)
			if err != nil {
				return fmt.Errorf("capture %q: %w", name, err)
			}
			value = "match(" + textRoot + ", " + lit + ")"
		case s.Type == execStep && strings.TrimSpace(c.JSON) == "$":
			return fmt.Errorf("capture %q reads $, the whole parsed document, which a terminal step has no "+
				"expression for -- `stdout` is the unparsed text; name a path inside it", name)
		default:
			expr, err := pathExpr(jsonRoot, c.JSON)
			if err != nil {
				return fmt.Errorf("capture %q: %w", name, err)
			}
			value = expr
		}
		w.line("capture " + name + " = " + value)
	}
	return nil
}

// sortedKeys is the keys of m, sorted, so a mapping that has lost its order in
// the decoder is emitted the same way every time.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
