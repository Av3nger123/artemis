# The conformance corpus

These scenarios are run **twice**: by `artemis run`, and as the pytest
`artemis build --lang=python` generates from them. Both runs hit the same
fixture server -- `pkg/steps/browserstep/fixture`, on loopback -- and
`pkg/codegen/parity_test.go` asserts the two agree, scenario by scenario, and
that both match the outcome declared here. A backend that drifts from the
interpreter fails CI instead of being found by a user.

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

`env("BASE_URL")` is the fixture's address. Nothing here hard-codes a port, and
nothing here reaches off the machine.

## What a scenario may not do

The generated file's own header lists three places a generated test and
`artemis run` differ **by design**. A scenario that depends on one of them would
fail this test for a reason neither backend is wrong about, so:

- do not compare a number against a string (`status == "200"`);
- give every step an explicit `timeout`, because a step without one has no
  timeout in Python and artemis's own default under the interpreter;
- do not assert on output long enough for artemis to truncate it.

And one more, from the same header: pytest stops at the first failed assert
while the interpreter evaluates every `expect` and runs the steps after a failed
one. The comparison is per *scenario* for that reason -- so do not write a
scenario whose meaning depends on what runs after a failure.

## The browser half

`browser/` holds the browser flow. It is run behind the `browser` build tag
(`make test-browser`), because the Go side needs a real Chromium and the Python
side needs Playwright's own browsers. Everything short of running it -- that it
compiles, that it declares its outcomes, that the corpus has a browser flow at
all -- is checked by the default suite.
