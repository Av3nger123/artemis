# Checking a command, not an API

A `terminal` step runs a command and asserts on what it did. It is the escape
hatch: anything artemis has no step type for -- a CLI, a migration script, a
release gate -- is a `terminal` step. The action is `run`, and `run` is what
makes the step a terminal one:

```art
scenario "release checks" {
  var tag = env("RELEASE_TAG")

  step "the tag exists" {
    run "git" { args = ["rev-parse", "--verify", "${tag}^{commit}"] }
    expect exit_code == 0
    expect stderr matches /^\s*$/
    capture sha = match(stdout, /^([0-9a-f]{40})/)
    timeout = "5s"
  }

  step "the changelog mentions it" {
    run "sh" { args = ["-c", "grep -c '${tag}' CHANGELOG.md"] }
    expect stdout matches /^[1-9]/
  }

  step "it builds" {
    run "go" {
      args = ["build", "./..."]
      env { CGO_ENABLED = "0" }
    }
    expect exit_code == 0
    expect stderr matches /^\s*$/
  }
}
```

**There is no shell.** The command is executed directly with its arguments: no
word splitting, no globbing, no `~` expansion. `run "ls *.go"` looks for a
binary with a space in its name and does not find one. A scenario that wants a
pipeline or a glob names a shell itself, as the second step does. That is longer
to write and impossible to misread.

A command that ran and exited with the wrong code is a **failed assertion**; one
that could not be run at all -- not on the `PATH`, a `cwd` that does not exist
-- is an **errored step**, because there was no exit code to compare. A wrong
exit code does not stop the `stderr` check from being made: stderr is exactly
what a failed command is diagnosed from.

[SPEC.md's *`terminal` steps*](../SPEC.md#terminal-steps) specifies every field of
the `run` block -- `args`, `cwd`, `stdin`, `env` -- and the 1 MiB per attempt
each stream is kept up to.
