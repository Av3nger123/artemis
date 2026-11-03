# Commands

`artemis run` runs scenario files. The other commands read the language
without running any of it -- they check, format, expand and print a file
through the same front end `run` uses, so none of them can disagree with it
about what a file means. Reports, traces and logs are in
[reports.md](reports.md), and running a large folder is in
[suites.md](suites.md).

## `artemis run`

```sh
artemis run scenarios/checkout.art   # one scenario file
artemis run tests/                   # every scenario file under a folder
```

`run` takes one path. A scenario file is run on its own: every scenario in it
is one run of its flow. A folder is walked recursively for `*.art` files, which
are run in path order -- the same order on every machine -- as **one run**: one
summary, one exit code. A flow file or a collection file in the folder is
skipped, because only a scenario file runs; a flow file named as the path is an
error whose hint names the scenario files that run it. Nothing is shared
between files or between scenarios: each starts with its flow's vars and its
own values, so what one writes is invisible to the next.

A path that holds no scenario file is an error, not an empty run that passes. A
file that does not compile is reported as an errored scenario, with its
diagnostics, and the files after it still run -- one typo does not hide the rest
of the folder. A mistake in a flow is reported at the flow's line, with the
scenario file that ran it as `used from`.

A run prints a line per step as it finishes, the checks that did not pass under
the step that made them, the blocks to go and fix, and a summary. It exits 0
only if everything passed:

```
tests/flows/broken.art:4:5: unknown field "timeot"
   4 |     timeot = "5s"
     |     ^^^^^^
   hint: did you mean "timeout"?
   used from tests/scenarios/broken.art:1:1: flow "../flows/broken.art"
scenario: tests/scenarios/broken.art
  ERROR tests/flows/broken.art:4:5: unknown field "timeot"
scenario: items (tests/scenarios/items.art)
  FAIL  list items                    1ms
         body.data.count >= 1, got 0
  FAIL  missing route                <1ms
         status == 200, got 404
scenario: login (tests/scenarios/login.art)
  ok    get a token                  <1ms

3 failures:

1) tests/scenarios/broken.art
     error     tests/flows/broken.art:4:5: unknown field "timeot"

2) tests/flows/items.art:7
     scenario  items
     step      list items
     assert    expect body.data.count >=
     expected  1
     actual    0

3) tests/flows/items.art:12
     scenario  items
     step      missing route
     assert    expect status ==
     expected  200
     actual    404

Scenarios     3  (1 passed, 1 failed, 1 errored)
Steps         3  (1 passed, 2 failed)
Assertions    4  (2 passed, 2 failed)
FAIL in 3ms
```

`artemis test -f checkout.art` is the old name for a one-file run. It still
works and still fails the process on a failing run, but it prints a deprecation
line: use `artemis run`.

## Reading a failure

Under the step list, a failing run prints one block per thing to go and fix.
Each block stands alone -- it names the file and the line, the scenario and the
step, so nothing above it has to be read -- because the reader is as often the
agent that wrote the test as a person scrolling a CI log.

| Field | What it is |
| --- | --- |
| the heading | `file:line` -- the line to edit, in the flow or in the collection a step called: the `expect` that failed, or the step `var` that could not be written. A step that could not run at all points at its own first line. A line artemis does not know is left off rather than printed as `:0`. |
| `scenario` | The scenario's title. Absent for a file that would not compile: there was no title to read. |
| `step` | The step's name. Absent for a file that would not compile, which never ran one. |
| `assert` | What was checked: the statement it came from, the subject's source text, and the comparison applied -- `expect body.data.count >`. |
| `expected` / `actual` | The two values as evaluated. A string is quoted and a number is not, so `"200"` and `200` do not read alike; when the two sides are different JSON types each is followed by its type. |
| `error` | In place of `expected`/`actual` when there was nothing to compare: a path that did not resolve, `>` against an object, a command that could not be started, a file that would not compile. |

A step that could not run, and a scenario whose file would not compile, each get
a block too:

```
1) flows/down.art:3
     scenario  down
     step      ping
     error     performing request: Get "http://127.0.0.1:1/orders": dial tcp 127.0.0.1:1: connect: connection refused
```

That distinction runs through everything artemis prints and everything it
reports: **failure** is "it ran and gave the wrong answer", **error** is "it
could not run at all". A flaky environment should not read as a broken API.
[SPEC.md](../SPEC.md#the-four-kinds-of-failure) specifies the four classes --
compile error, environment error, step error, assertion failure -- and which of
them is retried.

The blocks come between the step list and the tallies, so the verdict line is
still the last thing a run writes. The same list is in the JSON report, flat, as
`failures`.

## `artemis parse`

```sh
artemis parse -f scenarios/broken.art
```

`parse` lexes, parses and name-checks the file and runs nothing: no request is
sent, no command is run. It prints every problem it finds -- all of them, in
file order, with the source line echoed -- and exits non-zero if any is an
error. A flow is checked as the scenarios that run it, so parse a scenario
file to check it, its flow and the collections the flow calls:

```
tests/flows/broken.art:4:5: unknown field "timeot"
   4 |     timeot = "5s"
     |     ^^^^^^
   hint: did you mean "timeout"?
   used from tests/scenarios/broken.art:1:1: flow "../flows/broken.art"
1 error in tests/scenarios/broken.art
```

A clean file says so, because a command whose only job is to tell you something
has to say something when the answer is yes:

```
tests/scenarios/login.art: ok
```

**Changed with flows:** a step that writes a name that is already bound -- a
refresh that writes `token` again, a step that writes a flow var -- replaces
it, and the steps after it read the new value. The one fault left is two
declarations of a name in the same place, two vars at the top of a flow or two
inputs of one item, which is the compile error `duplicate-binding`.

### `--json`

`artemis parse -f scenarios/broken.art --json` writes the same diagnostics to
stdout as one document instead, which is what an editor or a UI reads:

```json
{
  "diagnostics": [
    {
      "code": "unknown-field",
      "severity": "error",
      "span": {
        "file": "tests/flows/broken.art",
        "line": 4,
        "col": 5,
        "endLine": 4,
        "endCol": 11,
        "offset": 68
      },
      "usedFrom": [
        {
          "file": "tests/scenarios/broken.art",
          "line": 1,
          "col": 1,
          "endLine": 1,
          "endCol": 27,
          "offset": 0
        }
      ],
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
`usedFrom` is the chain that brought the line in: the scenario file that ran
the flow, and the step that called an item, outermost last.
The span's `offset` and `endCol` locate the text to underline. A suggestion's
`replace` is a mechanical fix, so a client can offer one-click correction. A
clean file writes `{"diagnostics": []}`. The exit status is the same either way:
`--json` is a way to read the diagnostics, not a way to make a broken file pass.
`artemis grammar --json` lists every code that can appear here.

## `artemis fmt`

```sh
artemis fmt flows/checkout.art       # the formatted file on stdout
artemis fmt -w flows/checkout.art    # rewrite it in place
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

## `artemis ast`

```sh
artemis ast -f flows/login.art       # the syntax tree as JSON
artemis ast --from-json < tree.json  # that tree back to .art source
```

The same tree, from the same front end that runs the tests, so a UI, a
transpiler and the runtime cannot drift apart. Every node carries its span --
`file`, `line`, `col`, `endLine`, `endCol`, `offset` (left out below) -- and the
document carries a `schemaVersion`, because a UI ships and upgrades
independently of the CLI. A flow is `"kind": "flowDecl"`, a scenario file's
header `"flowRef"` and a scenario's `name = value` line `"set"`; a block such
as `parallel` or `if` is `"kind": "flow"`, the name it had before flows:

```json
{
  "schemaVersion": 1,
  "file": "flows/login.art",
  "scenarios": [
    {
      "kind": "flowDecl",
      "name": "login",
      "body": [
        {
          "kind": "var",
          "name": "user",
          "value": {
            "kind": "literal",
            "literal": "string",
            "text": "\"alice\"",
            "value": "alice"
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

## `artemis expand`

```sh
artemis expand scenarios/checkout.art                      # to stdout
artemis expand scenarios/checkout.art -o checkout.flat.art # to a file
```

What will actually run: each scenario of the file as its flow with the
scenario's values in its vars, imports resolved from disk, `skip` applied, and
every item call replaced by the step it stands for, preceded by a comment
naming the item, the line it is declared on, and the step that called it.

The file is checked first, exactly as `artemis run` checks it, and nothing is
printed for a file with errors. `-o` will not replace an existing file without
`--force`. See [Reusing requests](flows.md#reusing-requests).

## `artemis grammar`

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
