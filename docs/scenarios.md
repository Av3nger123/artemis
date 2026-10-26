# Writing scenarios

A scenario is a `.art` file of steps and what to expect of each one. This page
walks through one, then shows how a request written once is reused across
scenarios. [SPEC.md](../SPEC.md) is the full reference for the language.

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

Run it:

```sh
artemis run checkout.art
```

Five things in that file are the whole language:

| | |
| --- | --- |
| `var` | A value the steps can name, evaluated once before the first step runs. `env("NAME")` reads the environment, or the `.env` file |
| the action block | `post "..." { ... }` -- one per step, and it is what gives the step its type. There is no `type:` key |
| `expect` | One assertion, written as an expression. One `expect` line is one assertion in the report, so the line you wrote and the failure you read about are one to one |
| `capture` | A value pulled out of what the step produced, available by name in every later step |
| `retry`, `timeout`, `within` | Three different clocks: retry the whole step, bound one attempt, or wait for one assertion to come true |

## Reusing requests

A request or a sequence of steps written once lives in a **collection**, and a
scenario `use`s it by name, with arguments:

```art
# collections/auth.art
collection "auth" {

  request login(user, secret password, base = env("API_URL")) {
    post "${base}/token" {
      body = {"username": user, "password": password}
    }
    expect status == 200
    secret capture token = body.data.access_token
  }
}
```

```art
# checkout.art
import "collections/auth.art"

scenario "orders need a token" {
  var url = env("API_URL")
  use auth.login { user = "alice", password = env("API_PASSWORD") }

  step "list the orders" {
    get "${url}/orders" {
      header "Authorization" = "Bearer ${token}"
    }
    expect status == 200
  }
}

scenario "bad password is rejected" {
  use auth.login {
    user = "alice"
    password = "wrong"
    drop expects
    drop captures
    expect status == 401
  }
}
```

A `use` passes arguments by name, and can change what that one use sends and
expects: `header`, `query`, `body` and `body.<field>` lines override the
request, `drop expects` and `drop captures` remove the template's expects and
captures -- a 401 has no token to capture -- and `expect` lines add new ones.
`as rush` names the step `rush` and its captures `rush_...`, so one request can
be used twice in a scenario.

A collection holds single requests; steps in order are a scenario. A
collection file holds one collection, whose name is a title, and no scenario.
`import "collections/auth.art"` puts it in scope as `auth`, after the file;
`import "collections/Backend APIs.art" as api` puts it in scope as `api`.

An override applies to an `api` request only. To vary a terminal or browser
request, make the value a parameter and pass it as an argument.

A `use` is not a call. Before anything is checked, every `use` is replaced by
the plain steps it stands for, and `artemis expand` prints that file:

```sh
artemis expand checkout.art
```

```art
scenario "orders need a token" {
  var url = env("API_URL")
  secret var login_password = env("API_PASSWORD")

  # from auth.login (collections/auth.art:4) via checkout.art:6
  step "auth.login" {
    post "${env("API_URL")}/token" { body = {"username": "alice", "password": login_password} }
    expect status == 200
    secret capture token = body.data.access_token
  }

  step "list the orders" {
    get "${url}/orders" { header "Authorization" = "Bearer ${token}" }
    expect status == 200
  }
}
```

A failure in a step a use brought in names the collection file and line it was
written on. Each collection is checked on its own, once, and may read only its
parameters and its response. The rules are in
[SPEC.md](../SPEC.md#collections).
