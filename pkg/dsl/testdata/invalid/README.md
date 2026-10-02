# The invalid-file diagnostic corpus

Every file here is an invalid `.art` file, and every `<name>.golden` pins the
**whole** of what the front end says about it. Diagnostics are the feature this
rewrite exists for, so they are tested like a feature: changing a message, a
span, a severity, a hint, a suggestion or a code fails `make test` and someone
has to read the diff and agree with it.

The driver is `../../corpus_test.go`. It lives there and not here because Go
tooling ignores a `testdata` directory.

## A golden's two sections

```
=== terminal
two_actions.art:8:5: a step has exactly one action block
    8 |     run "echo deploy"
      |     ^^^
   hint: the first one is at line 7; a second action means a second step

=== codes
duplicate-action	error	8:5-8:8
```

`=== terminal` is `diag.TerminalString` verbatim — what a person receives. The
corpus renders; it never formats, so the thing under test is the thing shipped.

`=== codes` is the machine contract, one tab-separated line per diagnostic:
code, severity, `line:col-endLine:endCol`, and `suggest: "…"` when the
diagnostic carries mechanical fixes. None of that appears above, so a code
rename or a span that grew by one column fails here even where the caret run
happens not to move.

## Adding a fixture

1. Write `<name>.art`. Make it invalid in exactly one interesting way, with
   well-formed code either side of the fault, and say in a leading comment what
   the fault is and why it belongs to the stage that reports it.
2. `go test ./pkg/dsl -update` (or `make golden`).
3. **Read the golden.** A golden regenerated without being read asserts that the
   code does what the code does.

No Go changes: the driver globs `*.art`.

## Fixtures waiting on a stage

Six faults the corpus must cover belong to `pkg/dsl/check`, which lands in
ART-33. The design calls for these goldens to be written *before* the checker is
finished, because that is what stops diagnostics being under-built, and an empty
golden with a note asserts nothing. So such a fixture declares what it waits
for, in its header:

```
# todo(ART-33): unknown-field
```

and its golden is **authored**: the text ART-33 is expected to produce. `-update`
never rewrites it. The driver asserts the two things checkable today — the file
parses with *zero* diagnostics, so nothing reports a false syntax error on code
whose only fault is a name, and the golden's `=== codes` footer names exactly
the codes the `# todo` lines do.

When the stage lands the test fails, naming the file and the lines to delete.
ART-33 then deletes them, runs `-update`, and reads the diff against the text
that was written for it — matching it, or changing it deliberately.

Four of the codes these fixtures name are not in `diag`'s registry yet:
`reserved-word`, `invalid-regex`, `invalid-duration`, `unknown-identifier`.
ART-33 registers them. A `# todo` line's code is therefore a plain string the
driver does not check against the registry; codes actually *produced* are
checked.

## The corpus

| Fixture | Pins |
| --- | --- |
| `action_not_first.art` | `action-not-first` — a statement above the action block |
| `bad_duration.art` | **todo(ART-33)** `invalid-duration` ×2 — `timeout = "5 secs"`, `wait = "soon"` |
| `bad_object.art` | `unexpected-token` — a missing `:` in an object literal, and what it derails |
| `bad_regex.art` | **todo(ART-33)** `invalid-regex` — `/order-(\d+/` |
| `chained_compare.art` | `non-associative-operator` — `a == 1 == true` |
| `many_errors.art` | four unrelated faults, all reported; also asserted by count in `TestAllErrorsReported` |
| `missing_action.art` | `missing-action` — a step with no action block |
| `out_of_scope_root.art` | **todo(ART-33)** `not-in-scope` — `status` in a `browser` step |
| `reserved_word.art` | **todo(ART-33)** `reserved-word` ×2 — `var let`, `capture import` |
| `two_actions.art` | `duplicate-action` — `get` then `run` in one step |
| `unclosed_interp.art` | `unclosed-interpolation` — a `${` still open at end of file |
| `unclosed_interp_cascade.art` | the same typo mid-line: eleven diagnostics from one fault |
| `unknown_field.art` | **todo(ART-33)** `unknown-field` — `expect statu == 200`, with the did-you-mean |
| `unknown_ident.art` | **todo(ART-33)** `unknown-identifier` ×2 — in an expression and inside a `${}` |

`unclosed_interp_cascade.art` is pinned, not suppressed. Eleven diagnostics from
one typo is honest output from a recovering parser; narrowing it is a change to
`pkg/dsl/parser`, and this golden is the evidence for whoever takes that on.
