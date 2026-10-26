# Roadmap

What is being built, what is next, and the ideas not yet committed to. What the
language deliberately leaves out is in `SPEC.md`, under **Not in this spec**; this
file is for what is coming.

## Done

Designed together on 2026-10-26 and built as eight PRs: a builtin table shared
by the checker, the evaluator and code generation first, with no change in
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
   evaluator and code generation; `uuid()`, `now()`, `random_*()`, encoding and
   hashing builtins, reproducible with `--seed`; a cookie jar per scenario and a
   `cookies` root; `duration_ms` on every step.
4. **Selection.** `tags = [...]`, `--tags "smoke and not slow"`, `--grep`, and
   `artemis list`. The environment check covers only what was selected.

## Next

### Suites in `artemis build`

`artemis build` exports one file's scenarios, and a `suite.art` runs once for a
whole folder, so a scenario that reads `suite.<name>` is refused today rather
than exported with nothing behind the name. The export is a pytest
`conftest.py` with a session-scoped fixture per suite and a vitest
`globalSetup`, built from the same suite file; what it needs from `build` is
an output that is a directory of modules rather than one.

### `db` steps, the rest

Phase one shipped `db <url> { sql, args }` against PostgreSQL, MySQL and SQLite,
with `rows` and `affected`, a pool per URL per run, and traces that never print
a password; CI runs the server tests against PostgreSQL and MySQL service
containers. What is left, in order:

1. **SQL Server and Oracle.** A dialect row each in `pkg/steps/dbstep` (pure-Go
   drivers, so the binary stays static), plus `OUTPUT` in the query classifier.
2. **MongoDB and Redis.** A `command` field -- a command document for Mongo, an
   argv for Redis -- and a `result` root for the reply; Mongo's cursor fills
   `rows`.
3. **`artemis build`.** Export db steps to pytest and vitest through an
   `art_db(url)` helper that picks a client by scheme, held to the interpreter by
   a conformance file: SQLite in-process, the others against service containers.

## Ideas

Not designed, not committed to. Each says what it would take and what it is
worth, so the decision can be made later with the reasoning in hand.

### Graph workflows

Scenarios as nodes and their dependencies as edges, instead of one ordered list
per file: a graph that runs independent branches at once, fans out from one
setup into many checks, and routes on what an earlier node produced.

Authored as code first -- `.art` stays the source of truth, and a graph is
something the compiler builds from it -- with a visual workflow editor in Forge
that reads and writes the same files, the way `artemis ast` already turns a file
into a tree and back.

Two places a graph could live, and they are not equally cheap:

- **Between scenarios.** Suites (item 2 above) are the first edge: a suite's
  setup runs before every scenario under it and its teardown after. Letting a
  scenario name the scenarios it depends on generalises that into a graph, and
  the scheduler that runs it is `--jobs` with ordering. This is where to start.
- **Inside a scenario.** Most of a scenario's graph already exists implicitly: a
  step that reads another's capture depends on it. What the captures do not show
  is a side effect -- a `post` followed by a `get` of what it created -- so
  running steps in parallel needs those edges written by hand, and costs the
  property the language protects today: a run that does the same thing every time
  and a failure that reads top to bottom. Worth doing only after the scenario-level
  graph has shown what people actually need.

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
