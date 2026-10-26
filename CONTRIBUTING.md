# Contributing

Artemis is a Go module. You need the Go version in `go.mod`'s `toolchain` line;
an older Go downloads it on first use.

```sh
make test        # go test ./...
make test-race   # what CI runs: -race, and every database driver
make vet         # go vet ./...
make lint        # golangci-lint, skipped with a note if it is not installed
make cover       # coverage profile plus the total
make build       # the artemis binary: PostgreSQL, MySQL and SQLite only
make build-full  # with every database driver, as a release is built
make release     # the macOS release tarballs in dist/, as a v* tag publishes them
make all         # vet, lint, test, build
```

`make lint` needs golangci-lint, pinned to the version `.golangci.yml` is written for:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
```

It is skipped -- with a note, not an error -- when the linter is not on your PATH, so a missing tool never blocks `make build`. Use `make lint-strict` if you want it to fail instead.

## Database and browser tests

`pkg/steps/dbstep`'s server tests run against a real database when its
`ARTEMIS_TEST_*_URL` variable is set -- `ARTEMIS_TEST_POSTGRES_URL`,
`_MYSQL_URL`, `_SQLSERVER_URL`, `_ORACLE_URL`, `_MONGO_URL`, `_REDIS_URL` -- and
skip when it is not. `.github/workflows/ci.yml` starts all six as service
containers; the URLs there work against the same images run locally with
Docker.

The browser tests need Chromium, about 683 MB on the first run, so they are
behind a build tag and not part of `make test` or CI:

```sh
make test-browser         # once
make test-browser-repeat  # ten times in a row, to prove they are not flaky
```

## Golden-file tests

`pkg/cli/testdata/art` holds whole runs: `<case>.art` is a scenario a user could
have written, `<case>.golden` is every byte artemis printed for it plus the error
it exited with. A fixture writes `%SERVER%` where the test's HTTP server goes,
and the temp path, the server's port and every duration are normalised before
comparison, so the files are stable across machines.

When a change to the report or the runner is deliberate, regenerate them and read the diff:

```sh
make golden      # every golden file, in every package that has them
git diff '*/testdata/*'
```

## The documentation tests

`pkg/shared` compiles the examples in the README, in every guide under `docs/`,
and in SPEC.md. Every fenced `art` block goes through the real front end --
`pkg/dsl/front`, the same one the runner uses -- and every block that is a
whole scenario has to name-check as well as parse: types inferred, every name
resolvable, every field known. A fragment is wrapped in the part of a scenario
its prose puts it in and has to parse there. A block that imports compiles
against the collection files SPEC.md documents -- the blocks whose first line is
`# collections/<name>.art` -- served from memory, so an example's imports and
the collections it imports cannot drift apart.

So an example in any of them that artemis would reject is a failing test, not a
surprise for whoever copies it. The tests also hold the prose to the code:
SPEC.md's type names against `token.TypeNames`, its operator table against what
`artemis migrate` can translate, and its report schema against
`pkg/cli/testdata/art/report_json.golden`. Every relative link between the
documents has to lead to a file that exists, and an anchor to a heading in it.

The one fenced `yaml` block is `artemis migrate`'s documented input, in
[docs/migrating.md](docs/migrating.md), and it is loaded through
`migrate.ParseYAMLFile` -- the same call the command makes -- so it stays an
input the command can read.

## CI

`.github/workflows/ci.yml` runs on every push and pull request:

- **build, vet, test** -- both builds, `go vet`, a gofmt check, and
  `go test -race -tags db_all` against the six database containers.
- **golangci-lint** -- with every database driver's build tag.
- **govulncheck** -- fails on a known vulnerability the code calls, in the Go
  release or in a module.

`master` takes changes through a pull request whose three checks pass. Each is a
make target or one command, so a red build is reproducible locally.

## Releasing

A `v*` tag on `master` is a release:

```sh
git tag v0.2.0 && git push origin v0.2.0
```

`.github/workflows/release.yml` runs the tests and govulncheck again, builds the
macOS tarballs with `make release`, attests their provenance, and publishes them
as a GitHub release, where `install.sh` finds the latest one. Only maintainers
can push a `v*` tag.
