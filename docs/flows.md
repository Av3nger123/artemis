# Writing flows and scenarios

A test is a **flow**, a `.art` file of steps and what to expect of each one,
and a **scenario file**, the data to run it with. This page walks through
both, then shows how a request written once is reused across flows.
[SPEC.md](../SPEC.md) is the full reference for the language.

## A flow

```art
# flows/checkout.art
flow checkout {
  var user = "alice"
  var min_orders = 1
  private var url = env("API_URL")

  step get_a_token as "get a token" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"username": user, "password": env("API_PASSWORD")}
    }
    expect status == 200
    var token = body.data.access_token
  }

  step list_the_orders as "list the orders" {
    get "${url}/orders" {
      header "Authorization" = "Bearer ${token}"
      query "limit" = 10
    }
    timeout = "5s"
    retry { times = 3, delay = "2s" }

    expect status == 200
    expect body.data.count >= min_orders
    expect body.data.email matches /.+@.+/
    expect body.data.roles contains "admin"
    expect body.data.token exists
  }
}
```

Six things in that file are the whole language:

| | |
| --- | --- |
| `var` at the top | A parameter of the flow: a value the steps can name, evaluated once before the first step runs, which a scenario can set. `var x` with no value is one every scenario must set. `env("NAME")` reads the environment, or the `.env` file |
| `private var` | A value the steps can name and no scenario can set |
| the action block | `post "..." { ... }` -- one per step, and it is what gives the step its type. There is no `type:` key |
| `expect` | One assertion, written as an expression. One `expect` line is one assertion in the report, so the line you wrote and the failure you read about are one to one |
| `var` after the action | A value pulled out of what the step produced, available by name in every later step. A later step that writes the same name replaces it: the last write wins |
| `retry`, `timeout`, `within` | Three different clocks: retry the whole step, bound one attempt, or wait for one assertion to come true |

A flow does not run on its own: `artemis run flows/checkout.art` is an error
whose hint names the scenario files that run it.

## Its scenarios

```art
# scenarios/checkout.art
flow "../flows/checkout.art"

scenario alice_has_orders as "alice has orders" {}

scenario an_admin_s_orders as "an admin's orders" {
  tags = ["smoke"]
  user = "admin"
}

scenario a_new_user_has_none as "a new user has none" {
  user       = "newbie"
  min_orders = 0
}
```

Run it:

```sh
artemis run scenarios/checkout.art
```

The first line names the flow, relative to the scenario file. Each scenario is
one run of the flow, under its own id and label, with the values it sets and the
flow's defaults for the rest. A scenario holds `name = value` lines and `tags`,
and nothing else: a step or an `expect` in one is a compile error, because a
scenario changes the data and never the steps. A name the flow has no var for,
or one it keeps `private`, is an error with the names it does have.

A scenario can turn checks off with `skip`:

```art
# scenarios/any_orders.art
flow "../flows/checkout.art"

scenario whatever_alice_has as "whatever alice has" {
  min_orders = skip
}
```

Every `expect` that reads `min_orders` is removed for that scenario, so the
second step checks the status, the email, the roles and the token, and not the
count. A skipped var read anywhere other than an `expect` or an object field --
a URL, say -- is a compile error.

A scenario has no routing of its own. When different data should take
different steps, the flow says so with an `if` or a `switch` on the var, and
covers every case its scenarios can cause -- see [Blocks](blocks.md).

## Reusing requests

A request, a command or a query written once lives in a **collection**, and a
step calls it by name:

```art
# collections/auth.art
collection auth {

  request login {
    var url
    var user = "alice"
    var password

    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"username": user, "password": password}
    }
    expect status == 200
    var token = body.data.access_token
  }
}
```

```art
# flows/orders.art
import "../collections/auth.art"

flow orders {
  var user = "alice"
  private var url = env("API_URL")

  step log_in as "log in" = auth.login { password = env("API_PASSWORD") }

  step list_the_orders as "list the orders" {
    get "${url}/orders" {
      header "Authorization" = "Bearer ${token}"
    }
    expect status == 200
  }

  step a_wrong_password as "a wrong password" = auth.login as bad {
    password = "wrong"
    drop expects
    drop vars
    expect status == 401
  }
}
```

```art
# scenarios/orders.art
flow "../flows/orders.art"

scenario alice {}
scenario bob   { user = "bob" }
```

The keyword is the item's type -- `request` for an HTTP verb, `command` for
`run`, `query` for `db` -- and the vars above its action are its inputs. Each
input takes the first value it finds: the one the step passes, then the name
in the flow at that point, then its own default. So `log in` passes only the
password: `url` and `user` are the flow's, and a scenario that sets `user`
logs in as someone else. An input nothing supplies is a compile error at the
step. The vars after the action are the item's outputs, and the steps after
the call read them, as `list the orders` reads `token`.

A step's lines can change what that one call sends and expects: `header`,
`query`, `body` and `body.<field>` lines override the request, `drop expects`
and `drop vars` remove the item's expects and outputs -- a 401 has no token to
write -- and `expect` lines add new ones. `as bad` prefixes the call's outputs,
`bad_token`, so a second call keeps the first one's values; without it, the
second call's `token` replaces the first's.

A collection holds single items; steps in order are a flow. A collection file
holds one collection and nothing else. `import "../collections/auth.art"` puts
it in scope by its id, `auth` for `collection auth { ... }`;
`import "../collections/Backend APIs.art" as api` puts it in scope as `api`.

Every collection, item, flow, step and scenario has a snake_case id, and may
have a label after `as`: `request legacy_readiness as "Legacy readiness"`.
Reports and editors show the label, and the id where there is none; the id is
what a call, `--item`, `report.json` and a trace file name use. A step that
calls an item with no label of its own shows the item's.

An override applies to an `api` request only. To vary a command or a query,
make the value an input and pass it.

A call is not a function call. Before anything is checked, every scenario is
made into the flow with its values and every call into the plain step it
stands for, and `artemis expand` prints that:

```sh
artemis expand scenarios/orders.art
```

```text
# from orders (flows/orders.art:4) via scenarios/orders.art:5
scenario bob_2 as "bob" {
  var user = "bob"
  var url = env("API_URL")

  # from auth.login (collections/auth.art:4) via flows/orders.art:8
  step log_in as "log in" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"username": user, "password": env("API_PASSWORD")}
    }
    expect status == 200
    var token = body.data.access_token
  }

  step list_the_orders as "list the orders" {
    get "${url}/orders" { header "Authorization" = "Bearer ${token}" }
    expect status == 200
  }

  # from auth.login (collections/auth.art:4) via flows/orders.art:17
  step a_wrong_password as "a wrong password" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"username": user, "password": "wrong"}
    }
    expect status == 401
  }
}
```

A failure in a step a call brought in names the collection file and line it
was written on. Each collection is checked on its own, once, and an item may
read only its inputs and its response. The rules are in
[SPEC.md](../SPEC.md#collections).
