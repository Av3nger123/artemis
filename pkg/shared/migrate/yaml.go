package migrate

import (
	"fmt"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// This file is the YAML scenario model: the shape of a file `artemis migrate`
// reads. It used to be pkg/shared/models, back when the same struct was both
// the file format and the runtime step; ART-40 took YAML off the run path, so
// the format now lives with its one remaining reader and models keeps only what
// pkg/dsl/lower builds.
//
// Nothing here decides anything about how artemis runs. It decodes a file and
// says what is wrong with it, and Source translates the result into `.art`.
// Keeping the `yaml:` tags, the hand-written UnmarshalYAML methods and their
// wording exactly as they were is the point: a file that loaded yesterday has
// to migrate today, and a file that was rejected has to be rejected with the
// same message.

type Variable struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// BodyCheck is one assertion against a response body.
//
// Value keeps whatever type the YAML scalar had -- `value: 200` is an int,
// `value: "200"` a string, `value: true` a bool. Operator names the comparison
// and defaults to "equals"; Type, when set, names the JSON type the value at
// Path must have.
type BodyCheck struct {
	Path     string `yaml:"path"`
	Operator string `yaml:"operator,omitempty"`
	Value    any    `yaml:"value"`
	Type     string `yaml:"type,omitempty"`

	// Line is the 1-based line of the scenario file this check was written
	// on -- the `path:` line, which is the one a reader goes and edits. It is
	// stamped on after decoding by annotateLines and is zero for a check that
	// did not come from a file. `yaml:"-"` so a scenario cannot set it and the
	// YAML generator does not emit it.
	Line int `yaml:"-"`
}

// Param is one name/value pair in the order it was written.
//
// It is what a Postman collection has and a YAML mapping does not: an ordered
// list, in which a name may appear twice. `Accept: application/json` followed
// by `Accept: text/csv` is two headers on the wire, and the DSL writes both --
// so the importer cannot go through a map.
type Param struct {
	Name  string
	Value string
}

type Request struct {
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"`
	Headers map[string]string `yaml:"headers"`
	Body    string            `yaml:"body"`

	// HeaderList is the headers in source order, and is what request() emits
	// when it is set -- Headers is used only when it is not. Query is the
	// request's query parameters as their own `query` fields, which a YAML
	// scenario wrote into the URL instead.
	//
	// Both are `yaml:"-"`: no scenario file can set either, and ParseYAMLFile's
	// strict decode still rejects a `header_list:` key. They are here because
	// Config is the shape Source translates, and `artemis generate` (ART-41)
	// builds one from a Postman collection rather than from a file.
	HeaderList []Param `yaml:"-"`
	Query      []Param `yaml:"-"`
}

type Response struct {
	StatusCode int         `yaml:"status_code"`
	Body       []BodyCheck `yaml:"body,omitempty"`

	// StatusOp is the comparison the status assertion is written with. Empty
	// means "==", which is the only thing a YAML scenario can say: `status_code:
	// 200` is an equality and nothing else. `artemis generate` sets "<" for a
	// Postman request with no saved example response, where the honest
	// assertion is `expect status < 400` rather than a guessed 200.
	//
	// `yaml:"-"`; see Request.HeaderList.
	StatusOp string `yaml:"-"`

	// StatusCodeLine is the line the `status_code:` key sits on, which is what
	// a status mismatch should point at -- not the `response:` header above it.
	// Zero when the step did not write one; see BodyCheck.Line.
	StatusCodeLine int `yaml:"-"`
}

// Exec is what an `exec` step runs: the `exec:` block, the counterpart of
// `request:` on an `api` step.
type Exec struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Cwd     string            `yaml:"cwd,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Stdin   string            `yaml:"stdin,omitempty"`
}

// EnvKeys is the names in Env, sorted, so anything derived from the map comes
// out the same on every run.
func (e Exec) EnvKeys() []string {
	return sortedKeys(e.Env)
}

// Expect is what an `exec` step expects of the run: the `expect:` block, the
// counterpart of `response:` on an `api` step.
//
// ExitCode is asserted on always, and its zero value is the assertion a
// scenario almost always wants -- a step that says nothing expects the command
// to succeed. There is no way to say "any exit code", which is why Source
// always emits an `expect exit_code == N`.
type Expect struct {
	ExitCode int         `yaml:"exit_code,omitempty"`
	Stdout   []TextCheck `yaml:"stdout,omitempty"`
	Stderr   []TextCheck `yaml:"stderr,omitempty"`

	// ExitCodeLine is the line the `exit_code:` key sits on. Zero when the step
	// did not write one. See BodyCheck.Line.
	ExitCodeLine int `yaml:"-"`
}

// TextCheck is one assertion against a stream of plain text.
//
// It is the counterpart of BodyCheck for output that is not addressable by a
// JSON path. There is no Path: the whole stream is the subject, and which
// stream is said by where the check was written.
//
// Operator defaults to "contains", not "equals": nearly every command ends its
// output with a newline, so an exact match is the check that is right in theory
// and wrong in practice. Value is always a string -- there is nothing to infer
// a type from, and a YAML scalar like `value: 200` is matched as the text
// "200".
type TextCheck struct {
	Operator string `yaml:"operator,omitempty"`
	Value    string `yaml:"value,omitempty"`

	// Line is the line of the scenario file this check was written on. See
	// BodyCheck.Line.
	Line int `yaml:"-"`
}

type Step struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Request  Request  `yaml:"request"`
	Response Response `yaml:"response"`
	// Exec and Expect are the `exec` step type's halves: what to run, and what
	// to expect of the run. They sit beside Request and Response rather than
	// replacing them because a Step is one struct for every type; which pair a
	// step reads is decided by Type.
	Exec   Exec   `yaml:"exec,omitempty"`
	Expect Expect `yaml:"expect,omitempty"`
	// Capture is what the step pulls out of whatever it produced, keyed by
	// the name the steps after it reference the value by.
	Capture map[string]Capture `yaml:"capture,omitempty"`
	Retry   Retry              `yaml:"retry,omitempty"`
	// Timeout is how long one attempt of this step may take, as a Go duration
	// string ("5s", "1m30s").
	Timeout string `yaml:"timeout,omitempty"`

	// Line is the line the step's first key sits on. See BodyCheck.Line.
	Line int `yaml:"-"`
}

// Retry is how many times a step may be attempted and how long to wait between
// attempts.
//
// Times is the total number of attempts, not the number of retries after the
// first. Delay is a Go duration string.
type Retry struct {
	Times int    `yaml:"times,omitempty"`
	Delay string `yaml:"delay,omitempty"`
}

// UnmarshalYAML accepts both `retry: {times: 3, delay: "1s"}` and the older
// scalar `retry: 5`, which means `times: 5`.
//
// The decoder's KnownFields setting does not reach a node decoded by hand, so
// an unknown key in the mapping is rejected here: otherwise `retry:` would be
// the one corner of the file where a typo still vanished.
func (r *Retry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var times int
		if err := node.Decode(&times); err != nil {
			return retryShapeError(node, err)
		}
		r.Times, r.Delay = times, ""
		return nil

	case yaml.MappingNode:
		// Content alternates key, value, key, value.
		for i := 0; i+1 < len(node.Content); i += 2 {
			switch key := node.Content[i].Value; key {
			case "times", "delay":
			default:
				return fmt.Errorf("line %d: field %s not found in retry (known fields: times, delay)", node.Content[i].Line, key)
			}
		}
		// The alias avoids recursing back into this method.
		type retryShape Retry
		var full retryShape
		if err := node.Decode(&full); err != nil {
			return retryShapeError(node, err)
		}
		*r = Retry(full)
		return nil

	default:
		return retryShapeError(node, nil)
	}
}

// retryShapeError says what retry may be, keeping the decoder's own complaint
// when there is one.
func retryShapeError(node *yaml.Node, err error) error {
	const want = "retry must be a number of attempts or a {times, delay} mapping"
	if err != nil {
		return fmt.Errorf("line %d: %s: %w", node.Line, want, err)
	}
	return fmt.Errorf("line %d: %s", node.Line, want)
}

// Attempts is how many times the step may be tried: always at least one.
func (r Retry) Attempts() int {
	if r.Times < 1 {
		return 1
	}
	return r.Times
}

// Wait is how long to sleep between two attempts. An empty Delay is no wait; a
// delay that will not parse, or one that is negative, is an error.
//
// Nothing in migration sleeps. It is kept because it is the one place left
// that knows what a YAML `delay:` may say, and yamlretry_test.go pins it: a
// delay the old runner accepted has to be one the emitted `.art` still
// accepts.
func (r Retry) Wait() (time.Duration, error) {
	if r.Delay == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(r.Delay)
	if err != nil {
		return 0, fmt.Errorf("retry delay %q is not a duration (want something like \"500ms\" or \"2s\")", r.Delay)
	}
	if d < 0 {
		return 0, fmt.Errorf("retry delay %q is negative", r.Delay)
	}
	return d, nil
}

// sortedKeys is the keys of m, sorted. A YAML mapping has no order once it is
// decoded, so sorting is what keeps anything derived from one -- the headers a
// migrated request writes, the order captures are emitted in -- the same on
// every run.
func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type Config struct {
	Name      string     `yaml:"name"`
	Type      string     `yaml:"type"`
	Variables []Variable `yaml:"variables"`
	Steps     []Step     `yaml:"steps"`
}
