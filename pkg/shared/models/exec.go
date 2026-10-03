package models

// This file is the `terminal` step type's action: what the step runs. What it
// expects of the run is an `expect` expression held by pkg/dsl/lower, not a
// field here (ART-40).

// Exec is what a `terminal` step runs. It is the step's `run` action block,
// the counterpart of Request on an `api` step, with every value already
// resolved by pkg/dsl/lower.
//
// Command is executed directly: there is no shell, no word splitting and no
// globbing. `run "ls *.go"` looks for a binary with a space in its name and
// does not find one. A scenario that wants a pipeline or a glob asks for a
// shell by name -- `run "sh" { args = ["-c", "ls *.go | wc -l"] }` -- which is
// longer to write and impossible to misread.
//
// Env is layered onto the environment artemis itself was given, not a
// replacement for it: a replaced environment has no PATH, so the common case
// would need boilerplate to work at all. There is no spelling for unsetting a
// variable.
//
// Cwd is relative to the directory artemis was run from, not to the scenario
// file.
type Exec struct {
	Command string
	Args    []string
	Cwd     string
	Env     map[string]string
	Stdin   string
}

// EnvKeys is the names in Env, sorted, so the environment handed to a child
// process is built in the same order on every run.
func (e Exec) EnvKeys() []string {
	return sortedKeys(e.Env)
}
