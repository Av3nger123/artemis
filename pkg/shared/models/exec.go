package models

// This file is the `exec` step type's half of the step model (ART-17): what the
// step runs, and what it expects of the run. It is deliberately separate from
// collection.go, which holds the parts every step shares -- name, type, capture,
// retry, timeout -- and the `api` type's request and response.

// Exec is what an `exec` step runs. It is the `exec:` block of the step, the
// counterpart of `request:` on an `api` step.
//
// Command is executed directly: there is no shell, no word splitting and no
// globbing. `command: "ls *.go"` looks for a binary with a space in its name and
// does not find one. A scenario that wants a pipeline or a glob asks for a shell
// by name -- `command: "sh"`, `args: ["-c", "ls *.go | wc -l"]` -- which is
// longer to write and impossible to misread.
//
// Env is layered onto the environment artemis itself was given, not a
// replacement for it: a replaced environment has no PATH, so the common case
// would need boilerplate to work at all. There is no spelling for unsetting a
// variable.
//
// Cwd is relative to the directory artemis was run from, not to the scenario
// file. Every one of these fields is rendered against the scenario's variables
// before anything runs.
type Exec struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Cwd     string            `yaml:"cwd,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Stdin   string            `yaml:"stdin,omitempty"`
}

// EnvKeys is the names in Env, sorted, so the environment handed to a child
// process is built in the same order on every run.
func (e Exec) EnvKeys() []string {
	return sortedKeys(e.Env)
}

// Expect is what an `exec` step expects of the run: the `expect:` block, the
// counterpart of `response:` on an `api` step.
//
// ExitCode is asserted on always, and its zero value is the assertion a
// scenario almost always wants -- a step that says nothing expects the command
// to succeed. There is no way to say "any exit code".
//
// Stdout and Stderr are text checks against the two streams, in the order they
// are written, which is the order they are asserted and printed in.
type Expect struct {
	ExitCode int         `yaml:"exit_code,omitempty"`
	Stdout   []TextCheck `yaml:"stdout,omitempty"`
	Stderr   []TextCheck `yaml:"stderr,omitempty"`

	// ExitCodeLine is the line the `exit_code:` key sits on. Zero when the step
	// did not write one -- the assertion is still made, and it points at the
	// step instead. See BodyCheck.Line.
	ExitCodeLine int `yaml:"-"`
}

// TextCheck is one assertion against a stream of plain text.
//
// It is the counterpart of BodyCheck for output that is not addressable by a
// JSON path. There is no Path: the whole stream is the subject, and which stream
// is said by where the check was written.
//
// Operator defaults to "contains", not "equals": nearly every command ends its
// output with a newline, so an exact match is the check that is right in theory
// and wrong in practice. Value is always a string -- there is nothing to infer a
// type from, and a YAML scalar like `value: 200` is matched as the text "200".
type TextCheck struct {
	Operator string `yaml:"operator,omitempty"`
	Value    string `yaml:"value,omitempty"`

	// Line is the line of the scenario file this check was written on. See
	// BodyCheck.Line.
	Line int `yaml:"-"`
}
