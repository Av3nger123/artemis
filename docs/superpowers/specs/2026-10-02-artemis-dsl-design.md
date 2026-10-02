# Artemis: a test DSL for APIs, browsers and terminals

**Date:** 2026-10-02
**Status:** design approved, pending implementation plans
**Supersedes:** ART-18

## Problem

Artemis scenarios are YAML. A scenario is not data, it is a script: ordered steps,
variables, values captured from one step and referenced in the next, retry policy,
conditions. YAML can describe that shape but never express it, so every expressive
need so far has been met by inventing syntax *inside* YAML strings and scalars:

- `{{name}}` templating with a hand-rolled single-pass substituter
  (`pkg/shared/template.go`), no escape syntax, and `{{env.NAME}}` legal in exactly
  one position (a variable's `value`) and nowhere else.
- An assertion operator table (`equals`, `contains`, `matches`, `exists`, `type`,
  `gt`, `gte`, `lt`, `lte`) encoded as three sibling keys `operator:`, `value:`,
  `type:`, which is a comparison expression spelled out as a record.
- `retry:` needing a custom `UnmarshalYAML` to accept two shapes.
- Request bodies as JSON *strings* with `{{var}}` spliced in textually, so a
  captured object reaches a body by string concatenation.

Diagnostics are limited to what a YAML decoder can say. `KnownFields(true)` buys
"field X not found" with a line number; it cannot say "you wrote `statu`, you meant
`status`", because to the decoder every key is equally unknown.

Four motivations were confirmed:

1. **Expressiveness.** Assertions want to be expressions.
2. **Verbosity.** Nested maps are a poor fit for a linear script.
3. **Errors.** Precise, actionable diagnostics are the single biggest authoring win.
4. **Identity.** A language is a stronger product than a YAML schema.

Artemis also grows from an API test runner into a test runner for **APIs, browsers
and terminals**, with scenarios transpilable to Python and JavaScript, and a visual
builder that reads and writes the same files. YAML was already a poor fit for one
execution target; it is a worse fit for three.

### The AI-authoring premise

Scenario files are increasingly written by models, not typed by hand. That cuts in
a specific direction and the design follows it:

- It removes the usual objection to a DSL. Nobody has to learn it, so unfamiliar
  syntax stops being an adoption tax. This is the strongest argument for doing it.
- It *weakens* the verbosity argument. Nobody is hand-typing the six lines.
- It *sharpens* the error argument. An agent authoring scenarios needs a fast,
  precise compile-error loop more than a human does, because the loop runs more
  often and without judgment.
- It adds a constraint. A new DSL is the one format a model has seen zero examples
  of, against millions of lines of YAML and pytest. The grammar must be small
  enough to specify completely in a prompt, or familiar enough to guess correctly.
  **"A model writes it correctly on the first try" is a design goal**, which is why
  the syntax is HCL-shaped rather than novel.

## Scope and decomposition

This is five subsystems, not one project. Each gets its own implementation plan;
this document is the shared design they all derive from.

| | Subsystem | Depends on | Status |
| --- | --- | --- | --- |
| **A** | Language and runtime: front end, evaluator, `api` and `terminal` steps, migration | — | specced here |
| **B** | Browser step type | A | specced here, own plan |
| **C** | Transpilers: Python, JavaScript | A, B | specced here, own plan |
| **D** | UI builder | A's tree contract | contract here, own spec |
| **E** | Editor tooling: LSP, tree-sitter, VS Code | A | **deferred by decision** |

**Sequence: A → B → C → D.** A ends with a usable tool, which is what makes it the
right first slice. B proves the design against something that is not
request/response. C and D both hang off A's tree and can proceed in parallel.

E is deferred. The risk that creates is recorded under Risks; the structural
prerequisites for it are paid for in A regardless, so deferring it costs nothing
irreversible.

## Decisions

| Decision | Choice | Rejected |
| --- | --- | --- |
| Runtime role | Artemis executes. The DSL replaces the front end. | Compile-only (would make ART-1/15/16/17 dead weight) |
| Transpilation | Secondary output: escape hatch and native-stack integration | Transpiler as the product |
| Expressiveness | T1: declarative steps plus an expression layer | T0 (no expressions), T2 (control flow), T3 (functions/imports) |
| Syntax | Declarative blocks with embedded expressions, HCL-shaped | HTTP-literal (Hurl-shaped), statement/script-shaped |
| YAML | Replaced, with a one-shot `artemis migrate` | Hard delete; indefinite dual support |
| Agentic assertions | `ai` reserved; fails to parse with a pointer | Shipping it; leaving it unreserved |
| UI authoring | In scope as subsystem D; the language and toolchain guarantee lossless machine editing | A text-only format; a UI owning its own storage format |
| Step types | `api`, `terminal`, `browser`, inferred from the action block | A `type:` key; one universal expression scope |
| Browser engine | **playwright-go** (recommendation, see below) | chromedp / go-rod |
| Codegen targets | Python, JavaScript | Go, **reserved not dropped** |
| Editor tooling | Deferred | Shipping it with A |

### Why Artemis keeps executing

The transpiler is additive. Codegen hangs off the same tree the interpreter walks.
This keeps the v3 roadmap valuable: ART-1's result model, ART-15's `Executor` seam
and ART-16's HTTP step are all downstream of parsing.

### Why T1 and not T2

Every expressive feature is implemented at least three times: the Go interpreter
and each codegen target, with identical semantics. Expressiveness is priced in
backends, not parser code. T1 concentrates the readability win (it deletes the
operator table) at the lowest backend cost, because an expression tree lowers
mechanically into a native assertion everywhere. Each T2 feature is independently
shippable later.

### The constraint that chose the syntax

**Named-step identity must stay syntactic.** ART-1's result tree is run → scenario
→ step → assertion, the console report prints a line per named step, and the seven
golden files in `pkg/cli/testdata` pin that shape. A syntax where actions are
free-floating expressions (`let res = post(...)`) dissolves the step boundary and
takes the reporting with it. `step "login" { ... }` is a block, so the parser hands
the runner exactly the node ART-15's `Executor` wants.

The same block grammar is what lets `terminal` and `browser` arrive as new action
verbs rather than new formats, and lets T2 arrive later as new block types.

### Why playwright-go

This is a recommendation to confirm, not a settled fact, and it is the most
reversible-at-cost decision here.

| | playwright-go | chromedp / go-rod |
| --- | --- | --- |
| Go dependency | Node plus the Playwright driver | pure Go |
| Semantics vs Python/JS targets | identical, all three *are* Playwright | hand-written approximation |
| Waiting | auto-waiting built in | invent a wait model |
| Transpile fidelity | near-mechanical | where codegen bugs will live |

Honest accounting: earlier in this design, "a single Go binary with no runtime
dependency" was cited as Artemis's advantage over TypeScript-based competitors.
**Adding a browser destroys that regardless of engine** — Chrome has to come from
somewhere. Given it is already lost, buying exact semantic parity across Go, Python
and JavaScript is worth more than a dependency-free posture Artemis no longer has.

Mitigation: `api` and `terminal` stay dependency-free. The Playwright driver is
fetched only when a scenario actually contains a browser step, so an API-only user
pays nothing.

### Prior art

**Bruno** (API client, collections as plain-text `.bru` files edited by a GUI,
stored in git) is the closest existing system to this design, and is **migrating
off its own DSL to YAML** as of v3.1.0, with v4 shipping a one-click `.bru` → `.yml`
converter. Stated reason: an industry-standard format "enabling seamless
integration with existing tooling and workflows."

That is real evidence and it is treated as such. The decisive detail is that Bru
was a **T0** DSL — blocks of key-value pairs, no expressions, no types, no checking.
It is YAML with braces. Bruno paid the full price of a bespoke format and collected
almost none of the benefit a language offers, so YAML won that trade. Bruno is
evidence against T0, which this design already rejects.

The cost it does prove is **ecosystem tooling**: no highlighting, no language
server, no schema validation, nothing that already understands your files. That is
subsystem E, and it is deferred by decision rather than overlooked. The risk is
recorded.

**tester.army `e2e`** bets the other way on both axes: TypeScript instead of a DSL,
and AI at runtime (`agent.act('upgrade to Pro')`) instead of authoring time, with
resolved agent actions cached and replayed in CI without model calls. It does not
change this design, because API and terminal steps have almost no gap between
intent and mechanics for an agent to close, and because a host language forecloses
transpilation to other languages. Two things are taken from it: **migration as the
adoption path** (they ship eight converter guides; Artemis's Postman importer is
the same move and is currently buried at the bottom of the README), and
**resolve-once-replay-deterministically**, where Artemis's durable artifact is the
tree rather than an action trace.

## The language

File extension `.art`. One or more scenarios per file.

### A step's type comes from its action

Every step contains exactly one **action block**, and that block determines the
step's type. There is no `type:` key.

| Action | Step type |
| --- | --- |
| An HTTP verb: `get`, `post`, `put`, `patch`, `delete`, `head`, `options` | `api` |
| `run "<command>" { ... }` | `terminal` |
| `browser { <action>... }` | `browser` |

This is load-bearing beyond brevity: the checker knows a step's type before it
resolves any name in that step, which is what makes per-type expression scopes
possible.

### Expression scope is per step type

| Step type | Bound roots |
| --- | --- |
| `api` | `status`, `body`, `raw`, `headers` |
| `terminal` | `exit_code`, `stdout`, `stderr` |
| `browser` | `page.url`, `page.title`, and the element functions `text(sel)`, `value(sel)`, `attr(sel, name)`, `count(sel)`, `visible(sel)` |

Plus, in every type, each `var` in the scenario and each `capture` from an earlier
step, by name. Referencing `status` inside a browser step is a compile error naming
the roots that *are* in scope.

`raw` is the response body as unparsed text, which is what makes a regex capture
work against a body that is not JSON.

One builtin everywhere: `env("NAME")`, an ordinary expression legal wherever an
expression is. This removes the wart where `{{env.*}}` resolves only inside a
variable's `value`. An unset name is the empty string, preserving today's
documented behaviour that an absent variable is how a scenario says "no token".

### Worked example

```
scenario "checkout" {
  config browser { headless = true, viewport = "1280x720" }

  var url = env("API_URL")
  var app = env("APP_URL")
  var pw  = env("API_PASSWORD")

  step "seed the database" {
    run "psql" {
      args  = ["-f", "seed.sql"]
      cwd   = "db"
      env   { PGPASSWORD = pw }
    }
    expect exit_code == 0
    expect stdout contains "COPY 42"
  }

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

  step "upgrade in the app" {
    browser {
      goto "${app}/settings/billing"
      fill "#email" = "alice@example.com"
      fill "#password" = pw
      click "text=Sign in"
      click "[data-test=upgrade-pro]"
    }
    expect page.url contains "/billing/confirmed" within "10s"
    expect text("[role=status]") contains "Pro"
    expect visible(".invoice-preview")
    capture invoice = text(".invoice-total")
  }
}
```

### Grammar

```ebnf
File        = { Scenario } ;
Scenario    = "scenario" String "{" { ConfigDecl | VarDecl | StepDecl } "}" ;
ConfigDecl  = "config" Ident "{" [ Setting { Sep Setting } ] "}" ;
Setting     = Ident "=" Expr ;
VarDecl     = "var" Ident "=" Expr ;
StepDecl    = "step" String "{" Action { StepStmt } "}" ;

Action      = Request | Run | Browser ;
Request     = Method String [ "{" { ReqField } "}" ] ;
Method      = "get" | "post" | "put" | "patch" | "delete" | "head" | "options" ;
ReqField    = "header" String "=" Expr
            | "query"  String "=" Expr
            | "body"   "=" Expr ;
Run         = "run" Expr [ "{" { RunField } "}" ] ;
RunField    = "args" "=" Array | "cwd" "=" Expr | "stdin" "=" Expr
            | "env" "{" [ Setting { Sep Setting } ] "}" ;
Browser     = "browser" "{" { BrowserAct } "}" ;
BrowserAct  = "goto"   Expr
            | "click"  Expr
            | "fill"   Expr "=" Expr
            | "select" Expr "=" Expr
            | "press"  Expr
            | "hover"  Expr
            | "upload" Expr "=" Expr
            | "wait"   Expr ;

StepStmt    = Expect | Capture | Retry | Timeout ;
Expect      = "expect" Expr [ "within" String ] ;
Capture     = "capture" Ident "=" Expr ;
Retry       = "retry" "{" [ RetryField { Sep RetryField } ] "}" ;
RetryField  = "times" "=" Int | "delay" "=" String ;
Timeout     = "timeout" "=" String ;
Sep         = "," | newline ;

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
arrays. Statements separate by newline; no semicolons.

`not` binds looser than the `exists` and `is` predicates, so `not body.x exists`
means `not (body.x exists)`, the only reading anyone wants. It binds tighter than
`and` and `or`.

### Waiting, and why it is not retry

`retry { times, delay }` re-runs a whole step. That is right for an API that is not
ready yet and wrong for a browser, where the page is already loading and only one
assertion needs to settle. Browsers need *waiting*, which is per-assertion.

`expect <expr> within "10s"` re-evaluates that single assertion until it holds or
the budget expires. Defaults:

| Step type | Default `within` |
| --- | --- |
| `browser` | 5s |
| `api`, `terminal` | none; evaluated once |

The two compose: a step may retry *and* its assertions may wait. Browser actions
themselves auto-wait, which is Playwright's behaviour and is inherited rather than
reimplemented.

This lowers cleanly: `expect visible(".modal") within "5s"` becomes
`expect(page.locator(".modal")).to_be_visible(timeout=5000)`.

### Sessions

A `browser` step is not stateless. The page persists across steps: step 4 depends
on what step 3 did to the DOM. So a scenario owns **one lazily created browser
session**, opened on its first browser step, shared by all later browser steps, and
closed when the scenario ends, pass or fail.

This is a change to ART-15's seam, which is why it belongs in this document rather
than in B's plan. `Executor.Execute` takes a context carrying a session registry;
the HTTP and terminal executors ignore it. The registry, not the executor, owns
lifetime, so a crashed step cannot leak a browser process.

`config browser { headless, viewport }` configures the session. It is deliberately
tiny; anything else is deferred until a scenario needs it.

### String interpolation

`"${ Expr }"` inside a double-quoted string, with full expressions, so
`"${base}/users/${body.data.id}"` works. `\$` is a literal dollar, which fixes the
current "there is no escape syntax" limitation.

Values render with ART-6's existing `renderValue` rules, unchanged: a number as
written (`42`, not `42.000000`), a boolean as `true`/`false`, null as `null`, an
object or array as compact JSON.

An unclosed `${` and an unknown identifier are both **parse-time** errors. This is
stricter than today, where a missing variable fails the step at run time. That is
the point: the file either compiles or it does not.

### Request bodies

`body = {"username": "alice", "password": pw}` is an object literal serialised to
JSON, not a string with placeholders spliced in. This removes the failure mode
where a captured object reaches a body by text concatenation. A string is still
accepted (`body = "raw text"`) for non-JSON payloads.

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
| `gt` / `gte` / `lt` / `lte` | `>` / `>=` / `<` / `<=` |
| `scripts: [{key, path}]` | `capture k = <expr>` |
| `{{name}}` | `"${name}"` |
| `{{env.NAME}}` | `env("NAME")` |

### One expect is one assertion

Each top-level `expect` produces exactly one assertion in the result tree. An
`and` chain inside a single `expect` is still one assertion. This keeps source line
to reported assertion one-to-one and preserves ART-1's granularity; two assertions
means two `expect` lines.

### Reserved keywords

Reserved now so later tiers need no breaking change. Using one is a parse error
naming what it is reserved for:

`if`, `else`, `for`, `in`, `while`, `parallel`, `group`, `fn`, `return`, `import`,
`use`, `let`, `setup`, `teardown`, `ai`.

`ai` is reserved for a possible future agentic assertion
(`expect ai "the error explains the card was declined"`). **Not in scope.** A CI
gate's most valuable property is determinism, and an assertion that flakes for
unreproducible reasons is worse than a missing one.

## Authoring from a UI

Subsystem D is the builder itself and gets its own spec. What belongs *here* is the
contract it is built against, because three of its four requirements constrain the
language and one of them cannot be retrofitted.

### 1. Lossless round-trip (decide now or never)

A UI loads a file, changes one field, writes it back. Anything the parser discarded
is destroyed at that moment. So the tree is **concrete**: every node carries its
leading and trailing *trivia* (comments, blank lines, spacing), and `print`
reproduces them.

```
parse(src) -> tree -> print(tree) == src     // byte-identical, any valid file
```

A lexer that drops comments cannot be made lossless later without being rewritten,
which is why this is an **A acceptance test**, not a D task.

| Print mode | Behaviour | Used by |
| --- | --- | --- |
| Preserving | Reproduce trivia and layout; only edited nodes re-rendered | UI writes |
| Canonical | Normalise all layout | `artemis fmt -w`, `migrate`, `generate` |

A UI write therefore produces a diff touching only the lines the user changed,
which is what keeps the format reviewable in a pull request.

### 2. The tree as JSON, both directions

```
artemis ast -f checkout.art            # tree as JSON
artemis ast --from-json < tree.json    # JSON back to source
```

Every node carries its span (`file`, `line`, `col`, `endLine`, `endCol`, `offset`).
The encoding is versioned with `schemaVersion`, because the UI ships and upgrades
independently of the CLI. It is the same tree codegen consumes, so UI, interpreter
and transpilers cannot drift.

### 3. Structured diagnostics

```
artemis parse -f checkout.art --json
```

```json
{"diagnostics": [
  {"code": "unknown-field", "severity": "error",
   "span": {"line": 12, "col": 10, "endLine": 12, "endCol": 15},
   "message": "unknown field \"statu\"",
   "hint": "did you mean \"status\"?",
   "suggestions": [{"replace": "status"}]}
]}
```

Stable `code` values matter more than message text: a UI keys behaviour off them,
and rewording a message for clarity must not break it. `suggestions` carries a
mechanical fix so the UI can offer one-click correction.

### 4. Enumerable choice points, with an escape hatch

Forms need finite, discoverable option sets: HTTP methods, browser actions,
comparison operators, type names, retry fields. `artemis grammar --json` emits
them, so dropdowns come from the binary rather than a hardcoded list that drifts.

Expressions are the hard part for a form. Nearly every assertion has the shape
`<path> <op> <literal>`, which is three widgets. The checker labels each `expect`:

- **simple** — `<path> <op> <literal>`, `<path> exists`, `<path> is <type>`, and
  the `not` form of each. Rendered as path picker, operator dropdown, value field.
- **complex** — anything else. Rendered as one raw expression field with live
  validation.

The label is in the JSON encoding, so it is a documented property of the tree
rather than something each client re-derives. Nothing is unrepresentable; complex
expressions degrade to text.

`"${url}/token"` means a UI's URL field contains a small expression language. That
is deliberate: one field with validated interpolation beats a separate
variable-binding widget, and span data lets a bad `${` be underlined precisely.

### Partial and invalid states

A half-built step with no URL is normal in a UI and rejected by a strict parser.
Resolution: **the UI owns incomplete state in its own memory and only ever
serialises a complete tree.** The CLI is never asked to parse a half-written file.
Live validation is the UI calling the checker on a complete-but-wrong tree. This
keeps the on-disk format strict, which `artemis test` depends on.

## Architecture

```
.art source
    |
    v
pkg/dsl      lexer -> parser -> tree (+trivia) -> checker       (diagnostics)
    |
    +--> pkg/dsl/lower  -> runtime scenario --> pkg/executor
    |                                              |  api | terminal | browser
    |                                          pkg/session (browser lifetime)
    |                                              |
    |                                          pkg/result -> pkg/report
    |
    +--> pkg/codegen    -> python | js
    |
    +--> pkg/dsl/print  -> .art source   (preserving | canonical)
    |
    +--> pkg/dsl/encode -> tree as JSON <-> tree     (UI, subsystem D)
```

### Packages

| Package | Responsibility |
| --- | --- |
| `pkg/dsl/token` | token kinds; `Span{File,Line,Col,EndLine,EndCol,Offset}`; trivia |
| `pkg/dsl/lexer` | source to tokens; **retains comments and blank lines as trivia** |
| `pkg/dsl/ast` | node types; every node carries a span and its trivia |
| `pkg/dsl/parser` | recursive descent; precedence climbing for expressions |
| `pkg/dsl/check` | step-type inference, per-type scope resolution, reserved words, arity, simple/complex classification |
| `pkg/dsl/diag` | `Diagnostic{Span,Code,Severity,Message,Hint,Suggestions}`; terminal and JSON rendering |
| `pkg/dsl/print` | tree to source, preserving or canonical |
| `pkg/dsl/encode` | versioned JSON encoding, both directions |
| `pkg/dsl/lower` | tree to the runtime structures `pkg/executor` consumes |
| `pkg/eval` | expression evaluation against a step's observation plus scope |
| `pkg/session` | browser session lifetime, keyed per scenario |
| `pkg/steps/httpstep`, `pkg/steps/execstep`, `pkg/steps/browserstep` | the three executors |
| `pkg/codegen` | `Target` interface; `python`, `js` |

### What changes in existing code

- `pkg/shared.ParseYAMLFile` leaves the run path, surviving only in `migrate`.
- `models.Response.Body []BodyCheck` is replaced by compiled expressions.
  `models.BodyCheck`, `models.Script` and the operator machinery in
  `pkg/shared/assert` are deleted; `pkg/eval` subsumes them.
- `pkg/shared/template.go`'s `{{}}` substituter is deleted. Interpolation is a
  lexer concern. `renderValue`'s rules survive in `pkg/eval`.
- `models.Retry` keeps `Attempts()` and `Wait()`; its `UnmarshalYAML` goes.
- **ART-15's `Executor` gains a context** carrying the session registry.

### Relationship to the in-flight v3 roadmap

| Issue | Effect |
| --- | --- |
| ART-15 (`Executor` interface, registry) | **Amend before merge** to take a context carrying session state. Everything else stands. |
| ART-16 (HTTP step behind the seam, timeouts) | Unaffected. Land as planned. |
| ART-17 (`exec` step type) | Keep the semantics, replace the YAML surface with the `run` action block. |
| ART-18 (`scripts:` to `capture:`) | **Supersede.** It defines a YAML spelling this work immediately replaces. Its semantics (JSON path or regex, type preservation, sorted order, one errored assertion per failed capture) are adopted wholesale; its syntax should not ship. |

## Diagnostics

The bar is a compiler's, not a decoder's.

```
checkout.art:12:10: unknown field "statu"
   12 |   expect statu == 200
      |          ^^^^^
   hint: did you mean "status"?

checkout.art:31:10: "status" is not in scope in a browser step
   31 |   expect status == 200
      |          ^^^^^^
   hint: a browser step binds page.url, page.title, text(), value(),
         attr(), count(), visible()
```

- Spans cover the offending token exactly; the source line is echoed with a caret.
- Unknown identifiers get "did you mean" by edit distance against names in scope.
- **All** errors in a file are reported. Parsing recovers at statement boundaries.
- Nothing panics. A malformed file produces diagnostics and a non-zero exit, never
  a stack trace, continuing the value ART-6 and ART-7 established.

## Commands

| Command | Behaviour |
| --- | --- |
| `artemis test -f x.art` | Parse, check, lower, run. Unchanged reporting and exit codes. |
| `artemis parse -f x.art [--json]` | Parse and check only. No execution. |
| `artemis fmt [-w] x.art` | Canonical formatting. |
| `artemis ast -f x.art` / `--from-json` | Tree as JSON, both directions. |
| `artemis migrate -f old.yaml [-o new.art]` | One-shot YAML to DSL. The only remaining YAML reader. |
| `artemis generate -f postman.json` | Postman to `.art`, onto the same printer. |
| `artemis grammar [--json]` | EBNF, or the enumerable choice points. |
| `artemis build --lang=python\|js [-o dir]` | Transpile. |

## Error handling

Three classes, kept distinct:

1. **Compile errors** (parse, resolve, out-of-scope root, reserved word, unclosed
   interpolation, uncompilable regex, bad duration): diagnostics before anything
   executes, non-zero exit. Strictly larger than today's class by design, since
   unknown variables and bad regexes move from run time to compile time.
2. **Step errors** (transport failure, timeout, command not found, browser crash,
   unrenderable value): the step errors with a reason. Not assertions.
3. **Assertion failures**: the message is generated from the expression and its
   evaluated operands, so `expect body.data.count > 0` reports
   `body.data.count > 0, got 0`. The evaluator retains sub-expression values.

An evaluation that cannot be performed at all against a live observation (absent
path, `>` on an object) remains an *errored assertion* with a reason, matching
current behaviour.

## Codegen

```go
type Target interface {
    Name() string
    Generate(*ast.File) ([]GeneratedFile, error)
}
```

| Target | Output |
| --- | --- |
| `python` | pytest, `requests`, `playwright.sync_api`, `subprocess` |
| `js` | vitest, `fetch`, `@playwright/test`, `child_process` |
| `go` | **reserved**, not implemented |

Lowering rules, identical across targets:

- One scenario becomes one test function; steps are ordered statements inside it.
- `capture` becomes a local variable.
- `expect` becomes the target's native assertion; `within` becomes its timeout
  argument.
- `retry` becomes a small helper emitted once per file.
- `env()` becomes the target's environment lookup.
- A browser session becomes the target's Playwright page fixture.

Generated code is a **one-way export**. Artemis does not re-import it, and the
output header says so.

## Testing

| Layer | Approach |
| --- | --- |
| Lexer/parser | Unit tests including exact span assertions |
| Diagnostics | A corpus of invalid files, each with full expected diagnostic text as a golden. Written first; this is the feature. |
| Formatter | Canonical printing is idempotent |
| **Lossless round trip** | `print(parse(src)) == src` byte for byte in preserving mode, over the fixture corpus and fuzz-generated valid inputs |
| Tree JSON | `decode(encode(tree))` equals the original; schema snapshotted as a golden so an unintended change to the UI contract fails CI |
| Evaluator | Table-driven over every operator and type combination, including error cases |
| **Behaviour parity** | The seven existing `pkg/cli/testdata` fixtures migrated to `.art` must reproduce their `.golden` outputs byte for byte, except the two that change by design (below). The strongest available proof the rewrite preserved behaviour. |
| Browser | A local fixture server plus headless Playwright; no public sites in CI |
| Codegen | Golden files per target, plus an execution-parity test for Python: run the generated pytest against the same fixture server and assert the same pass/fail. Skipped with a note if `python3` is absent, matching how `make lint` handles a missing linter. |
| README | `readme_test.go` retargeted: every fenced `art` block parses, every whole scenario checks |
| Fuzz | Native Go fuzzing on the parser. No input may panic. |

**Two goldens change by design.** `bad_capture` and `template_error` currently pin
run-time failure for an empty capture path and an unresolvable placeholder. Both
become compile errors. Every other golden must be byte-identical.

## Risks

| Risk | Mitigation |
| --- | --- |
| Largest change Artemis has had, across five subsystems | Sequenced A → B → C → D, each independently shippable; separate plans |
| Trivia skipped under A time pressure, making D impossible without a parser rewrite | Lossless round trip is an A acceptance test, not a D task. The lexer retains trivia from its first commit. |
| **Deferred editor tooling (E) repeats Bruno's failure** | Accepted deliberately. The prerequisites (spans, codes, checker, formatter) are built in A, so E is an adapter whenever it is funded. Revisit if adoption stalls on authoring friction. |
| Browser dependency undermines the single-binary story | Driver fetched only when a scenario contains a browser step; `api` and `terminal` stay dependency-free |
| Browser tests flake | Playwright auto-waiting; `within` on assertions; local fixture server only in CI |
| Codegen backends drift from the interpreter | Shared conformance corpus; Python execution-parity test; Playwright on all three sides so browser semantics are identical by construction |
| Diagnostics under-built, forfeiting the main win | Diagnostic goldens written before the parser is finished |
| Tree JSON churn breaking a released UI | `schemaVersion`; schema golden in CI; `code` values stable independently of message text |
| A model has never seen this syntax | Small surface; `artemis grammar` prints the complete EBNF; HCL-shaped so a wrong guess is close |

## Milestones

**Subsystem A**

1. **A1 Front end.** Lexer with trivia, parser, tree, checker with per-type scopes,
   diagnostics, printer in both modes. `artemis parse` and `artemis fmt`. Lossless
   round trip is an acceptance test. Nothing executes.
2. **A2 Execution parity.** Lowering, evaluator, `api` and `terminal` steps,
   `artemis test`. Migrated fixtures reproduce the existing goldens.
3. **A3 Migration and docs.** `artemis migrate`, Postman `generate` retargeted,
   YAML out of the run path, README rewritten.
4. **A4 Machine contract.** `pkg/dsl/encode`, `artemis ast` both ways, `--json`
   diagnostics, `artemis grammar --json`, schema goldens.

**Subsystem B** — session registry, `browser` action block, `within`, playwright-go
executor, fixture server, flake controls.

**Subsystem C** — Python target, then JavaScript. Execution parity for Python.

**Subsystem D** — the UI builder, against A4's contract. Separate spec.

**Subsystem E** — deferred.

## Non-goals

- Control flow of any kind: `if`, loops, data-driven tables, `parallel` (T2).
- Functions, imports, fixtures, setup/teardown (T3). Reuse is what the host
  language is for, and the eject path answers "I need real abstraction".
- Agentic assertions (`ai`). Reserved, not implemented.
- YAML as a runtime format. It survives only as a migration input.
- Re-importing generated code.
- A Go codegen target. Reserved, not built.
- Editor tooling (LSP, tree-sitter, VS Code). Deferred by decision.
- Mobile app testing. Not in scope at any tier.
- Machine-readable report formats (JUnit, JSON report). Still open, still unrelated.
