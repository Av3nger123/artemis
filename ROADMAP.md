# Roadmap

What is being built, what is next, and the ideas not yet committed to. What the
language deliberately leaves out is in `SPEC.md`, under **Not in this spec**; this
file is for what is coming.

## Done

Designed together on 2026-10-26 and built as eight PRs: a builtin table shared
by the checker and the evaluator first, with no change in
behaviour; then the value builtins, cookies and timing, files, data rows, setup
and teardown, suites, and selection last, because it reads tags from suites and
names from data rows. `SPEC.md` documents each.

1. **Data sources and files.** `json()`, `csv()` and `file()`; `form` and
   `multipart` bodies, a file as a body, as `stdin` and in `upload`; and
   data-driven scenarios, `scenario "..." for row in json("rows.json")`, expanded
   into one scenario per row before the checker runs.
2. **Setup, teardown and suites.** `setup { }` and `teardown { }` in a scenario,
   teardown always running; a `suite.art` per folder whose setup runs once and
   whose values every scenario below it reads as `suite.<name>`.
3. **Builtins, cookies and timing.** One builtin table for the checker, the
   evaluator; `uuid()`, `now()`, `random_*()`, encoding and
   hashing builtins, reproducible with `--seed`; a cookie jar per scenario and a
   `cookies` root; `duration_ms` on every step.
4. **Selection.** `tags = [...]`, `--tags "smoke and not slow"`, `--grep`, and
   `artemis list`. The environment check covers only what was selected.
5. **`db` steps.** `db <url> { sql, args }` against PostgreSQL, MySQL, SQLite,
   SQL Server and Oracle, and `db <url> { command }` against MongoDB and Redis,
   binding `rows`, `affected` and `result`; one pool per URL per run. A default
   build carries the PostgreSQL, MySQL and SQLite drivers and `make build-full`
   every one. CI runs the server tests against all six in service containers.
6. **Blocks.** `parallel`, `if`/`else`, `switch`, `try`/`catch`/`finally` and
   a bounded `repeat ... until` for polling, anywhere a step can be written;
   the checker holds each branch to the captures it can rely on, and the report
   stays in source order whatever order parallel branches finished in.

## Not yet

Held back on purpose, with the word for each reserved so it can arrive without a
breaking change.

- **Functions and loops over a list.** `fn`, `return`, `group` and `while` are
  reserved words, not features. Reusing a request is a
  [collection](docs/scenarios.md#reusing-requests), running a scenario per row of
  data is a `for` clause on the scenario, and polling is
  [`repeat ... until`](docs/blocks.md) -- all of them expand or lower into plain
  steps the checker can see through.
- **Agentic assertions.** `ai` is reserved and will not parse. A CI gate's most
  valuable property is determinism, and an assertion that flakes for
  unreproducible reasons is worse than a missing one.
- **Request and response detail in a report.** The report stays a verdict.
  `--trace` writes what each step sent and got back to a file per step instead
  -- see [Tracing a run](docs/reports.md#tracing-a-run).

## Removed

**`artemis build`**, on 2026-10-26, before any release. It exported a `.art`
file as pytest or vitest, held to `artemis run` by a conformance corpus run
three ways in CI. That cost every language feature three implementations --
suites and `db` steps were never exported for that reason -- and a Python and
Node toolchain in CI, for something an AI assistant does well on request:
translating a scenario into the test framework a team already uses. `artemis
run` is how a `.art` file runs.

## Ideas

Not designed, not committed to. Each says what it would take and what it is
worth, so the decision can be made later with the reasoning in hand.

### The workflow editor in Forge

A scenario's blocks -- parallel, if, switch, try, repeat -- are a graph the
compiler already builds. What is left is drawing it: an `artemis graph x.art`
that prints it as mermaid, dot or JSON, and a workflow editor in Forge that
renders the JSON and edits the blocks back through `artemis ast --from-json`, so
the `.art` file stays the source of truth. A block can only nest, so a step that
waits on one parallel branch and not its sibling cannot be written; an `after`
clause on a step would be the escape hatch if that turns out to be needed.

### Compile cache

Skip compiling `.art` files that have not changed since the last run.

Measured on 2026-10-26: compiling and lowering a 71-line scenario takes about
0.12 ms, so a thousand files cost about 120 ms -- less than one HTTP round trip,
and far less than starting a browser. A cache of the compiled form would have to
be read back and decoded, which costs about what parsing does, so for `artemis
run` this saves almost nothing today. Its key would have to cover the file, every
collection it imports, every data file it reads, every `suite.art` above it and
the artemis version, so it also adds a way to run something stale.

Where a cache does pay:

- **A long-lived process.** Forge and `artemis mcp` re-check files on every edit;
  an in-memory table of compiled units keyed by a hash of those inputs makes an
  edit re-check only what it touched.
- **Large data files.** A `for row in` over ten thousand rows expands into ten
  thousand scenarios; caching the expansion per data-file hash is where compile
  time would first become visible.
- **Skipping execution, not compilation.** The bigger saving is running only the
  scenarios whose inputs changed since the last green run (`--changed`). The same
  hash of a scenario's inputs serves both, so it is worth building once, for this.
