# Artemis DSL: replacing YAML with a parsed language

**Date:** 2026-10-02
**Status:** design approved, pending implementation plan

## Problem

Artemis scenarios are YAML. A scenario is not data, it is a script: it has ordered
steps, variables, values captured from one step and referenced in the next, retry
policy, and conditions. YAML can only describe that shape, never express it, so
every expressive need has been met by inventing syntax *inside* YAML strings and
scalars:

- `{{name}}` templating with a hand-rolled single-pass substituter
  (`pkg/shared/template.go`), no escape syntax, and `{{env.NAME}}` legal in exactly
  one position (a variable's `value`) but nowhere else.
- An assertion operator table (`equals`, `contains`, `matches`, `exists`, `type`,
  `gt`, `gte`, `lt`, `lte`) encoded as three sibling keys, `operator:`, `value:` and
  `type:`, which is a comparison expression spelled out as a record.
- `retry:` needing a custom `UnmarshalYAML` to accept two shapes.
- Request bodies as JSON *strings* with `{{var}}` spliced into them textually,
  so a captured object is pasted into a body by string concatenation.

Diagnostics are limited to what a YAML decoder can say. `KnownFields(true)` buys
"field X not found" with a line number; it cannot say "you wrote `statu`, you meant
`status`", because to the decoder every key is equally unknown.

Four motivations were confirmed for this change:

1. **Expressiveness.** Assertions want to be expressions.
2. **Verbosity.** Nested maps are a poor fit for a linear script.
3. **Errors.** Precise, actionable diagnostics are the single biggest authoring win.
4. **Identity.** A language is a stronger product than a YAML schema.

### The AI-authoring premise

The trigger for this work is that scenario files are increasingly written by
models, not typed by hand. That cuts in a specific direction and the design
follows it:

- It removes the usual objection to a DSL. Nobody has to learn it, so unfamiliar
  syntax stops being an adoption tax. This is the strongest argument for doing
  this at all.
- It *weakens* the verbosity argument. Nobody is hand-typing the six lines.
- It *sharpens* the error argument. An agent authoring scenarios needs a fast,
  precise compile-error loop more than a human does, because the loop runs more
  often and without judgment.
- It adds one new constraint. A brand-new DSL is the one format a model has seen
  zero examples of, against millions of lines of YAML and pytest. So the grammar
  must be either small enough to specify completely in a prompt or familiar
  enough to guess correctly. **"A model writes it correctly on the first try" is a
  design goal, not an afterthought.** It is why the syntax below is deliberately
  HCL-shaped rather than novel.

## Decisions

| Decision | Choice | Rejected |
| --- | --- | --- |
| Runtime role | Artemis still executes. The DSL replaces the front end only. | Compile-only (would make ART-1/15/16/17 dead weight) |
| Transpilation | Secondary output: escape hatch and native-stack integration | Transpiler as the product |
| Expressiveness | T1: declarative steps plus an expression layer | T0 (no expressions), T2 (control flow), T3 (functions/imports) |
| Syntax | Declarative blocks with embedded expressions, HCL-shaped | HTTP-literal (Hurl-shaped), statement/script-shaped |
| YAML | Replaced, with a one-shot `artemis migrate` | Hard delete; indefinite dual support |
| Agentic assertions | `ai` is a reserved keyword that fails to parse with a pointer | Shipping it; leaving it unreserved |

### Why Artemis keeps executing

The transpiler is additive, not a replacement. Codegen hangs off the same AST the
interpreter walks. This keeps the entire v3 roadmap valuable: ART-1's result
model, ART-15's `Executor` seam, ART-16's HTTP step and ART-17's `exec` step are
all downstream of parsing and are untouched by this change.

### Why T1 and not T2

Every expressive feature is implemented at least four times: once in the Go
interpreter and once per codegen target, with identical semantics. Expressiveness
is priced in backends, not in parser code. T1 concentrates the readability win
(it deletes the operator table entirely) at the lowest backend cost, because an
expression tree lowers mechanically into a native assertion in every target. Each
T2 feature is independently shippable later.

### The constraint that chose the syntax

**Named-step identity must stay syntactic.** Artemis's output model is built on
it: ART-1's result tree is run → scenario → step → assertion, the console report
prints a line per named step, and the seven golden files in `pkg/cli/testdata`
pin that exact shape. A syntax where requests are free-floating expressions
(`let res = post(...)`) dissolves the step boundary and takes the reporting with
it. `step "login" { ... }` is a block, so the parser hands the runner exactly the
node ART-15's `Executor` already wants.

The same block grammar is what lets ART-17's `exec` arrive as a new verb rather
than a new format, and what lets T2 arrive later as new block types without a
breaking change.

### Prior art considered: tester.army `e2e`

`e2e` (https://tester.army/e2e) is an open-source AI testing framework that bets
the opposite way on both axes: plain TypeScript instead of a DSL, and AI at
runtime (`agent.act('upgrade the workspace to the Pro plan')`) instead of at
authoring time. Its headline feature is caching resolved agent actions and
replaying them in CI without model calls.

It does not change this design, for three reasons:

1. **The domain gap that justifies their design does not exist here.** Browser E2E
   has a large distance between intent ("upgrade to Pro") and mechanics (which
   selector, when it settles); the runtime agent exists to close it. API testing
   has almost no such gap. `POST /token` and `status == 200` are already
   mechanically precise, so a runtime agent has little to resolve.
2. **TypeScript is an ecosystem choice that would be a regression here.** Their
   users already have Node and Playwright habits. Artemis is a single Go binary
   with no runtime dependency, which is its actual advantage in CI. Requiring Node
   to test a Java backend is a loss.
3. **A host language forecloses transpilation.** Eject-to-Python/Go/JS requires a
   language-neutral source. A DSL provides one; TypeScript cannot.

Two things are worth taking from them:

- **Migration is the adoption path.** They ship guides for Playwright, Cypress,
  Selenium, Puppeteer, Appium, Maestro, Mabl and Bug0. Artemis's Postman importer
  is the same move and is currently buried at the bottom of the README. Converting
  an existing suite in an afternoon sells a test tool; syntax elegance does not.
- **Resolve once, replay deterministically.** Their artifact is a cached action
  trace. Artemis's is the AST: parsed once, then either interpreted or emitted as
  code. Making that explicit is what keeps interpreter and transpiler one system
  rather than two codebases.

## The language

File extension `.art`. One or more scenarios per file.

```
scenario "checkout" {
  var url  = env("API_URL")
  var pw   = env("API_PASSWORD")

  step "login" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"username": "alice", "password": pw}
    }
    expect status == 200
    capture token = body.data.access_token
  }

  step "orders" {
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
    expect body.data.count is number
  }
}
```

### Grammar

```ebnf
File        = { Scenario } ;
Scenario    = "scenario" String "{" { VarDecl | StepDecl } "}" ;
VarDecl     = "var" Ident "=" Expr ;
StepDecl    = "step" String "{" { StepStmt } "}" ;

StepStmt    = Request | Expect | Capture | Retry | Timeout ;
Request     = Method String [ "{" { ReqField } "}" ] ;
Method      = "get" | "post" | "put" | "patch" | "delete" | "head" | "options" ;
ReqField    = "header" String "=" Expr
            | "query"  String "=" Expr
            | "body"   "=" Expr ;
Expect      = "expect" Expr ;
Capture     = "capture" Ident "=" Expr ;
Retry       = "retry" "{" [ RetryField { Sep RetryField } ] "}" ;
RetryField  = "times" "=" Int | "delay" "=" String ;
Sep         = "," | newline ;
Timeout     = "timeout" "=" String ;

Expr        = Or ;
Or          = And { "or" And } ;
And         = Not { "and" Not } ;
Not         = "not" Not | Cmp ;
Cmp         = Unary [ BinOp Unary | "exists" | "is" TypeName ] ;
BinOp       = "==" | "!=" | "<" | "<=" | ">" | ">="
            | "contains" | "matches" ;
TypeName    = "string" | "number" | "boolean" | "object" | "array" | "null" ;
Unary       = "-" Unary | Postfix ;
Postfix     = Primary { "." Ident | "[" Expr "]" } ;
Primary     = Number | String | Regex | Bool | "null" | Ident
            | Object | Array | Call | "(" Expr ")" ;
Call        = Ident "(" [ Expr { "," Expr } ] ")" ;
Object      = "{" [ String ":" Expr { "," String ":" Expr } ] "}" ;
Array       = "[" [ Expr { "," Expr } ] "]" ;
```

Comments are `#` to end of line. Trailing commas are permitted in objects and
arrays. Statement separation is by newline; no semicolons. Inside `retry`,
`{ times = 3, delay = "2s" }` and the same two fields on separate lines are both
valid, so a one-line policy does not force a multi-line block.

`not` binds looser than the `exists` and `is` predicates, so
`not body.x exists` means `not (body.x exists)`, which is the only reading anyone
wants. It binds tighter than `and` and `or`.

### Expression scope

Inside a step, these roots are bound:

| Root | Type | Meaning |
| --- | --- | --- |
| `status` | number | HTTP status code of the attempt |
| `body` | JSON value | response body parsed as JSON; navigable with `.` and `[]` |
| `raw` | string | response body as text, unparsed |
| `headers` | object | response headers, e.g. `headers["Content-Type"]` |

Plus every `var` in the scenario and every `capture` from an earlier step, by
name. `raw` is what makes a regex capture work against a body that is not JSON,
which is ART-18's R10 requirement.

One builtin: `env("NAME")`. It is an ordinary expression, legal anywhere an
expression is legal. This removes the current wart where `{{env.*}}` resolves only
inside a variable's `value`. An unset name is the empty string, preserving today's
documented behaviour that an absent variable is how a scenario says "no token".

### String interpolation

`"${ Expr }"` inside a double-quoted string. A full expression is permitted, so
`"${base}/users/${body.data.id}"` works. `\$` is a literal dollar sign, which fixes
the current "there is no escape syntax" limitation.

Interpolated values render with ART-6's existing `renderValue` rules, unchanged: a
number as written (`42`, not `42.000000`), a boolean as `true`/`false`, null as
`null`, and an object or array as compact JSON.

An unclosed `${` and an unknown identifier are both parse-time errors, not
run-time ones. This is stricter than today, where a missing variable fails the
step at run time. It is the point of the change: the file either compiles or it
does not.

### Request bodies

`body = {"username": "alice", "password": pw}` is an object literal serialised to
JSON, not a string with placeholders spliced into it. This removes the current
failure mode where a captured object is pasted into a JSON string by text
concatenation. A string is still accepted (`body = "raw text"`) for non-JSON
payloads.

### Operator mapping

| YAML today | DSL |
| --- | --- |
| `status_code: 200` | `expect status == 200` |
| `operator: equals` | `expect body.x == v` |
| `operator: contains` | `expect body.x contains v` |
| `operator: matches` | `expect body.x matches /re/` |
| `operator: exists` | `expect body.x exists` |
| `operator: exists, value: false` | `expect not body.x exists` |
| `type: number` | `expect body.x is number` |
| `operator: gt` / `gte` / `lt` / `lte` | `>` / `>=` / `<` / `<=` |
| `scripts: [{key: k, path: p}]` | `capture k = <path expr>` |
| `{{name}}` | `"${name}"` |
| `{{env.NAME}}` | `env("NAME")` |

### One expect is one assertion

Each top-level `expect` produces exactly one assertion in the result tree. An
`and` chain inside a single `expect` is still one assertion. This keeps the
mapping from source line to reported assertion one-to-one and preserves ART-1's
granularity; authors who want two assertions write two `expect` lines.

### Reserved keywords

Reserved now so later tiers do not require a breaking change. Using one is a parse
error naming the release it is reserved for:

`if`, `else`, `for`, `in`, `while`, `parallel`, `group`, `fn`, `return`, `import`,
`use`, `let`, `setup`, `teardown`, `ai`.

`ai` is reserved specifically for a possible future agentic assertion
(`expect ai "the error explains the card was declined"`). It is **not** in scope
here: a CI gate's most valuable property is determinism, and a non-deterministic
assertion that flakes for unreproducible reasons is worse than a missing one.

## Architecture

```
.art source
    |
    v
pkg/dsl      lexer -> parser -> AST -> checker        (diagnostics)
    |
    +--> pkg/dsl/lower   -> runtime scenario ----> pkg/executor (ART-15/16/17)
    |                                                   |
    |                                              pkg/result -> pkg/report
    |
    +--> pkg/codegen     -> python | go | js source files
```

Parsing, checking and lowering are the only new things in the run path. Everything
below `pkg/executor` is unchanged.

### Packages

| Package | Responsibility |
| --- | --- |
| `pkg/dsl/token` | token kinds; `Position{File, Line, Col, Offset}` |
| `pkg/dsl/lexer` | source bytes to tokens; every token carries a position span |
| `pkg/dsl/ast` | node types; every node carries a span |
| `pkg/dsl/parser` | recursive descent, precedence climbing for expressions |
| `pkg/dsl/check` | name resolution, reserved words, type sanity, arity |
| `pkg/dsl/diag` | `Diagnostic{Span, Message, Hint}`; rendering; multi-error collection |
| `pkg/dsl/print` | AST to canonical source; the formatter, and the backend for `migrate` and Postman `generate` |
| `pkg/dsl/lower` | AST to the runtime structures `pkg/executor` consumes |
| `pkg/eval` | expression evaluation against a response plus scope |
| `pkg/codegen` | `Target` interface and the three backends |

### What changes in existing code

- `pkg/shared.ParseYAMLFile` leaves the run path. It survives only inside
  `pkg/cli/migrate.go`.
- `models.Response.Body []BodyCheck` is replaced by compiled expressions on the
  step. `models.BodyCheck`, `models.Script` and the `operator`/`type` machinery in
  `pkg/shared/assert` are deleted; `pkg/eval` subsumes them.
- `pkg/shared/template.go`'s `{{}}` substituter is deleted. Interpolation is a
  lexer concern now. `renderValue`'s rules survive in `pkg/eval`.
- `models.Retry` keeps `Attempts()` and `Wait()`; its custom `UnmarshalYAML` goes
  with the YAML.

### Relationship to the in-flight v3 roadmap

| Issue | Effect |
| --- | --- |
| ART-15 (`Executor` interface, registry) | Unaffected. Land as planned; the DSL lowers onto this seam. |
| ART-16 (HTTP step behind the seam, timeouts) | Unaffected. Land as planned. |
| ART-17 (`exec` step type) | Keep the semantics, replace the YAML surface with DSL blocks. |
| ART-18 (`scripts:` to `capture:`) | **Supersede.** ART-18 defines a new YAML spelling that this work immediately replaces. Its *semantics* (JSON-path or regex, type preservation, sorted order, one errored assertion per failed capture) are adopted wholesale by `capture`; its YAML syntax should never ship. |

Recommendation: land ART-15 and ART-16, fold ART-18's semantics into this work,
and re-surface ART-17 in DSL syntax.

## Diagnostics

This is the feature, not a side effect. The bar is a compiler's, not a decoder's.

```
checkout.art:12:10: unknown field "statu"
   12 |   expect statu == 200
      |          ^^^^^
   hint: did you mean "status"?

checkout.art:19:5: "parallel" is a reserved keyword
   19 |     parallel {
      |     ^^^^^^^^
   hint: reserved for a future release; not available in this version
```

Requirements:

- Every diagnostic carries file, line, column and an end column, so the caret span
  covers the offending token exactly.
- The source line is echoed with the caret underneath.
- Unknown identifiers get a "did you mean" suggestion by edit distance against the
  names actually in scope.
- **All** errors in a file are reported, not just the first. Parsing recovers at
  statement boundaries.
- Nothing panics. A malformed file produces diagnostics and a non-zero exit, never
  a stack trace. This continues the value ART-6 and ART-7 established.

## Commands

| Command | Behaviour |
| --- | --- |
| `artemis test -f x.art` | Parse, check, lower, run. Unchanged reporting and exit codes. |
| `artemis parse -f x.art` | Parse and check only. No requests. Non-zero if it would not run. |
| `artemis fmt [-w] x.art` | Canonical formatting via `pkg/dsl/print`. |
| `artemis migrate -f old.yaml [-o new.art]` | One-shot YAML to DSL. The only remaining YAML reader. |
| `artemis generate -f postman.json` | Postman to `.art`, retargeted onto the same printer. |
| `artemis grammar` | Print the EBNF grammar. Exists so an agent can be handed the complete language in one command. |

## Error handling

Three distinct failure classes, kept distinct:

1. **Compile errors** (parse, resolve, reserved word, unclosed interpolation,
   uncompilable regex, bad duration literal): reported as diagnostics before any
   request is sent, non-zero exit. This is a strictly larger class than today, by
   design: unknown variables and bad regexes move from run time to compile time.
2. **Step errors** (transport failure, timeout, unrenderable value): the step
   errors with a reason, as today. Not assertions.
3. **Assertion failures**: evaluated expression is false. The failure message is
   generated from the expression and its evaluated operands, so
   `expect body.data.count > 0` reports `body.data.count > 0, got 0`. The evaluator
   retains sub-expression values for this purpose.

An evaluation that cannot be performed at all against a live response (a path that
is absent, `>` applied to an object) remains an *errored assertion* with a reason,
matching current behaviour.

## Codegen

```go
type Target interface {
    Name() string
    Generate(*ast.File) ([]GeneratedFile, error)
}
```

| Target | Output |
| --- | --- |
| `python` | pytest plus `requests` |
| `go` | `go test` plus `net/http` |
| `js` | vitest plus `fetch` |

Lowering rules, identical across targets:

- One scenario becomes one test function; steps are ordered statements inside it.
- `capture` becomes a local variable.
- `expect` becomes the target's native assertion.
- `retry` becomes a small helper emitted once per generated file.
- `env()` becomes the target's environment lookup.

Generated code is a **one-way export**. Artemis does not re-import it. This is
stated plainly in the output header so nobody treats it as a round trip.

## Testing

| Layer | Approach |
| --- | --- |
| Lexer/parser | Unit tests including exact position assertions |
| Diagnostics | A corpus of invalid files, each with its full expected diagnostic text as a golden. Written first; this is the feature. |
| Formatter | Round trip: parse, print, parse yields an identical AST; printing is idempotent |
| Evaluator | Table-driven over every operator and type combination, including the error cases |
| **Behaviour parity** | The seven existing fixtures in `pkg/cli/testdata` are migrated to `.art`. Their `.golden` outputs must be byte-identical except where the format intentionally changed. This is the strongest available proof the rewrite preserved behaviour. |
| Codegen | Golden files per target. Plus an execution-parity test for Python: run the generated pytest against the same fixture server and assert the same pass/fail outcome. Skipped with a note if `python3` is absent, matching how `make lint` handles a missing linter. |
| README | `readme_test.go` retargeted: every fenced `art` block must parse, and every whole scenario must check. |
| Fuzz | Native Go fuzzing on the parser. No input may panic. |

## Risks

| Risk | Mitigation |
| --- | --- |
| Largest change Artemis has had; easy to stall half-done | Sequenced milestones below, each independently shippable |
| A model has never seen this syntax | Keep the surface small; `artemis grammar` prints the complete EBNF; README carries a full reference; HCL-shaped so a wrong guess is still close |
| Three codegen backends drift apart | Shared conformance corpus; Python execution-parity test |
| Diagnostics under-built, which would forfeit the main win | Diagnostic goldens written before the parser is finished |
| Silent behaviour change during the rewrite | Migrated fixtures must reproduce existing goldens byte for byte |

## Milestones

1. **M1 Front end.** Lexer, parser, AST, checker, diagnostics, formatter.
   `artemis parse` and `artemis fmt` work. Nothing executes yet.
2. **M2 Execution parity.** Lowering, evaluator, `artemis test` on `.art`. The
   migrated fixtures reproduce the existing golden files.
3. **M3 Migration and docs.** `artemis migrate`, Postman `generate` retargeted,
   YAML removed from the run path, README rewritten.
4. **M4 Codegen.** Python first, then Go, then JS.

M1 to M3 deliver the DSL. M4 delivers the escape hatch and native-stack
integration, and can slip without blocking the rest.

## Non-goals

- Control flow of any kind: `if`, loops, data-driven tables, `parallel` (T2).
- Functions, imports, fixtures, setup/teardown (T3). Reuse is what the host
  language is for, and the eject path is the answer to "I need real abstraction".
- Agentic assertions (`ai`). Reserved, not implemented.
- YAML as a runtime format. It survives only as a migration input.
- Re-importing generated code.
- Machine-readable report formats (JUnit, JSON report). Still open, still unrelated
  to this change.
