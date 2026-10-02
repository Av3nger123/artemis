# Artemis

Artemis is a command-line tool for automated testing of REST APIs, built with Go and Cobra. It provides a convenient way to ensure the stability and correctness of your API endpoints through automated testing procedures.

## Features

- Easy-to-use command-line interface (CLI) powered by Cobra
- REST API endpoints described as steps in a YAML file, run one file or a whole folder at a time
- Assertions with real operators: `equals`, `contains`, `matches`, `exists`, `type`, `gt`/`gte`/`lt`/`lte`
- Values captured from one response and templated into the next
- A run summary on the terminal, plus an opt-in JSON log of every request, response and error
- `--report json`: the whole outcome of a run as one JSON document, for a CI job or an agent to read
- `--report junit`: the same run as JUnit XML, so CI surfaces failures under the scenario they came from
- A non-zero exit code whenever anything fails, so a CI job goes red on a broken API
- Postman collections converted to Artemis YAML as a starting point

## Installation

To install Artemis, make sure you have Go installed and then run:

```bash
source ./install.sh
```

## Commands

### Running scenarios

```sh
artemis run sample.yaml      # one file
artemis run ./suite          # every scenario under a folder
```

`run` takes one path. A file is run on its own. A folder is walked recursively
for `*.yaml` and `*.yml` files, which are run in path order -- the same order on
every machine -- as **one run**: one summary, one exit code. Nothing is shared
between files: each scenario gets its own variables, so what one file captures is
invisible to the next.

A path that holds no scenario file is an error, not an empty run that passes. A
file artemis cannot load is reported as an errored scenario naming the file and
the line, and the files after it still run -- one typo does not hide the rest of
the suite.

A run prints a line per step as it finishes, the checks that did not pass under
the step that made them, and a summary. It exits 0 only if everything passed:

```
scenario: checkout (checkout.yaml)
  ok    login                         2ms
  FAIL  orders                       <1ms
         $.status equals ok, got pending
  ERROR fetch order                  <1ms
         error performing request: Get "http://127.0.0.1:1/orders": connection refused

Scenarios     1  (1 errored)
Steps         3  (1 passed, 1 failed, 1 errored)
Assertions    3  (2 passed, 1 failed)
FAIL in 8ms
```

A folder run prints the same thing per file, with the file that would not load
in its place:

```
scenario: login (suite/01_login.yaml)
  ok    sign in                     12ms
scenario: suite/02_broken.yaml
  ERROR parse suite/02_broken.yaml: yaml: unmarshal errors:
  line 12: field respones not found in type models.Step
scenario: items (suite/nested/03_items.yaml)
  ok    list items                   4ms

Scenarios     3  (2 passed, 1 errored)
Steps         2  (2 passed)
Assertions    4  (4 passed)
FAIL in 17ms
```

`artemis test -f sample.yaml` is the old name for a one-file run. It still works
and still fails the process on a failing run, but it prints a deprecation line:
use `artemis run`.

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
  "duration_ms": 14,
  "status": "fail",
  "passed": false,
  "counts": {
    "scenarios":  { "total": 1, "passed": 0, "failed": 1, "errored": 0, "skipped": 0 },
    "steps":      { "total": 1, "passed": 0, "failed": 1, "errored": 0, "skipped": 0 },
    "assertions": { "total": 2, "passed": 1, "failed": 1, "errored": 0, "skipped": 0 }
  },
  "scenarios": [
    {
      "name": "items",
      "file": "suite/items.yaml",
      "status": "fail",
      "duration_ms": 13,
      "error": "",
      "steps": [
        {
          "name": "get item",
          "status": "fail",
          "duration_ms": 12.5,
          "attempts": 1,
          "error": "",
          "assertions": [
            {
              "kind": "status_code",
              "path": "",
              "operator": "equals",
              "expected": 200,
              "actual": 200,
              "status": "pass",
              "error": ""
            },
            {
              "kind": "body",
              "path": "$.status",
              "operator": "equals",
              "expected": "ready",
              "actual": "pending",
              "status": "fail",
              "error": ""
            }
          ]
        }
      ]
    }
  ]
}
```

| Key | Where | Meaning |
| --- | --- | --- |
| `schema_version` | run | `1`. It goes up when a key is removed or its meaning changes; a new key does not change it. |
| `started_at` | run | When the run began, RFC 3339. |
| `status` | run, scenario, step, assertion | `pass`, `fail`, `error` or `skip`. `fail` is "it ran and gave the wrong answer"; `error` is "it could not run at all". Every level above an assertion is the worst of its children, and a `skip` never drags a parent down. |
| `passed` | run | `false` if anything failed or errored. The same thing the exit code says, for a consumer that does not want to learn the vocabulary. |
| `duration_ms` | run, scenario, step | Milliseconds, to microsecond precision. |
| `counts` | run | Totals per level, so nobody has to walk the tree to say "2 of 5 assertions failed". |
| `name` | scenario, step | As written in the scenario. A scenario whose file would not load has no name, so it is `""` and `file` is what identifies it. |
| `file` | scenario | The path the scenario was read from. |
| `error` | scenario, step, assertion | Why it could not run, or `""`. A scenario's error is a file that would not load, and such a scenario has no steps. |
| `attempts` | step | How many times the step was tried; `1` unless `retry:` asked for more. |
| `kind` | assertion | What sort of check it was: `status_code`, `body`, `exit_code`, `stdout`, `stderr`. |
| `path` | assertion | What was inspected -- a JSON path for a body check, `""` for a check with nothing to address. |
| `operator` | assertion | The comparison that was applied: `equals`, `contains`, `gt`, and the rest of [Operators](#operators). |
| `expected` / `actual` | assertion | The value the scenario asked for and the value that was there, each keeping its JSON type. `null` when there was no such value -- a path that did not resolve. |

Every key is always present, with its zero value rather than omitted, so a `jq`
expression never has to tell absent from empty. `expected` and `actual` are the
exception: they are `null`, because either may legitimately be any JSON type.
Every list is a list, empty rather than `null`.

What is *not* in the document is anything artemis does not record today: no
request or response bodies, no headers, no line numbers.
`pkg/cli/testdata/report_json.golden` is a whole document from a real run, for
reading; `pkg/report/json.go` is where the shape is defined.

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
<testsuites name="artemis" tests="4" failures="2" errors="1" skipped="0" time="0.042" timestamp="2026-03-04T05:06:07Z">
  <testsuite name="health" file="suite/01_health.yaml" tests="1" failures="0" errors="0" skipped="0" time="0.004">
    <testcase name="ping" classname="suite/01_health.yaml" time="0.004"></testcase>
  </testsuite>
  <testsuite name="items" file="suite/02_items.yaml" tests="2" failures="2" errors="0" skipped="0" time="0.031">
    <testcase name="list items" classname="suite/02_items.yaml" time="0.012">
      <failure message="$.total gt 0, got 0">$.total gt 0, got 0</failure>
    </testcase>
    <testcase name="missing route" classname="suite/02_items.yaml" time="0.019">
      <failure message="status_code equals 200, got 404">status_code equals 200, got 404</failure>
    </testcase>
  </testsuite>
  <testsuite name="suite/03_broken.yaml" file="suite/03_broken.yaml" tests="1" failures="0" errors="1" skipped="0" time="0.000">
    <testcase name="could not load" classname="suite/03_broken.yaml" time="0.000">
      <error message="parse suite/03_broken.yaml: yaml: line 10: field respones not found">parse suite/03_broken.yaml: yaml: line 10: field respones not found</error>
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
  all" -- a refused connection, a template that would not resolve, a capture that
  would not read. A flaky environment should not read as a broken API.
- **One `<failure>` per step, not per assertion.** Several reporters render only
  the first `<failure>` child of a case, so every failing assertion of a step goes
  in one element: the `message` attribute is the first one plus `(+N more)`, and
  the element's text has them one per line. Nothing is dropped.
- **A scenario whose file would not load** gets a suite holding one synthetic
  `could not load` case with the reason, because an empty suite is rendered as
  nothing much however high its `errors` count.
- **`timestamp` is on `<testsuites>` only.** artemis records when the run began,
  not when each scenario did, and a made-up per-suite timestamp would be worse
  than none.
- The counts are of cases actually emitted, so they are what a reporter adds up.

`pkg/cli/testdata/report_junit.golden` is a whole document from a real run --
the same run as `report_json.golden`, for comparing the two -- and
`pkg/report/junit.go` is where the dialect is defined.

### Command for validating a YAML file without calling anything

```sh
artemis parse -f sample.yaml
```

`parse` loads the file exactly as `run` does -- strict decoding, so an unknown or
misspelled key is an error naming its line, and every step type is checked --
then prints the parsed scenario. It sends no requests, and exits non-zero if the
file is not one artemis can run.

### Command to convert Postman collection to YAML format

An additional feature that i shipped with this is to convert postman collection format to artemis yaml format for faster configuration

```sh
artemis generate -f postman_collection.json
```
**Note**: After generating a YAML file from a Postman collection, manual adjustments might be necessary to tailor the YAML file according to specific requirements. The generated file serves as a starting point and helps in maintaining the structure and format consistent with the original collection.

### Logging

The terminal output above is all a run writes by default: no log file is created
unless you ask for one. Pass `-l` or `--log` to also write a detailed JSON log of
the run -- every request, every response and every error -- to that path:

```sh
artemis run sample.yaml -l custom_log_file.log
```

The log is appended to, so a path that already exists keeps its earlier runs. A
path that cannot be opened fails the command.

### Environment variables

Artemis supports loading environment variables from a specified `.env` file. This allows you to store sensitive information or configuration-specific details outside of your main configuration files.

For custom env file path:

```sh
artemis run sample.yaml -l custom_log_file.log -e dev.env
```
When running Artemis with the -e flag followed by the path to your environment file, Artemis will load the environment variables from that file and make them available during the execution of your tests. A file named with `-e` that cannot be loaded is warned about; the default `.env` is optional and its absence is silent.

**Remember not to commit your environment files to version control systems like Git, as they may contain sensitive information.**

# Configuration

## Basic YAML Config

This configuration defines a basic API request to generate a token. It includes the following parameters:

- **name**: Name of the scenario.
- **type**: Recorded with the scenario and nothing more today; `functional` is what
  the Postman importer writes and what the examples here use.
- **variables**: Variables the steps can reference as `{{name}}`.
  - **name**: Name of the variable.
  - **value**: Value of the variable.
- **steps**: The steps, executed in the order they are written. `name`, `type`,
  `request`, `response`, `capture`, `retry` and `timeout` are all keys of a step,
  at the same indentation.
  - **name**: Name of the step.
  - **type**: What kind of step it is. `api` is a REST call with a JSON payload; `exec` runs a command -- see [Running commands](#running-commands-the-exec-step). An `api` step reads `request:` and `response:`; an `exec` step reads `exec:` and `expect:`.
  - **request**
    - **url**: The URL endpoint for the request. `"{{url}}"` is replaced with the
      value of the variable named `url`.
    - **method**: HTTP method for the request (e.g., POST).
    - **headers**: Headers to be included in the request.
    - **body**: Request body, as a string.
  - **response**
    - **status_code**: The HTTP status code the step expects.

```yaml
name: "API Collection"
variables:
  - name: "url"
    value: "https://api.example.com/v2"
type: functional
steps:
  - name: "Login"
    type: api
    request:
      url: "{{url}}/token"
      method: "POST"
      headers:
        Content-Type: "application/json"
      body: '{"username":"user_name","password":"password"}'
    response:
      status_code: 200
```

## Capturing Values

A step can pull values out of what it produced and leave them for the steps
after it. Here the access token is captured under the name `token`, which later
steps write as `{{token}}`.

- **capture**: A map from the name a value is referenced by to where the value
  comes from. Any step type can have one -- it is not an HTTP-only key.
    - A plain string is a [JSON path](https://support.smartbear.com/alertsite/docs/monitors/api/endpoint/jsonpath.html)
      into the step's output parsed as JSON: `token: "$.data.access_token"`.
    - `{json: "..."}` is the same thing written out.
    - `{regex: "..."}` matches the output as plain text, for output that is not
      JSON at all. The value is capturing group 1 when the pattern has one, and
      the whole match when it does not. Flags go inline: `(?s)`, `(?i)`.

Exactly one of `json:` and `regex:` is given. Everything that can be checked
without running anything is checked when the file is read, so a regex that will
not compile, a capture with no path, and a key that is not `json` or `regex` are
all load errors naming the line -- `artemis parse -f scenario.yaml` finds them
without sending a request.

```yaml
name: "API Collection"
variables:
  - name: "url"
    value: "https://api.example.com/v2"
type: functional
steps:
  - name: "Login"
    type: api
    request:
      url: "{{url}}/token"
      method: "POST"
      headers:
        Content-Type: "application/json"
      body: '{"username":"user_name","password":"password"}'
    response:
      status_code: 200
    capture:
      token: "$.data.token.access_token"
```

A capture that cannot be read is an errored assertion under its step, naming the
key, and the step fails -- one per unreadable capture, so two mistyped paths take
one run to find. A capture that is read writes nothing to the terminal: it is
often a token.

```yaml
name: "API Collection"
variables:
  - name: "url"
    value: "https://api.example.com/v2"
type: functional
steps:
  - name: "Follow the redirect"
    type: api
    request:
      url: "{{url}}/latest"
      method: "GET"
    response:
      status_code: 200
    capture:
      itemId: {regex: "/items/([0-9]+)"}
      name: {json: "$.name"}
  - name: "Read it back"
    type: api
    request:
      url: "{{url}}/items/{{itemId}}"
      method: "GET"
    response:
      status_code: 200
```

## Placeholders

Anywhere a step's `url`, `body` or a header value is written -- and in every
field of an `exec` step's `command`, `args`, `cwd`, `env` and `stdin` --
`{{name}}` is replaced with the value of `name`. A name resolves against the scenario's
`variables:` and against anything an earlier step captured with `capture:`.
Surrounding spaces are ignored, so `{{ url }}` and `{{url}}` are the same.

A captured value does not have to be a string: a JSON-path capture keeps the
type the response gave it. A number renders as it was written (`42`, not
`42.000000`), a boolean as `true` or `false`, a null as `null`, and an object or
array as compact JSON -- so a captured object can be templated straight into a
body:

```yaml
body: '{"user": {{user}}}'
```

Two things are errors, and fail the step before any request goes out:

- **An unknown name.** `{{tokn}}` is not quietly sent to the server as literal
  braces; the step fails saying which variable is missing.
- **An unclosed placeholder.** `{{token` with no `}}`, or `{{url}` with one
  closing brace, fails naming the placeholder and its position.

Substitution is single pass: a value that itself contains `{{x}}` is used as it
is and not expanded again. There is no escape syntax -- `{{` always opens a
placeholder, so a body that needs literal braces should keep them in a
variable's value. To use an environment variable, reference it from a variable's
value (`{{env.NAME}}`, below) rather than in a step.

## Adding Assertions

This configuration further enhances the API request by adding assertions to validate the response. It checks if the HTTP status code is 200.

- **response**: Specifies assertions to be performed on the response.
  - **status_code**: Expected HTTP status code.
  - **body**: Expected values in response.
    - **path**: The [JSON path](https://support.smartbear.com/alertsite/docs/monitors/api/endpoint/jsonpath.html) in the response body.
    - **operator**: The comparison to make. Defaults to `equals`.
    - **value**: Expected value. It keeps the type you write: `value: 200` is a number, `value: "200"` a string, `value: true` a boolean.
    - **type**: Optional. The JSON type the value at **path** must have -- one of `string`, `number`, `boolean`, `object`, `array`, `null`. A value of another type fails the check.

### Operators

| Operator | Checks |
| --- | --- |
| `equals` (default) | The value equals **value**. Numbers compare as numbers whichever way they are written, and arrays and objects compare element by element. |
| `contains` | A string contains **value** as a substring, an array has it as an element, or an object has it as a key. |
| `matches` | The value, rendered as a string, matches the regular expression in **value**. Unanchored, so `"ok"` matches `"not ok"`. |
| `exists` | The path resolves to a non-null value. `value: false` asserts the opposite -- the key must be absent. |
| `type` | The value's JSON type is **value** -- `string`, `number`, `boolean`, `object`, `array` or `null`. |
| `gt`, `gte`, `lt`, `lte` | The value is a number greater than, at least, less than or at most **value**. |

A check that cannot be made at all -- a malformed path, a path that is not in the
response, an operator Artemis does not know, a regular expression that will not
compile, `gt` against an object -- is reported as an errored assertion with the
reason, and fails the run.

```yaml
response:
  status_code: 200
  body:
    - path: "$.data.message"
      value: "success"
    - path: "$.data.id"
      operator: gt
      value: 0
    - path: "$.data.token"
      operator: exists
    - path: "$.data.roles"
      operator: contains
      value: "admin"
    - path: "$.data.email"
      operator: matches
      value: ".+@.+"
    - path: "$.data.count"
      type: "number"
      value: 3
```

```yaml
name: "API Collection"
variables:
  - name: "url"
    value: "https://api.example.com/v2"
type: functional
steps:
  - name: "Login"
    type: api
    request:
      url: "{{url}}/token"
      method: "POST"
      headers:
        Content-Type: "application/json"
      body: '{"username":"user_name","password":"password"}'
    response:
      status_code: 200
      body:
        - path: "$.data.message"
          value: "success"
          type: "string"
```

## Meta Section

A step can be retried, which is how a scenario polls an API that is not ready
yet.

- **retry**: How many times a step may be attempted, and how long to wait between attempts.
  - **times**: the total number of attempts, not the number of retries after the first — `times: 3` sends at most three requests. Omitted, zero or negative means one attempt; a step is never attempted zero times.
  - **delay**: a duration string (`"500ms"`, `"2s"`, `"1m30s"`) slept *between* attempts — never before the first, never after the last. Omitted, there is no wait at all.

  Retrying stops as soon as an attempt passes: the status code matched and every assertion held. The older scalar form `retry: 5` still works and means `times: 5`.

- **timeout**: How long one attempt may take, as a duration string (`"5s"`, `"1m30s"`). Omitted, it is **30s**. There is no way to say "wait forever": a request with no deadline is how a CI job hangs until someone notices.

  It is per attempt, not per step, so a step with `timeout: "5s"` and `retry: {times: 3}` may take fifteen seconds. A step that runs out of time fails with `no response within 5s`, which is a failed step like any other and is retried if the step asked for retries.
```yaml
name: "API Collection"
variables:
  - name: "url"
    value: "https://api.example.com/v2"
type: functional
steps:
  - name: "Login"
    type: api
    request:
      url: "{{url}}/token"
      method: "POST"
      headers:
        Content-Type: "application/json"
      body: '{"username":"user_name","password":"password"}'
    response:
      status_code: 200
      body:
        - path: "$.data.message"
          value: "success"
          type: "string"
    retry:
      times: 5
      delay: "2s"
    timeout: "10s"
```
## Running commands: the `exec` step

An `exec` step runs a command and asserts on what it did. It is the escape
hatch: anything artemis has no step type for -- a CLI, a migration script, a
health check that is a shell one-liner -- is an `exec` step.

- **exec**: What to run. The counterpart of `request:` on an `api` step.
  - **command**: The program to run. It is executed directly: there is no shell,
    no word splitting and no globbing, so `command: "ls *.go"` looks for a
    binary with a space in its name. To use a shell, name one:
    `command: "sh"`, `args: ["-c", "ls *.go | wc -l"]`.
  - **args**: The arguments, one list entry each. Quoting is yours to get right
    only in the sense that each entry arrives at the command exactly as written.
  - **cwd**: The directory to run in. Relative paths are relative to where
    artemis itself was run from, not to the scenario file.
  - **env**: Variables to add to the environment. They are layered on top of the
    environment artemis was given, and a name set here wins over an inherited
    one. There is no way to unset a variable.
  - **stdin**: Text written to the command's standard input. A step that does
    not set it gives the command a standard input that is immediately at end of
    file, so a command that reads stdin cannot hang the run.
- **expect**: What to expect of the run. The counterpart of `response:`.
  - **exit_code**: The exit code the command must have. Omitted, it is **0**, so
    a step that says nothing expects the command to succeed. There is no way to
    say "any exit code".
  - **stdout**, **stderr**: Checks against the stream as plain text, each one
    assertion.
    - **operator**: `contains` (the default), `equals`, `matches` or `empty`.
    - **value**: The text, or for `matches` the regular expression. `empty`
      takes no value.

`contains` is the default rather than `equals` because almost every command ends
its output with a newline, which makes an exact match the check that is right in
theory and wrong in practice. `empty` is "nothing but whitespace", which is how
you say *stderr was quiet*. A `matches` pattern is a Go regular expression and
unanchored; `$` is end of output, so a pattern anchoring one line of many needs
the inline `(?m)` flag.

A command that ran and exited with the wrong code is a **failed assertion**. A
command that could not be run at all -- not on the `PATH`, a `cwd` that does not
exist -- is an **errored step**, because there was no exit code to compare. A
wrong exit code does not stop the stream checks from being made: stderr is
exactly what a failed command is diagnosed from.

`capture:`, `retry:` and `timeout:` work as they do on any other step.
`capture:` reads the command's standard output -- a JSON path when the command
printed a JSON object, a regex against it as text. Each stream is kept up to 1
MiB per attempt; a command that prints more than that has the rest dropped.

```yaml
name: "Release checks"
variables:
  - name: "tag"
    value: "v1.4.0"
type: functional
steps:
  - name: "Tag exists"
    type: exec
    exec:
      command: "git"
      args: ["rev-parse", "--verify", "{{tag}}^{commit}"]
    expect:
      exit_code: 0
      stderr:
        - operator: empty
    capture:
      sha: {regex: "^([0-9a-f]{40})"}
    timeout: "5s"
  - name: "Changelog mentions the tag"
    type: exec
    exec:
      command: "sh"
      args: ["-c", "grep -c '{{tag}}' CHANGELOG.md"]
      cwd: "."
    expect:
      stdout:
        - operator: matches
          value: "^[1-9]"
  - name: "The build is reproducible"
    type: exec
    exec:
      command: "go"
      args: ["build", "./..."]
      env:
        CGO_ENABLED: "0"
    expect:
      exit_code: 0
```

## Environment support

Environment variables are read in a variable's `value`, with `{{env.NAME}}`:

```yaml
name: "API Collection"
type: functional
variables:
  - name: "url"
    value: "{{env.url}}"
  - name: "secret"
    value: "{{env.secret}}"
steps:
  - name: "Login"
    type: api
    request:
      url: "{{url}}/token"
      method: "POST"
      headers:
        Content-Type: "application/json"
      body: '{"secret":"{{secret}}"}'
    response:
      status_code: 200
```

`{{env.url}}` and `{{env.secret}}` are replaced with the values of the `url` and
`secret` environment variables, loaded from the `.env` file (or the file given
with `-e`) and from the process environment:

```dotenv
url=https://localhost:8000
secret=my_secret_key
```

The steps then use `{{url}}` and `{{secret}}` like any other variable. `{{env.*}}`
is resolved only in a variable's value, not inside a step, so a scenario has one
place where its environment is wired up. A name that is not set becomes the empty
string rather than failing the run: that is how a scenario says "no token".


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

`pkg/cli/testdata` holds whole runs: `<case>.yaml` is a scenario a user could have written, `<case>.golden` is every byte artemis printed for it plus the error it exited with. A fixture writes `%SERVER%` where the test's HTTP server goes, and the temp path, the server's port and every duration are normalised before comparison, so the files are stable across machines.

When a change to the report or the runner is deliberate, regenerate them and read the diff:

```sh
make golden      # go test ./pkg/cli -run TestGolden -update
git diff pkg/cli/testdata
```

### README examples

`pkg/shared/readme_test.go` parses the YAML in this file. Every fenced `yaml`
block has to be well-formed YAML, and every block that is a whole scenario -- one
with a top-level `steps:` -- is loaded through the same strict decoder and
validator `artemis run` uses. An example here that artemis would reject is a
failing test, not a surprise for whoever copies it.

### CI

`.github/workflows/ci.yml` runs on every push and pull request: `go build ./...`, `go vet ./...`, a gofmt check, `go test -race -coverprofile=coverage.out ./...`, and golangci-lint. The same commands are available as make targets, so a red build is reproducible locally.


## Not yet

- **Concurrent execution.** Steps run one after another, in the order they are
  written, and a scenario's steps share their captured variables. There is no way
  to ask for parallelism yet.
- **Step types other than `api` and `exec`.** `type:` accepts those two and
  nothing else; any other value is an error at load time. `db` and `browser` are
  what the executor interface was sized for, and neither exists yet.
- **Request and response detail in a report.** Neither report carries a request
  body, a response body, headers or the line of the scenario a failure came from:
  the result model does not record them yet.
