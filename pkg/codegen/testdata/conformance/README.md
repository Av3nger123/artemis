# The conformance corpus

These scenarios are run **three times**: by `artemis run`, as the pytest
`artemis build --lang=python` generates from them, and as the vitest
`artemis build --lang=js` generates from them. Every run hits the same fixture
server -- `pkg/steps/browserstep/fixture`, on loopback -- and
`pkg/codegen/parity_test.go` and `pkg/codegen/jsparity_test.go` each assert that
their export agrees with the interpreter, scenario by scenario, and that both
match the outcome declared here. A backend that drifts from the interpreter fails
CI instead of being found by a user.

One corpus, not one per target. A scenario the Python export handles and the
JavaScript export does not is reported rather than never asked -- which is what
makes this directory the thing it is.

## Declaring an outcome

Every scenario needs a directive on the line above it:

```hcl
# conformance: pass
scenario "the query reaches the page" { ... }
```

`pass` or `fail`, and nothing else. A scenario with no directive fails
`TestEveryConformanceScenarioDeclaresAnOutcome` by name. The declarations are
what makes a *shared* mistake visible: if a lowering rule changes, both backends
move together and only the declared outcome notices.

A `fail` is as important as a `pass`. Without one the whole comparison could be
green while detecting no failure at all.

## Reaching the server

`env("ARTEMIS_BASE_URL")` is the fixture's address. Nothing here hard-codes a
port, and nothing here reaches off the machine.

Not `BASE_URL`, which would be the obvious name: Vite overwrites
`process.env.BASE_URL` with its own base path before a test module loads, so the
generated vitest would read `"/"` where the interpreter and the generated pytest
read the server's address. That is a divergence the corpus would have reported as
a backend bug, and it is neither backend's. The prefix is what keeps the name
ours; the generated file's header says so too.

## What a scenario may not do

Each generated file's own header lists the places that generated test and
`artemis run` differ **by design**. A scenario that depends on one of them would
fail this test for a reason neither backend is wrong about, so:

- do not compare a number against a string (`status == "200"`);
- give every step an explicit `timeout`, because a step without one has no
  timeout in Python and artemis's own default under the interpreter;
- do not assert on output long enough for artemis to truncate it.

And one more, from the same header: pytest and vitest both stop at the first
failed assertion while the interpreter evaluates every `expect` and runs the
steps after a failed one. The comparison is per *scenario* for that reason -- so
do not write a scenario whose meaning depends on what runs after a failure.

## The browser half

`browser/` holds the browser flow. It is run behind the `browser` build tag
(`make test-browser`), because the Go side needs a real Chromium and each
export's side needs Playwright's own browsers. Everything short of running it -- that it
compiles, that it declares its outcomes, that the corpus has a browser flow at
all -- is checked by the default suite.
