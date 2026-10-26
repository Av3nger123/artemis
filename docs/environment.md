# Environment and secrets

Where a scenario's values come from when they are not written in the file --
the environment and `.env` files -- and how to keep a credential out of
everything a run prints.

## Environment variables

`env("NAME")` and `env("NAME", "default")` are ordinary expressions and are
legal wherever an expression is: in a `var`, in a URL, in a header, in a
`body`, in an `expect`. A variable that is not set, that is empty, or that
holds only space characters has no value; with no default, that is an error
that artemis reports before the run's first step, naming every such variable
at once.

**This changed.** `env()` on a variable with no value used to give the empty
string, so `get "${url}/orders"` quietly requested `/orders` and the run
reported a refused connection or somebody else's 404 instead of the absent
variable behind it. It now fails. A scenario that wants the old behaviour for
one variable writes `env("NAME", "")`, which asks for an empty value in a way
the file shows.

```art
scenario "staging" {
  var url    = env("API_URL")
  var secret = env("API_PASSWORD")

  step "sign in" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"secret": secret}
    }
    expect status == 200
  }
}
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

**Remember not to commit your environment files to version control systems like
Git, as they may contain sensitive information.**

## Keeping a credential out of a report

A report prints both operands of a failed assertion, so `expect pw == "..."`
against a password would print the password -- into the terminal and into
`report.json`, which a CI job often keeps as an artifact. Mark the binding
`secret` and it does not:

```art
scenario "checkout" {
  secret var pw = env("API_PASSWORD")
  var url = env("API_URL")

  step "get a token" {
    post "${url}/token" {
      body = {"username": "alice", "password": pw}
    }
    expect status == 200
    secret capture token = body.data.access_token
  }

  step "list the orders" {
    get "${url}/orders" {
      header "Authorization" = "Bearer ${token}"
    }
    expect status == 200
  }
}
```

`secret` goes before `var` and before `capture`. A captured token needs it as
much as an environment variable does, which is why both take it.

The marking spreads on its own. The `Authorization` header above is withheld
because it interpolates `token`, and the same holds through a call, a
comparison, an object field and an array element -- so a value derived from a
credential does not escape by being rewritten. A `capture` whose expression
reads a secret binding is secret without the word.

Three things it does not do. It does not change the request: the same bytes go
out either way, and `secret` is a statement about reports alone. It does not
take the name away -- `var secret = ...` still parses, because the word means
the modifier only in front of `var` and `capture`. And it is not a guarantee
about anything but a report: an environment file is still yours to keep out of
Git.

See [SPEC.md](../SPEC.md) for exactly how much of a body is withheld when one
field of it is secret.
