# Environment and secrets

Where a test's values come from when they are not written in the file -- the
environment and `.env` files -- and what a run prints of a credential.

## Environment variables

`env("NAME")` and `env("NAME", "default")` are ordinary expressions and are
legal wherever an expression is: in a flow's `var`, in a scenario's value, in
a URL, in a header, in a `body`, in an `expect`. A variable that is not set, that is empty, or that
holds only space characters has no value; with no default, that is an error
that artemis reports before the run's first step, naming every such variable
at once.

**This changed.** `env()` on a variable with no value used to give the empty
string, so `get "${url}/orders"` quietly requested `/orders` and the run
reported a refused connection or somebody else's 404 instead of the absent
variable behind it. It now fails. A test that wants the old behaviour for one
variable writes `env("NAME", "")`, which asks for an empty value in a way the
file shows.

```art
# flows/sign_in.art
flow sign_in {
  var user = "alice"
  private var url      = env("API_URL")
  private var password = env("API_PASSWORD")

  step sign_in as "sign in" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"username": user, "password": password}
    }
    expect status == 200
  }
}
```

```art
# scenarios/sign_in.art
flow "../flows/sign_in.art"

scenario as_alice as "as alice" {}
scenario as_the_smoke_user as "as the smoke user" { user = env("SMOKE_USER", "alice") }
```

Names come from the process environment and from the `.env` file in the working
directory, or from the file given with `-e`:

```dotenv
API_URL=https://localhost:8000
API_PASSWORD=my_secret_key
```

```sh
artemis run checkout.art -e dev.env
```

If the file named with `-e` cannot be read, the run stops with an error that
names the file, before any step. A missing default `.env` is silent.

An environment file supplies `env("...")` and nothing else. It does not set a
flow's vars by name: a var that should come from the environment says so,
`env("NAME")`, in the flow or in a scenario.

**Remember not to commit your environment files to version control systems like
Git, as they may contain sensitive information.**

## Credentials in a report

A report prints both operands of a failed assertion, so `expect password ==
"..."` against a password prints the password -- into the terminal and into
`report.json`, which a CI job often keeps as an artifact -- and a trace
records what each step sent, a password in a body included. Nothing in the
language marks a value as a credential: masking one in reports without
marking it is an open item on [the roadmap](../ROADMAP.md), and until it is
built, a report withholds no value of yours.

What a run does withhold: a trace writes `***` for every cookie's value and
for the password in a `db` step's connection URL. Keep `private` the vars that
hold a credential, so a scenario file never sets one to a value written out,
and read it with `env()` from an environment file you keep out of Git.
