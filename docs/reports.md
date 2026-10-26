# Reports, traces and logs

The console summary is for a person reading a terminal. This page is everything
else a run can write: a report another program reads, a live stream of events,
screenshots of a failed page, a log, and a trace of what each step sent and got
back.

## Machine-readable reports

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
produce both; a format given twice, and two formats aimed at stdout, are errors
-- as is a report on stdout beside `--events`, which has stdout to itself.

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

### The document

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
[SPEC.md's *The JSON report*](../SPEC.md#the-json-report) -- the one place it is
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

### JUnit XML

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

## Watching a run: `--events ndjson`

`--events ndjson` writes the run to stdout **as it happens**, one JSON object
per line, for a program that shows a run live -- a UI that ticks steps off as
they finish. The console report moves to stderr, as it does for a report on
stdout, so every line of stdout is one event:

```sh
artemis run ./suite --events ndjson                             # the stream alone
artemis run ./suite --events ndjson --report json=results.json  # and the record
```

The stream is not the record of the run; `--report json` still is. A report
beside `--events` needs a path, because both cannot have stdout: `--events
ndjson --report json` is refused before anything runs.

With `--jobs`, files run at once and their events interleave on the one
stream. Every event names its file and scenario, and within one file the order
below holds. Unlike the console, the stream is not held back until a file is
done: it stays live under `--jobs`. A `secret` operand is `***` here as it is
in the report.

The events, in order:

```
{"event":"run-start","files":["suite/01_login.art","suite/02_items.art"]}
{"event":"scenario-start","file":"suite/01_login.art","scenario":"login","steps":["get a token"]}
{"event":"step-start","file":"suite/01_login.art","scenario":"login","step":"get a token","index":0}
{"event":"step-end","file":"suite/01_login.art","scenario":"login","index":0,"result":{ ...a step of the JSON report... }}
{"event":"scenario-end","file":"suite/01_login.art","scenario":"login","result":{ ...a scenario of the JSON report, without steps... }}
{"event":"run-end","result":{ ...the JSON report, without scenarios and failures... }}
```

Every `result` is the JSON report's own shape, with the same keys, so one model
reads both. A step's assertions are in its `step-end`: they are only known when
the step ends.

- **A file that does not compile** is a `scenario-start` with `"scenario":""`
  and `"steps":[]`, then a `scenario-end` with `"status":"error"` and the first
  diagnostic in `error`. The caret-gutter diagnostics go to stderr, as always.
- **A scenario that cannot start** -- vars that will not bind, a `config
  browser` that will not resolve -- has its `scenario-start`, listing the steps
  it would have run, and an errored `scenario-end` with no step events between.
- **A run the environment check stops** lists its files in `run-start` and then
  goes straight to `run-end`, with `"status":"error"` and the reason in `error`:
  none of the scenarios that compiled was started. A file that did not compile
  still gets its pair of events.

The MCP server's `artemis_run` does not pass `--events`: what it returns is
still the one document `--report json` prints.

`pkg/cli/testdata/art/events_*.golden` are whole streams from real runs -- a
pass, a failure, a file that does not compile, and `events_report_json.golden`,
the same run as `report_json.golden` for comparing the two -- and
`pkg/report/events.go` is where the events are defined.

## Screenshots of a failed page

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

## Logging

The console summary is all a run writes by default: no log file is created
unless you ask for one. Pass `-l` or `--log` to also write a detailed JSON log
of the run -- every request, every response and every error -- to that path:

```sh
artemis run checkout.art -l run.log
```

The log is appended to, so a path that already exists keeps its earlier runs. A
path that cannot be opened fails the command.

## Tracing a run

A report says what failed. It does not say what came back, which is the thing you
want when a CI job goes red with `body.total > 0, got 0` and the service is three
deploys along from what you have locally.

`--trace` writes that, one file per step:

```sh
artemis run checkout.art --trace traces
```

```json
{
  "scenario": "checkout",
  "step": "get a token",
  "type": "api",
  "attempt": 1,
  "sent": {
    "method": "POST",
    "url": "https://api.test/token",
    "headers": { "Content-Type": "application/json" },
    "body": { "username": "alice", "password": "***" }
  },
  "observed": {
    "status": 401,
    "headers": { "Content-Type": "application/json" },
    "body": { "error": "password expired" },
    "raw": "{\"error\":\"password expired\"}"
  }
}
```

Every step gets one, whether it passed or not: comparing the step that worked
with the step that did not is half of how anyone finds the difference. A step
that could not run at all gets one too, holding what it sent and the reason --
that is the case with no response to read, so what went out is the whole of the
evidence.

What is under `observed` is whatever the step's type binds, so a `run` step
traces `exit_code`, `stdout` and `stderr` instead, and nothing about the format
is specific to HTTP.

**It is off unless you ask**, and values from [`secret`](environment.md#keeping-a-credential-out-of-a-report)
bindings are withheld in two ways. The marked field is replaced, so one secret
password does not hide the body around it. And the secret's *value* is removed
wherever else it appears -- which matters because a service that echoes a token
back in a later response has put the credential somewhere no `capture` ever
named, and no amount of static analysis can know that.

Withholding covers what the scenario declared. A trace still holds whatever the
service answered, so treat the folder as sensitive.
