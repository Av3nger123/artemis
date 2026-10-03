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

A fault can get its fixture before the stage that reports it exists. The design
calls for the checker's goldens to be written *before* the checker, because that
is what stops diagnostics being under-built, and an empty golden with a note
asserts nothing. So such a fixture declares what it waits for, in its header:

```
# todo(ART-46): not-in-scope
```

and its golden is **authored**: the text that stage is expected to produce.
`-update` never rewrites it. The driver asserts the two things checkable before
the stage lands — the file draws *zero* diagnostics, so nothing reports a false
error on code whose only fault is one the stage owns, and the golden's
`=== codes` footer names exactly the codes the `# todo` lines do.

When the stage lands the test fails, naming the file and the lines to delete.
That stage then deletes them, runs `-update`, and reads the diff against the
text that was written for it — matching it, or changing it deliberately.

A `# todo` line's code is a plain string the driver does not check against
`diag`'s registry, because a code with no emitter would make `codes.golden`
claim something false. Codes actually *produced* are checked.

ART-32 used this for six fixtures and ART-33 answered them. Every message, hint
and suggestion the checker produces is the text ART-32 authored, with two
deliberate changes: `unknown_ident.art`'s second diagnostic is `unknown-field`
rather than `unknown-identifier` (`bdy` is a near miss of `body`, a root the api
step binds — see `diag/codes.go` on the split), and `bad_duration.art` was
rewritten from `config http` and `retry { attempts, wait }`, which the grammar
does not have, to a step-level `timeout` and `retry { times, delay }`. There are
no `# todo` fixtures outstanding today.

## The corpus

| Fixture | Pins |
| --- | --- |
| `action_not_first.art` | `action-not-first` — a statement above the action block |
| `bad_arity.art` | `bad-arity` ×3 — `attr("#link")`, `click "x" = v`, `fill "#e"` with no value |
| `bad_duration.art` | `invalid-duration` ×2 — `timeout = "5 secs"`, `delay = "soon"` |
| `bad_object.art` | `unexpected-token` — a missing `:` in an object literal, and what it derails |
| `bad_regex.art` | `invalid-regex` — `/order-(\d+/`, carrying Go's own compile error |
| `bad_value.art` | `bad-value` ×5 — a bool, a call argument, a missing key, an int, an array |
| `bad_wait.art` | `invalid-duration`, `bad-value` — `wait "soon"` and `wait 5`, a browser action reaching the one duration check |
| `browser_fn_in_api.art` | `not-in-scope` ×2 — `page.url` and `text()` in an api step |
| `browser_fn_in_var.art` | `not-in-scope` ×2 — `text()` and `page.url` in a `var`'s value, the position that is not a step |
| `browser_in_terminal.art` | `not-in-scope` ×2 — `page.title` and `value()` in a terminal step |
| `capture_same_step.art` | `unknown-identifier` — a capture read in the step that writes it |
| `chained_compare.art` | `non-associative-operator` — `a == 1 == true` |
| `many_errors.art` | four unrelated faults, all reported; also asserted by count in `TestAllErrorsReported` |
| `missing_action.art` | `missing-action` — a step with no action block, and no scope cascade under it |
| `out_of_scope_root.art` | `not-in-scope` — `status` in a `browser` step, with the roots that are |
| `page_member.art` | `unknown-field` — `page.titl`, the one root whose members are closed |
| `reserved_ai.art` | `reserved-word` — `ai`, and the agentic-assertion note in its hint |
| `reserved_word.art` | `reserved-word` ×2 — `var let`, `capture import` |
| `two_actions.art` | `duplicate-action` — `get` then `run` in one step |
| `unclosed_interp.art` | `unclosed-interpolation` — a `${` still open at end of file |
| `unclosed_interp_cascade.art` | the same typo mid-line: eleven diagnostics from one fault |
| `unknown_block_field.art` | `unknown-field` ×3 — a near miss, and two names near nothing |
| `unknown_browser_act.art` | `unexpected-token` ×2 — `clik` with the did-you-mean, `jump` with the eight listed |
| `unknown_config.art` | `unknown-config` ×2 — `config browsr` and `config http` |
| `unknown_field.art` | `unknown-field` — `expect statu == 200`, with the did-you-mean |
| `unknown_function.art` | `unknown-identifier` ×2 — an unknown callee, and one named without its call |
| `unknown_ident.art` | `unknown-identifier` then `unknown-field` — in a `${}` and in an expression |
| `unknown_type.art` | `unknown-type` ×2 — `is numbr` and `is int` |

The four not-in-scope fixtures are one matrix, not four cases:
`out_of_scope_root.art` is an api root in a browser step,
`browser_in_terminal.art` and `browser_fn_in_api.art` are the browser
vocabulary in the other two step types, and `browser_fn_in_var.art` is it in a
`var`'s value. The hint is what each one is really pinning -- the roots that
*are* bound where the author is standing -- and there is one per position
because each position's list is a different list.

`unclosed_interp_cascade.art` is pinned, not suppressed. Eleven diagnostics from
one typo is honest output from a recovering parser; narrowing it is a change to
`pkg/dsl/parser`, and this golden is the evidence for whoever takes that on. The
checker stays out of it: an interpolated string that did not lex has its
contents skipped, because the "expressions" inside one are whatever the parser
could salvage from the rest of the file.
