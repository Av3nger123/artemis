# Artemis

Artemis is a command-line test runner for things you check from the outside: a
REST API, a CLI, a release gate. A scenario is a file of steps and what to
expect of each one, written in the Artemis DSL -- `.art` -- and a run exits
non-zero the moment anything does not hold, so a CI job goes red on a broken
API.

The language has one job: say what to do and what must be true, in a file that a
person can read at a glance and an agent can write without guessing. It is
parsed, not templated -- a mistyped field or an undeclared variable is a compile
error with a caret under it, before a single request goes out.

[SPEC.md](SPEC.md) is the normative reference for the language. This file
documents the commands.

## A scenario

```art
scenario "checkout" {
  var url = env("API_URL")

  step "get a token" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"username": "alice", "password": env("API_PASSWORD")}
    }
    expect status == 200
    capture token = body.data.access_token
  }

  step "list the orders" {
    get "${url}/orders" {
      header "Authorization" = "Bearer ${token}"
      query "limit" = 10
    }
    timeout = "5s"
    retry { times = 3, delay = "2s" }

    expect status == 200
    expect body.data.count > 0
    expect body.data.email matches /.+@.+/
    expect body.data.roles contains "admin"
    expect body.data.token exists
  }
}
```

Run it:

```sh
artemis run checkout.art
```

Five things in that file are the whole language:

| | |
| --- | --- |
| `var` | A value the steps can name, evaluated once before the first step runs. `env("NAME")` reads the environment, or the `.env` file |
| the action block | `post "..." { ... }` -- one per step, and it is what gives the step its type. There is no `type:` key |
| `expect` | One assertion, written as an expression. One `expect` line is one assertion in the report, so the line you wrote and the failure you read about are one to one |
| `capture` | A value pulled out of what the step produced, available by name in every later step |
| `retry`, `timeout`, `within` | Three different clocks: retry the whole step, bound one attempt, or wait for one assertion to come true |

## Features

- Scenarios in a real language: lexed, parsed and name-checked before anything runs, with compiler-grade diagnostics -- the source line, a caret, and `did you mean "status"?`
- Two step types today: `api` for an HTTP call, `terminal` for a command. One action block per step, and the block is the type
- Assertions as expressions: `==`, `!=`, `>`, `>=`, `<`, `<=`, `contains`, `matches`, `exists`, `is <type>`, `not`, `and`, `or`
- Values captured from one step and used in the next, keeping their JSON type
- A run summary on the terminal, plus a block per failure naming the file and line, the scenario, the step, expected and actual -- output an agent can act on
- `--report json` and `--report junit`: the whole outcome of a run as one document, for a CI job or an agent to read
- `browser` steps on Playwright, with per-assertion waiting and a screenshot of any page that failed
- A canonical formatter (`artemis fmt`), a syntax tree as JSON in both directions (`artemis ast`), and the grammar itself as text or as machine-readable choice points (`artemis grammar`)
- A one-way export to pytest or vitest (`artemis build --lang=python|js`), for a team whose tests live in another language
- One-shot conversion in: a Postman collection with `artemis generate`, an old YAML scenario with `artemis migrate`
- A non-zero exit code whenever anything fails, and no third code to learn

## Installation

Make sure you have Go installed, then:

```bash
source ./install.sh
```

## Coming from Postman or YAML

Both conversions are one-shot: they read the old file, write `.art` source, and
never touch the original. Nothing translates on the fly, because a format that
is only ever read through a converter is a format nobody has to learn.

Both write through the same printer `artemis fmt` uses, so what comes out is
already canonically formatted and `artemis fmt` over it changes nothing.

### A Postman collection

```sh
artemis generate -f orders.postman_collection.json              # to stdout
artemis generate -f orders.postman_collection.json -o orders.art
```

Folders nest to any depth and every request becomes one step named by its folder
path -- `"Orders / Admin / list orders"`. Headers, `url.query` parameters and the
body's mode are read as the collection wrote them: a raw JSON body becomes an
object literal, a urlencoded body a string. A disabled header or parameter is
left out, because the author switched it off.

Auth at the collection and at the request both become a header: bearer, basic
with literal credentials, and apikey in a header or the query. A request with a
saved example response asserts that example's status; one without asserts
`expect status < 400`, which is the strongest thing the collection actually
says.

A Postman variable whose key is not a DSL name is renamed -- `base-url` becomes
`base_url` -- and every placeholder is rewritten to match. A `{{name}}` no
variable defines becomes `var name = env("NAME")`. Both are noted in the comment
above the scenario, so nothing is renamed silently:

```art
# Generated by artemis generate from kitchen_sink.postman_collection.json.
# The Postman variable "access-token" is written access_token here, because a DSL name cannot hold "-".
# The Postman variable "base-url" is written base_url here, because a DSL name cannot hold "-".
# Read from the environment, because the collection used them without defining them: access_token from ACCESS_TOKEN.
scenario "Orders API" {
  var base_url = "https://api.example.com"
  var page_size = "10"
  var access_token = env("ACCESS_TOKEN")

  step "Health / ping" {
    get "${base_url}/ping"
    expect status == 200
  }

  step "Orders / Admin / list orders" {
    get "${base_url}/orders" {
      header "Authorization" = "Bearer ${access_token}"
      header "Accept" = "application/json"
      query "limit" = "${page_size}"
      query "tag" = "new"
    }
    expect status < 400
  }

  step "Orders / create order" {
    post "${base_url}/orders" {
      header "Authorization" = "Bearer ${access_token}"
      header "Content-Type" = "application/json"
      body = {"note": "rush", "qty": 2, "sku": "A-1"}
    }
    expect status == 201
  }
}
```

A construct with no DSL spelling is an error naming the request, and nothing is
written: a formdata or file body, an auth type needing a signature, a dynamic
variable like `{{$guid}}`. Pre-request and test scripts are not read at all --
the DSL has no spelling for arbitrary JavaScript, and it is not going to grow
one.

With `-o`, a file that is already there is an error rather than an overwrite;
`--force` replaces it.

### An old YAML scenario

`artemis migrate` is the only thing in artemis that still reads YAML. Nothing
runs it.

```sh
artemis migrate -f checkout.yaml                 # to stdout
artemis migrate -f checkout.yaml -o checkout.art
```

This is the input:

```yaml
# The scenario this README documented before the DSL landed.
name: "API Collection"
type: functional
variables:
  - name: "url"
    value: "{{env.API_URL}}"
steps:
  - name: "Login"
    type: api
    request:
      url: "{{url}}/token"
      method: "POST"
      headers:
        Content-Type: "application/json"
      body: '{"username":"alice","password":"s3cret"}'
    response:
      status_code: 200
      body:
        - path: "$.data.message"
          value: "success"
    capture:
      token: "$.data.access_token"
  - name: "Orders"
    type: api
    request:
      url: "{{url}}/orders"
      method: "GET"
      headers:
        Authorization: "Bearer {{token}}"
    response:
      status_code: 200
      body:
        - path: "$.data.count"
          operator: gt
          value: 0
    retry:
      times: 3
      delay: "2s"
    timeout: "5s"
```

And this is what `artemis migrate -f checkout.yaml` writes:

```art
# The scenario this README documented before the DSL landed.
scenario "API Collection" {
  var url = env("API_URL")

  step "Login" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"password": "s3cret", "username": "alice"}
    }
    expect status == 200
    expect body.data.message == "success"
    capture token = body.data.access_token
  }

  step "Orders" {
    get "${url}/orders" { header "Authorization" = "Bearer ${token}" }
    timeout = "5s"
    retry { times = 3, delay = "2s" }
    expect status == 200
    expect body.data.count > 0
  }
}
```

The YAML file is checked as strictly as it ever was -- a strict decode, and
every step type validated -- so a scenario artemis would have refused to run is
refused here too and nothing is written. The comment above the file and the
comment above each step are carried over; comments anywhere else are dropped,
and how many is reported on stderr, because a sentence written above one body
check has no line in the migrated file to belong to.

A construct with no DSL spelling is an error naming the step it was in: a
wildcard, a recursive descent or a filter in a JSON path, `operator: empty` on a
stream, an unclosed `{{`. Migration will not guess at one. [SPEC.md's *Coming
from YAML*](SPEC.md#coming-from-yaml) lists the habits that do not carry over
and what to write instead.

## Commands

### `artemis run`

```sh
artemis run checkout.art     # one file
artemis run ./suite          # every scenario under a folder
```

`run` takes one path. A file is run on its own. A folder is walked recursively
for `*.art` files, which are run in path order -- the same order on every
machine -- as **one run**: one summary, one exit code. Nothing is shared between
files or between scenarios: each gets its own variables, so what one captures is
invisible to the next.

A path that holds no scenario file is an error, not an empty run that passes. A
file that does not compile is reported as an errored scenario, with its
diagnostics, and the files after it still run -- one typo does not hide the rest
of the suite.

A run prints a line per step as it finishes, the checks that did not pass under
the step that made them, the blocks to go and fix, and a summary. It exits 0
only if everything passed:

```
scenario: login (suite/01_login.art)
  ok    get a token                   4ms
scenario: items (suite/02_items.art)
  FAIL  list items                   <1ms
         body.data.count > 0, got 0
  FAIL  missing route                <1ms
         status == 200, got 404
suite/nested/03_broken.art:4:5: unknown field "timeot"
   4 |     timeot = "5s"
     |     ^^^^^^
   hint: did you mean "timeout"?
scenario: suite/nested/03_broken.art
  ERROR suite/nested/03_broken.art:4:5: unknown field "timeot"

3 failures:

1) suite/02_items.art:7
     scenario  items
     step      list items
     assert    expect body.data.count >
     expected  0
     actual    0

2) suite/02_items.art:12
     scenario  items
     step      missing route
     assert    expect status ==
     expected  200
     actual    404

3) suite/nested/03_broken.art
     error     suite/nested/03_broken.art:4:5: unknown field "timeot"

Scenarios     3  (1 passed, 1 failed, 1 errored)
Steps         3  (1 passed, 2 failed)
Assertions    4  (2 passed, 2 failed)
FAIL in 6ms
```

`artemis test -f checkout.art` is the old name for a one-file run. It still
works and still fails the process on a failing run, but it prints a deprecation
line: use `artemis run`.

### Reading a failure

Under the step list, a failing run prints one block per thing to go and fix.
Each block stands alone -- it names the file and the line, the scenario and the
step, so nothing above it has to be read -- because the reader is as often the
agent that wrote the scenario as a person scrolling a CI log.

| Field | What it is |
| --- | --- |
| the heading | `file:line` -- the line of the scenario to edit: the `expect` that failed, or the `capture` that could not be read. A step that could not run at all points at its own first line. A line artemis does not know is left off rather than printed as `:0`. |
| `scenario` | The scenario's name. Absent for a file that would not compile: there was no name to read. |
| `step` | The step's name. Absent for a file that would not compile, which never ran one. |
| `assert` | What was checked: the statement it came from, the subject's source text, and the comparison applied -- `expect body.data.count >`. |
| `expected` / `actual` | The two values as evaluated. A string is quoted and a number is not, so `"200"` and `200` do not read alike; when the two sides are different JSON types each is followed by its type. |
| `error` | In place of `expected`/`actual` when there was nothing to compare: a path that did not resolve, `>` against an object, a command that could not be started, a file that would not compile. |

A step that could not run, and a scenario whose file would not compile, each get
a block too:

```
1) refused.art:2
     scenario  down
     step      ping
     error     performing request: Get "http://127.0.0.1:1/orders": dial tcp 127.0.0.1:1: connect: connection refused
```

That distinction runs through everything artemis prints and everything it
reports: **failure** is "it ran and gave the wrong answer", **error** is "it
could not run at all". A flaky environment should not read as a broken API.
[SPEC.md](SPEC.md#the-three-kinds-of-failure) specifies the three classes --
compile error, step error, assertion failure -- and which of them is retried.

The blocks come between the step list and the tallies, so the verdict line is
still the last thing a run writes. The same list is in the JSON report, flat, as
`failures`.

### `artemis parse`

```sh
artemis parse -f checkout.art
```

`parse` lexes, parses and name-checks the file and runs nothing: no request is
sent, no command is run. It prints every problem it finds -- all of them, in
file order, with the source line echoed -- and exits non-zero if any is an
error:

```
suite/nested/03_broken.art:4:5: unknown field "timeot"
   4 |     timeot = "5s"
     |     ^^^^^^
   hint: did you mean "timeout"?
1 error in suite/nested/03_broken.art
```

A clean file says so, because a command whose only job is to tell you something
has to say something when the answer is yes:

```
suite/01_login.art: ok
```

#### `--json`

`artemis parse -f checkout.art --json` writes the same diagnostics to stdout as
one document instead, which is what an editor or a UI reads:

```json
{
  "diagnostics": [
    {
      "code": "unknown-field",
      "severity": "error",
      "span": {
        "file": "suite/nested/03_broken.art",
        "line": 4,
        "col": 5,
        "endLine": 4,
        "endCol": 11,
        "offset": 78
      },
      "message": "unknown field \"timeot\"",
      "hint": "did you mean \"timeout\"?",
      "suggestions": [
        {
          "replace": "timeout"
        }
      ]
    }
  ]
}
```

The `code` is stable and the `message` is not, so key behaviour off the code.
The span's `offset` and `endCol` locate the text to underline. A suggestion's
`replace` is a mechanical fix, so a client can offer one-click correction. A
clean file writes `{"diagnostics": []}`. The exit status is the same either way:
`--json` is a way to read the diagnostics, not a way to make a broken file pass.
`artemis grammar --json` lists every code that can appear here.

### `artemis fmt`

```sh
artemis fmt checkout.art       # the formatted file on stdout
artemis fmt -w checkout.art    # rewrite it in place
```

Canonical layout: two-space indentation, one space either side of an operator,
one item per line in a block that does not fit inline, LF endings, one trailing
newline.

Formatting changes layout and nothing else. No string is requoted, no number
reformatted, no expression reassociated, and no comment dropped. Running it
twice changes nothing the second time, and `-w` over a file that is already
formatted does not touch its mtime.

A file artemis reports an error for is not formatted at all: the diagnostics are
printed and the file is left exactly as it was. A formatter that rewrites a
broken file is a formatter that loses someone's work.

### `artemis ast`

```sh
artemis ast -f checkout.art          # the syntax tree as JSON
artemis ast --from-json < tree.json  # that tree back to .art source
```

The same tree, from the same front end that runs the suite, so a UI, a
transpiler and the runtime cannot drift apart. Every node carries its span --
`file`, `line`, `col`, `endLine`, `endCol`, `offset` -- and the document carries
a `schemaVersion`, because a UI ships and upgrades independently of the CLI:

```json
{
  "schemaVersion": 1,
  "file": "suite/01_login.art",
  "scenarios": [
    {
      "kind": "scenario",
      "name": {
        "text": "\"login\"",
        "value": "login"
      },
      "body": [
        {
          "kind": "var",
          "name": "url",
          "value": {
            "kind": "call",
            "callee": "env",
            "args": [
              {
                "kind": "literal",
                "literal": "string",
                "text": "\"API_URL\"",
                "value": "API_URL"
              }
            ]
          }
        }
      ]
    }
  ]
}
```

The checker's conclusions travel with the nodes they are about: each step
carries the type its action implies and the names resolvable inside it, and each
`expect` carries the simple/complex label that decides whether a form renders
three widgets or one expression field. Nothing re-derives them.

A file artemis reports an error for produces no JSON. `--from-json` is a
conversion and nothing else: it writes canonical source and exits zero whenever
the document decodes, and refuses a tree holding a node that did not parse.

### `artemis grammar`

```sh
artemis grammar          # the whole language as text
artemis grammar --json   # its enumerable choice points
```

The plain form is the language in one piece: the production rules, the lexical
tokens they treat as atoms, the precedence the productions only imply, the rules
the checker enforces after parsing, what each step type binds, and one worked
example that compiles. It is meant to be read once, or pasted whole into a
prompt.

The `--json` form is for a program: every finite option set in the language --
HTTP methods, browser actions with their arity, comparison operators, type
names, the fields of every block with the kind each takes, builtins, reserved
words with what they are held for, step types with the names they bind, and the
diagnostic codes `artemis parse --json` can emit:

```json
{
  "schemaVersion": 1,
  "choices": {
    "action": {
      "doc": "The action-block openers that are not an HTTP verb: run is a terminal step, browser is a browser step.",
      "values": [
        { "value": "run" },
        { "value": "browser" }
      ]
    }
  }
}
```

Every one of those is read out of the table the parser and the checker
themselves consult, so a word added to the language appears here without
anything shipping. A client building a form reads it instead of hardcoding a
list that drifts.

### `artemis build`

```sh
artemis build --lang=python checkout.art              # to stdout
artemis build --lang=python -o tests/ checkout.art    # tests/test_checkout.py
artemis build --lang=js -o tests/ checkout.art        # tests/checkout.test.js
```

Exports the scenarios as tests for another runner, for a team whose tests live in
another language. Two targets, with no artemis runtime in either beyond a handful
of small helpers at the top of the file:

| `--lang` | Output |
| --- | --- |
| `python` | pytest: `requests`, `subprocess`, `playwright.sync_api` |
| `js` | vitest: `fetch`, `child_process`, `@playwright/test` |

The lowering rules are the same for both. One scenario becomes one test with its
steps as ordered statements, a `var` and a `capture` become local variables, an
`expect` becomes the runner's own assertion, a `retry` becomes a helper around
the step, and a `within` on a browser assertion becomes Playwright's own timeout
-- `expect visible(".modal") within "5s"` is
`expect(page.locator(".modal")).to_be_visible(timeout=5000)` under pytest and
`await expect(page.locator(".modal")).toBeVisible({ timeout: 5000 })` under
vitest. A browser scenario gets one page for the whole test.

That both exports drive Playwright, as `artemis run` does, is the point rather
than a coincidence: a browser scenario behaves the same way three ways because
the three are one protocol, not three reimplementations. A shared conformance
corpus is run all three ways in CI, and a scenario they disagree about fails the
build.

The export is **one way**. Artemis never reads generated code back: the `.art`
file stays the source of truth, running the command again overwrites the output,
and the header of every generated file says both, along with the few places the
generated test and `artemis run` differ. One of those is worth knowing before you
hit it: Vite owns `process.env.BASE_URL` and `process.env.NODE_ENV`, so a
scenario that reads `env("BASE_URL")` reads Vite's base path under the generated
vitest. Name yours something else.

The generated JavaScript is ESM, which Vite transforms whatever your
`package.json` says, and it needs `npm install -D vitest` -- plus
`@playwright/test` for a scenario with a browser step. Nothing else ships with
it: no `package.json`, no `vitest.config.js`, no lockfile.

`--lang` is required. A file that does not compile is reported and nothing is
written. Without `-o` the module goes to stdout; with it, `-o` names a directory,
created if it is not there, and each path written is printed. There is no
`--force`: the output is derived, and regenerating it is the point.

A Go target is reserved and will not be built, because the `.art` file is already
the Go-side source of truth and `artemis run` is how Go runs it.

### Machine-readable reports

`--report` writes the outcome of a run in a format another program can read. Its
value is `format[=path]`:

```sh
artemis run ./suite --report json               # the document on stdout
artemis run ./suite --report json=results.json  # the document in a file
artemis run ./suite --report junit=junit.xml    # JUnit XML for a CI reporter

# both out of one run: the JSON on stdout, the XML in a file
artemis run ./suite --report json --report junit=junit.xml
```

The formats are `json` and `junit`. The flag is repeatable, so one run can
produce both; a format given twice, and two formats aimed at stdout, are errors.

With no path the document goes to **stdout**, and the console report moves to
**stderr**, so stdout holds exactly one JSON document and nothing else:

```sh
artemis run ./suite --report json | jq '.counts.assertions'
```

With a path, the document is written there -- truncating whatever was there
before -- and the console report stays on stdout. A document is written whether
the run passed or failed, and a report that cannot be written fails the command
even when the run itself passed: a missing artifact must not pass for a green
build. The exit code is unchanged by the flag; it still comes from the run.

#### The document

One document per run, mirroring the result tree: a run of scenarios, a scenario
of steps, a step of assertions. Everything that ran is in it, passing things
included, so a reader can tell "nothing failed" from "nothing ran".

```json
{
  "schema_version": 1,
  "started_at": "2026-03-04T05:06:07Z",
  "duration_ms": 1.627,
  "status": "error",
  "passed": false,
  "counts": {
    "scenarios":  { "total": 3, "passed": 1, "failed": 1, "errored": 1, "skipped": 0 },
    "steps":      { "total": 3, "passed": 1, "failed": 2, "errored": 0, "skipped": 0 },
    "assertions": { "total": 4, "passed": 2, "failed": 2, "errored": 0, "skipped": 0 }
  },
  "scenarios": [
    {
      "name": "items",
      "file": "suite/02_items.art",
      "status": "fail",
      "duration_ms": 0.624,
      "error": "",
      "steps": [
        {
          "name": "list items",
          "status": "fail",
          "duration_ms": 0.316,
          "attempts": 1,
          "line": 4,
          "error": "",
          "assertions": [
            {
              "kind": "expect",
              "path": "body.data.count",
              "operator": ">",
              "expected": 0,
              "actual": 0,
              "status": "fail",
              "error": "",
              "line": 7
            }
          ]
        }
      ]
    }
  ],
  "failures": [
    {
      "file": "suite/02_items.art",
      "line": 7,
      "scenario": "items",
      "step": "list items",
      "status": "fail",
      "kind": "expect",
      "path": "body.data.count",
      "operator": ">",
      "expected": 0,
      "actual": 0,
      "error": ""
    }
  ]
}
```

Every key is specified, one row each, in
[SPEC.md's *The JSON report*](SPEC.md#the-json-report) -- the one place it is
written down, so the two documents cannot come to disagree. In short: every key
is always present with its zero value rather than omitted, so a `jq` expression
never has to tell absent from empty; `expected` and `actual` are the exception
and are `null`, because either may legitimately be any JSON type; and every list
is a list, empty rather than `null`.

What is *not* in the document is anything artemis does not record: no request
bodies, no response bodies and no headers. The line a failure came from it does
record.

`pkg/cli/testdata/art/report_json.golden` is a whole document from a real run,
for reading, and `pkg/report/json.go` is where the shape is defined.

#### JUnit XML

`--report junit` writes the run as the XML every CI test reporter reads --
Jenkins, GitLab, CircleCI, the GitHub Actions reporters. All of them group what
they read by test suite, so:

| In artemis | In the XML |
| --- | --- |
| a run | `<testsuites name="artemis">` |
| a scenario | `<testsuite name="<scenario>" file="<path>">` |
| a step | `<testcase classname="<path>" name="<step>">` |
| a step that failed | `<failure>` on its case |
| a step that could not run | `<error>` on its case |
| a step that was skipped | `<skipped>` on its case |
| a step that passed | a case with no child element |
| every failing assertion of a step | one line of its `<failure>` text |

That mapping is the point: a step is the unit a person reruns and fixes, so a CI
UI lists failing steps under the scenario they came from with no configuration.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="artemis" tests="4" failures="2" errors="1" skipped="0" time="0.002" timestamp="2026-03-04T05:06:07Z">
  <testsuite name="login" file="suite/01_login.art" tests="1" failures="0" errors="0" skipped="0" time="0.001">
    <testcase name="get a token" classname="suite/01_login.art" time="0.001"></testcase>
  </testsuite>
  <testsuite name="items" file="suite/02_items.art" tests="2" failures="2" errors="0" skipped="0" time="0.001">
    <testcase name="list items" classname="suite/02_items.art" time="0.000">
      <failure message="body.data.count &gt; 0, got 0">body.data.count &gt; 0, got 0</failure>
    </testcase>
    <testcase name="missing route" classname="suite/02_items.art" time="0.000">
      <failure message="status == 200, got 404">status == 200, got 404</failure>
    </testcase>
  </testsuite>
  <testsuite name="suite/nested/03_broken.art" file="suite/nested/03_broken.art" tests="1" failures="0" errors="1" skipped="0" time="0.000">
    <testcase name="could not load" classname="suite/nested/03_broken.art" time="0.000">
      <error message="suite/nested/03_broken.art:4:5: unknown field &#34;timeot&#34;">suite/nested/03_broken.art:4:5: unknown field &#34;timeot&#34;</error>
    </testcase>
  </testsuite>
</testsuites>
```

Things worth knowing before you point a reporter at it:

- **`classname` is the scenario's file, not its name.** A reporter keys a test on
  `classname` plus `name` and displays them joined, so the path keeps two files
  that happen to name their scenario the same from merging into one test in your
  CI history -- and it points at the file to edit. The scenario's own name is on
  the enclosing `<testsuite>`.
- **`time` is seconds**, which is how a reporter parses it. The JSON report's
  `duration_ms` is milliseconds; do not confuse the two.
- **`<failure>` versus `<error>`** is the distinction artemis draws everywhere:
  `failure` is "it ran and gave the wrong answer", `error` is "it could not run at
  all" -- a refused connection, a file that would not compile, a capture that
  would not read.
- **One `<failure>` per step, not per assertion.** Several reporters render only
  the first `<failure>` child of a case, so every failing assertion of a step goes
  in one element: the `message` attribute is the first one plus `(+N more)`, and
  the element's text has them one per line. Nothing is dropped.
- **A scenario whose file would not compile** gets a suite holding one synthetic
  `could not load` case with the reason, because an empty suite is rendered as
  nothing much however high its `errors` count.
- **`timestamp` is on `<testsuites>` only.** artemis records when the run began,
  not when each scenario did, and a made-up per-suite timestamp would be worse
  than none.
- The counts are of cases actually emitted, so they are what a reporter adds up.

`pkg/cli/testdata/art/report_junit.golden` is a whole document from a real run
-- the same run as `report_json.golden`, for comparing the two -- and
`pkg/report/junit.go` is where the dialect is defined.

### Screenshots of a failed page

A `browser` step that does not pass leaves a PNG of the page, and `--report json`
names its path -- on the step, and on the matching `failures` entry, so an entry
stands alone:

```sh
artemis run upgrade.art                       # artemis-screenshots/
artemis run upgrade.art --screenshots shots   # shots/
artemis run upgrade.art --screenshots ""      # none
```

```json
{
  "name": "check the receipt",
  "status": "fail",
  "screenshot": "artemis-screenshots/upgrade-to-pro-check-the-receipt.png",
  "assertions": [ ... ]
}
```

The file is `<scenario>-<step>.png`, lower-cased with everything that is not a
letter or a digit collapsed to a hyphen, and it carries no timestamp: a rerun
overwrites the one before it, so the path is predictable enough for a CI job to
name the artifact it uploads. Two steps that share a name in one run get `-2`
and `-3`.

It is written after the last attempt -- the one the report describes -- and only
for a step that did not pass. The folder is created on the first failure, so a
suite of `api` steps never grows one.

### Logging

The terminal output above is all a run writes by default: no log file is created
unless you ask for one. Pass `-l` or `--log` to also write a detailed JSON log
of the run -- every request, every response and every error -- to that path:

```sh
artemis run checkout.art -l run.log
```

The log is appended to, so a path that already exists keeps its earlier runs. A
path that cannot be opened fails the command.

### Environment variables

`env("NAME")` is an ordinary expression and is legal wherever an expression is:
in a `var`, in a URL, in a header, in a `body`, in an `expect`. An unset name is
the empty string rather than an error -- an absent variable is how a scenario
says "no token".

```art
scenario "staging" {
  var url    = env("API_URL")
  var secret = env("API_PASSWORD")

  step "sign in" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"secret": secret}
    }
    expect status == 200
  }
}
```

Names come from the process environment and from the `.env` file in the working
directory, or from the file given with `-e`:

```dotenv
API_URL=https://localhost:8000
API_PASSWORD=my_secret_key
```

```sh
artemis run checkout.art -e dev.env
```

A file named with `-e` that cannot be read is a warning; a missing default
`.env` is silent.

**Remember not to commit your environment files to version control systems like
Git, as they may contain sensitive information.**

## Checking a command, not an API

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

[SPEC.md's *`terminal` steps*](SPEC.md#terminal-steps) specifies every field of
the `run` block -- `args`, `cwd`, `stdin`, `env` -- and the 1 MiB per attempt
each stream is kept up to.

## Development

```sh
make test        # go test ./...
make test-race   # what CI runs
make vet         # go vet ./...
make lint        # golangci-lint, skipped with a note if it is not installed
make cover       # coverage profile plus the total
make build       # the artemis binary
make all         # vet, lint, test, build
```

`make lint` needs golangci-lint, pinned to the version `.golangci.yml` is written for:

```sh
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
```

It is skipped -- with a note, not an error -- when the linter is not on your PATH, so a missing tool never blocks `make build`. Use `make lint-strict` if you want it to fail instead.

### Golden-file tests

`pkg/cli/testdata/art` holds whole runs: `<case>.art` is a scenario a user could
have written, `<case>.golden` is every byte artemis printed for it plus the error
it exited with. A fixture writes `%SERVER%` where the test's HTTP server goes,
and the temp path, the server's port and every duration are normalised before
comparison, so the files are stable across machines.

When a change to the report or the runner is deliberate, regenerate them and read the diff:

```sh
make golden      # go test ./pkg/cli -run TestGolden -update
git diff pkg/cli/testdata
```

### The documentation tests

`pkg/shared/readme_test.go` and `pkg/shared/spec_test.go` compile the examples in
this file and in SPEC.md. Every fenced `art` block goes through the real front
end -- `pkg/dsl/parser` and `pkg/dsl/check`, the same two the runner uses -- and
every block that is a whole scenario has to name-check as well as parse: types
inferred, every name resolvable, every field known. A fragment is wrapped in the
part of a scenario its prose puts it in and has to parse there.

So an example in either document that artemis would reject is a failing test,
not a surprise for whoever copies it. The tests also hold the prose to the code:
SPEC.md's type names against `token.TypeNames`, its operator table against what
`artemis migrate` can translate, and its report schema against
`pkg/cli/testdata/art/report_json.golden`.

The one fenced `yaml` block left in this file is `artemis migrate`'s documented
input, and it is loaded through `migrate.ParseYAMLFile` -- the same call the
command makes -- so it stays an input the command can read.

### CI

`.github/workflows/ci.yml` runs on every push and pull request: `go build ./...`, `go vet ./...`, a gofmt check, `go test -race -coverprofile=coverage.out ./...`, and golangci-lint. The same commands are available as make targets, so a red build is reproducible locally.

## Not yet

- **`db` steps.** Designed, not implemented.
- **Control flow, functions, imports.** `if`, loops, `parallel`, `group`, `fn`,
  `import`, `setup`, `teardown` are reserved words, not features. Reuse is what a
  host language is for, and code generation is the answer to "I need real
  abstraction".
- **Agentic assertions.** `ai` is reserved and will not parse. A CI gate's most
  valuable property is determinism, and an assertion that flakes for
  unreproducible reasons is worse than a missing one.
- **Concurrent execution.** Scenarios and steps run one after another, in the
  order they are written. There is no way to ask for parallelism.
- **Request and response detail in a report.** Neither report carries a request
  body, a response body or headers: the result model does not record them. The
  line a failure came from it does -- see [Reading a failure](#reading-a-failure).
