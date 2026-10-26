# Running suites

A folder of scenarios is one run. This page is about making a large one fast,
running part of it, and drawing the same random test data again when a run
fails.

## Running a suite faster

A suite that waits on a service spends nearly all of its time waiting. Separate
scenario files share nothing -- no variables, no captures, no scope -- so they can
wait at the same time:

```sh
artemis run suite/ --jobs 6      # six files at once
artemis run suite/               # one at a time, the default
```

Six files against a two-second endpoint: 14.6s at one job, 3.3s at six.

This is the runner's doing, not the language's. A scenario's steps are still
ordered -- a step reads what the steps above it captured -- and steps inside
one scenario run at the same time only in a [`parallel`](blocks.md) block.

**The report is unaffected.** A scenario keeps the place its file had in
discovery order, whenever that file happened to finish, so goldens and `jq`
expressions do not move.

**The console changes.** With more than one job each file's output is held and
printed when its turn arrives, rather than a step at a time as it happens:
two files writing to one terminal at once is unreadable. One job still streams
live, which is why it is the default.

One is also the default because the limit here is the service, not the machine.
A suite pointed at a staging API will meet a rate limit long before it saturates
a laptop, so the number is yours to choose.

## Running part of a suite

Tag scenarios -- `tags = ["smoke", "payments"]` in a scenario, or in a
`suite.art` for its whole folder -- and select by tag, by name, or both:

```sh
artemis run suite/ --tags "smoke and not slow"
artemis run suite/ --grep "^login as"
artemis list suite/ --tags payments        # what would run, running nothing
```

What is not selected is skipped in the report and left off the console, and
only what is selected has its `env()` variables checked: a smoke job needs the
smoke tests' variables and nobody else's. A selection of nothing fails the run,
and a tag no scenario has is a warning that names the nearest one -- `--tags
smok` does not quietly run nothing. `artemis list --json` writes the selection
as one document.

## Reproducing random test data

`uuid()`, `random_int()` and `random_string()` make test data up, so a sign-up
test does not collide with the last run's user. Every scenario draws from its own
generator, seeded from the run's seed, the scenario's name and its file's path
within the suite, so a failed run says how to draw the same values again:

```
Seed        8817265503411  (rerun with --seed 8817265503411 for the same values)
FAIL in 1.2s
```

```sh
artemis run suite/ --seed 8817265503411
```

The seed is in the JSON report as `seed`, and only when a scenario drew a random
value. The rest of the value builtins -- `now()`, `base64()`, `sha256()`,
`hmac_sha256()`, `jwt_claims()`, `to_json()`, `len()` and the others -- are in
[SPEC.md](../SPEC.md#making-and-converting-values).
