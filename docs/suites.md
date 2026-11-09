# Running a folder

A folder of scenario files is one run. This page is about running every test
in a folder, making a large run fast, running part of it by tag or name, and
drawing the same random test data again when a run fails.

## A folder of tests

A folder holds all three kinds of file, usually each in its own folder:

```text
tests/
  collections/auth.art      # the items, written once
  flows/checkout.art        # the steps
  flows/refund.art
  scenarios/checkout.art    # the data: flow "../flows/checkout.art"
  scenarios/refund.art
```

```sh
artemis run tests/
```

`run` walks the folder for `.art` files, in path order, and runs each scenario
file it finds: every scenario in it is one run of its flow. A flow file or a
collection file in the folder is skipped, not an error -- only scenario files
run -- so the folder can hold the whole test. Naming a flow file on its own,
`artemis run tests/flows/checkout.art`, is the one case that is an error, and
its hint names the scenario files that run that flow.

Nothing is shared between scenarios, or between files: each scenario starts
with its flow's vars and its own values, and what one writes is invisible to
the next. What one setup does for every scenario is a `setup` in the flow,
which runs once for each scenario that runs it.

## Running a folder faster

A run that waits on a service spends nearly all of its time waiting. Separate
scenario files share nothing -- no values, no context -- so they can wait at the
same time:

```sh
artemis run tests/ --jobs 6      # six files at once
artemis run tests/               # one at a time, the default
```

Six files against a two-second endpoint: 14.6s at one job, 3.3s at six.

This is the runner's doing, not the language's. A flow's steps are still
ordered -- a step reads what the steps above it wrote -- and steps inside one
flow run at the same time only in a [`parallel`](blocks.md) block.

**The report is unaffected.** A scenario keeps the place its file had in
discovery order, whenever that file happened to finish, so goldens and `jq`
expressions do not move.

**The console changes.** With more than one job each file's output is held and
printed when its turn arrives, rather than a step at a time as it happens:
two files writing to one terminal at once is unreadable. One job still streams
live, which is why it is the default.

One is also the default because the limit here is the service, not the machine.
A run pointed at a staging API will meet a rate limit long before it saturates
a laptop, so the number is yours to choose.

## Running part of a folder

Tag scenarios -- `tags = ["smoke", "payments"]` in a scenario -- and select by
tag, by title, or both, across every scenario file in the folder:

```art
# scenarios/checkout.art
flow "../flows/checkout.art"

scenario full_price as "full price" {
  tags = ["smoke", "payments"]
}

scenario a_big_order as "a big order" {
  tags     = ["payments", "slow"]
  quantity = 500
}
```

```art
# flows/checkout.art
flow checkout {
  var quantity = 1
  private var url = env("API_URL")

  step pay {
    post "${url}/cart/pay" { body = {"sku": "A-1", "quantity": quantity} }
    expect status == 200
  }
}
```

```sh
artemis run tests/ --tags "smoke and not slow"
artemis run tests/ --grep "^login as"
artemis run tests/scenarios/checkout.art --grep "big"   # one file, by title
artemis list tests/ --tags payments                     # what would run, running nothing
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
generator, seeded from the run's seed, the scenario's title and its file's path
within the folder, so a failed run says how to draw the same values again:

```
Seed        8817265503411  (rerun with --seed 8817265503411 for the same values)
FAIL in 1.2s
```

```sh
artemis run tests/ --seed 8817265503411
```

The seed is in the JSON report as `seed`, and only when a scenario drew a random
value. The rest of the value builtins -- `now()`, `base64()`, `sha256()`,
`hmac_sha256()`, `jwt_claims()`, `to_json()`, `len()` and the others -- are in
[SPEC.md](../SPEC.md#making-and-converting-values).
