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

## Install

On macOS, Apple Silicon or Intel:

```sh
curl -fsSL https://raw.githubusercontent.com/Av3nger123/artemis/master/install.sh | sh
artemis --version
```

It installs the latest release to `/usr/local/bin`, after checking it against
the release's checksums; `ARTEMIS_VERSION=v0.1.0` picks another release and
`ARTEMIS_INSTALL_DIR` another directory. Run it again to upgrade. On anything
else, or from a clone, build it with Go: `make build-full`.

The first `browser` step downloads Chromium, about 683 MB, once.

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

```sh
artemis run checkout.art
```

`var` is a value the steps can name, the action block -- `post "..." { ... }` --
is what the step does, `expect` is one assertion and one line of the report,
and `capture` keeps a value for the steps after it. A failure names the file and
line, the scenario, the step, what was expected and what came back, and the run
exits non-zero. [Writing scenarios](docs/scenarios.md) goes through it properly.

## What it can check

- **HTTP APIs** -- requests, headers, bodies, forms and file uploads, with a
  cookie jar per scenario. [Writing scenarios](docs/scenarios.md)
- **Commands** -- a CLI, a migration script, a release gate: exit code, stdout
  and stderr. [Checking a command](docs/terminal.md)
- **Web pages** -- `browser` steps on Playwright, with per-assertion waiting and
  a screenshot of any page that failed. [SPEC.md](SPEC.md#browser-steps)
- **Databases** -- PostgreSQL, MySQL, SQLite, SQL Server and Oracle with SQL,
  MongoDB and Redis with their own commands, to check what an API actually saved.
  [Checking the database](docs/databases.md)

## Features

- Scenarios in a real language: lexed, parsed and name-checked before anything runs, with compiler-grade diagnostics -- the source line, a caret, and `did you mean "status"?`
- Assertions as expressions: `==`, `!=`, `>`, `>=`, `<`, `<=`, `contains`, `matches`, `exists`, `is <type>`, `not`, `and`, `or`
- Values captured from one step and used in the next, keeping their JSON type
- [Collections](docs/scenarios.md#reusing-requests): write a request once, `import` it, and `use` it with arguments, overrides and different expects; `artemis expand` shows what will run
- A `suite.art` per folder: one login, once per run, for every scenario below it; `setup` and `teardown` in a scenario, teardown always running
- [Blocks](docs/blocks.md) inside a scenario: `parallel`, `if`/`else`, `switch`, `try`/`catch`/`finally`, and `repeat ... until` for polling
- [Selection](docs/suites.md#running-part-of-a-suite) by tag and name, and whole files run at once with [`--jobs`](docs/suites.md#running-a-suite-faster)
- Data-driven scenarios, one per row of a JSON or CSV file, and test data made up where it is needed -- `uuid()`, `now()`, `random_string()` -- [reproducible with `--seed`](docs/suites.md#reproducing-random-test-data)
- [`secret` values](docs/environment.md#keeping-a-credential-out-of-a-report) withheld from every report and trace
- [Reports](docs/reports.md) for programs: `--report json`, `--report junit`, `--events ndjson` as it happens, and `--trace` for what each step sent and got back
- [An MCP server](docs/mcp.md), so an agent writes scenarios against the real grammar and checker
- [One-shot conversion](docs/migrating.md) from a Postman collection or an old YAML scenario
- A canonical formatter, a syntax tree as JSON in both directions, and the grammar as text or as machine-readable choice points -- [the commands](docs/cli.md)

## Documentation

| | |
| --- | --- |
| [Writing scenarios](docs/scenarios.md) | A scenario step by step, and reusing requests through collections |
| [Commands](docs/cli.md) | `run`, `parse`, `fmt`, `ast`, `expand`, `grammar`, and reading a failure |
| [Reports, traces and logs](docs/reports.md) | JSON and JUnit reports, the event stream, screenshots, logs, traces |
| [Environment and secrets](docs/environment.md) | `env()`, `.env` files, and keeping a credential out of a report |
| [Running suites](docs/suites.md) | Running in parallel, selecting by tag or name, reproducing random data |
| [Blocks](docs/blocks.md) | Parallel steps, branches, try/catch and polling |
| [Commands as tests](docs/terminal.md) | `terminal` steps |
| [Databases](docs/databases.md) | `db` steps |
| [The MCP server](docs/mcp.md) | Authoring scenarios from an agent |
| [Coming from Postman or YAML](docs/migrating.md) | `artemis generate` and `artemis migrate` |
| [SPEC.md](SPEC.md) | The language reference: every field, rule and builtin |
| [ROADMAP.md](ROADMAP.md) | What is done, what is held back, and the ideas |

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) has the make targets, the tests and how a
release is cut. To report a vulnerability, see [SECURITY.md](SECURITY.md).

## License

MIT -- see [LICENSE](LICENSE). Each release tarball also carries the licenses of
the modules compiled into it, in `THIRD_PARTY_LICENSES/`.
