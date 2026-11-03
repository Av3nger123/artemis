# The Artemis scenario language

This is the normative reference for `.art`, the language artemis tests are
written in. It is written to be read start to finish by whoever -- or whatever --
is about to author a test: every section opens with an example you can copy
and then states the rules that example obeys.

If this document and anything else disagree, this document is right.

> **Status.** `artemis run` reads this language. The front end -- lexer, parser,
> checker, printer -- is built, and every example in this document is compiled by
> `pkg/shared/spec_test.go` on every CI run: an example here either works or the
> build is red.
>
> The language was designed and approved in `docs/artemis-dsl-design.md`; four
> places where this spec decides something that design left open are marked
> **spec decision**. All four step types are specified here.
> YAML is not a runtime format: it is an input to `artemis migrate`, and the
> [docs/cli.md](docs/cli.md) documents the commands.

---

## A whole test

A test is three kinds of file, and each does one job. A **collection** holds
units of work written once. A **flow** holds steps in order. A **scenario
file** holds data: each scenario in it is one run of the flow, with its own
values.

```art
# collections/auth.art
collection "auth" {

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
# flows/checkout.art
import "../collections/auth.art"

flow checkout {
  var sku      = "A-1"
  var quantity = 1
  var coupon   = ""
  var code     = 200
  var total    = 100
  private var url = env("API_URL")

  setup {
    step "log in" = auth.login { password = env("API_PASSWORD") }
  }

  step "pay" {
    post "${url}/cart/pay" {
      header "Authorization" = "Bearer ${token}"
      body = {"sku": sku, "quantity": quantity, "coupon": coupon}
    }
    timeout = "5s"
    retry { times = 3, delay = "2s" }

    expect status == code
    expect body.total == total
    var paid = status
  }

  if paid == 200 {
    step "the order is there" {
      get "${url}/orders/latest" {
        header "Authorization" = "Bearer ${token}"
        query "limit" = 1
      }
      expect status == 200
      expect body.total == total
      expect body.items[0].sku == sku
      expect body.email matches /.+@.+/
      expect body.id is number
    }
  } else {
    step "the error says why" {
      get "${url}/errors/last" {
        header "Authorization" = "Bearer ${token}"
      }
      expect body.code exists
    }
  }
}
```

```art
# scenarios/checkout.art
flow "../flows/checkout.art"

scenario "full price" {}

scenario "with a coupon" {
  tags     = ["smoke"]
  quantity = 3
  coupon   = "TEN"
  total    = 270
}

scenario "bad coupon" {
  tags   = ["errors"]
  coupon = "NOPE"
  code   = 422
  total  = skip
}
```

```sh
artemis run scenarios/checkout.art
```

That runs the flow three times, once per scenario, and reports three
scenarios. Everything in this document is a rule those three files follow.

---

## The three files

| | Collection file | Flow file | Scenario file |
| --- | --- | --- | --- |
| Holds | One `collection "<title>" { ... }` of items | `import` lines, then one `flow <name> { ... }` | A `flow "<path>"` header on its first line, then `scenario "<title>" { ... }` blocks |
| Its job | Units of work, written once: see [Collections](#collections) | Steps in order: see [Inside a flow](#inside-a-flow) | Data: see [Scenario files](#scenario-files) |
| Runs | One item at a time, with [`--item`](#running-one-item) | Never alone: through a scenario file that names it | Yes: each scenario is one run of the flow |

| | |
| --- | --- |
| Extension | `.art`, for all three |
| Comments | `#` to the end of the line |
| Statement separator | A newline, or a comma inside `{ }` where the grammar allows one. No semicolons |
| Trailing commas | Permitted in objects and arrays |
| Encoding | UTF-8 |

Each kind of file holds one thing, and each of these is a compile error:

| | Code |
| --- | --- |
| A flow file that also holds a collection or a scenario | `flow-file-holds` |
| A file with a second flow, or a second `flow "..."` header | `second-flow` |
| A scenario file whose `flow "..."` header is not its first line | `flow-ref-placement` |
| A header whose path cannot be read, or names a file that holds no flow | `flow-not-found`, `not-a-flow-file` |
| A second `collection` in a collection file | `second-collection` |

Nothing is shared between scenarios: each run of a flow starts with the flow's
vars and the scenario's values and nothing else, so what one scenario's steps
wrote is invisible to the next. Nothing is shared between files except what a
flow imports and what a scenario file names.

---

## Inside a flow

A flow holds these declarations:

```art
flow list_orders {
  config browser { headless = true }   # settings for a step type

  var limit = 10                       # a value a scenario can set
  private var url = env("API_URL")     # a value only the flow sets

  step "list" {                        # a thing to do, and what to expect of it
    get "${url}/orders?limit=${limit}"
    expect status == 200
  }
}
```

| Declaration | Form | What it is |
| --- | --- | --- |
| `config` | `config <ident> { <key> = <expr>, ... }` | Settings for one step type, named by the ident |
| `var` | `var <ident> = <expr>`, `var <ident>` | A parameter of the flow: a value every step can name, and a scenario can set |
| `private var` | `private var <ident> = <expr>` | A value every step can name, and no scenario can set |
| `step` | `step "<name>" { ... }` | One action and what to expect of it |
| an item call | `step "<name>" = <collection>.<item> { ... }` | A step written once in a collection -- see [Collections](#collections) |
| `setup`, `teardown` | `setup { ... }`, `teardown { ... }` | Steps that run first, and steps that run last whatever happened -- see [setup and teardown](#setup-and-teardown) |
| a block | `parallel { ... }`, `if`, `switch`, `try`, `repeat` | Steps that run at the same time, on a condition, under try, or in a loop -- see [Blocks](#blocks) |

The flow's name is an identifier, and reports print it. A flow is the
function, its vars are its parameters, and a scenario is one call of it.

Steps run in the order they are written, one after another: a step reads what
the steps above it wrote, so the order is the meaning. A block is how a flow
asks for anything else -- steps at the same time, on a condition, or until
something holds -- and it says so where it is written.

Whole scenario *files* are a different question, and one the language does not
answer. `artemis run --jobs N` runs N of them at once; see
[Running files at once](#running-files-at-once).

### `var`

The vars at the top of a flow, above its first `setup`, step or block, are its
parameters:

```art
# flows/orders.art
flow orders {
  var version = "v2"
  var limit   = 10
  var user
  var accept  = "application/json"
  private var base = env("API_URL")
  private var root = "${base}/${version}"

  step "list" {
    get "${root}/orders?limit=${limit}&user=${user}" {
      header "Accept" = accept
    }
    expect status == 200
  }
}
```

| Form | Meaning |
| --- | --- |
| `var x = <expr>` | A default. A scenario can set it |
| `var x` | **Required.** Every scenario of the flow must set it: one that does not is `missing-param`, naming the var |
| `private var x = <expr>` | Internal. No scenario can set it -- one that tries is `private-param` -- and it is not one of the names a scenario's did-you-mean offers |

A var's value is any expression, evaluated once, before the first step runs.
A var may name an earlier var, as `root` does. It may not name a later one,
and it may not name a value a step writes: no step has run yet when vars are
evaluated. Both are compile errors.

**Vars go at the top.** A `var` below the flow's first setup, step or block is
the compile error `var-after-step`: every var is evaluated before the first
step runs, so a var written lower would suggest an order the runner does not
follow. A value a step makes is a [step var](#step-vars), written after the
step's action.

Two vars of one name at the top of a flow are `duplicate-binding`. A var may
not be named `tags`, which is each scenario's own list (`reserved-tags`).

### `setup` and `teardown`

```art
flow checkout {
  private var url = env("API_URL")

  setup {
    step "make a cart" {
      post "${url}/carts"
      expect status == 201
      var cart_id = body.id
    }
  }

  step "pay" {
    post "${url}/carts/${cart_id}/pay"
    expect status == 200
    var order_id = body.id
  }

  teardown {
    step "cancel the order" {
      delete "${url}/orders/${order_id}"
    }

    step "drop the cart" {
      delete "${url}/carts/${cart_id}"
    }
  }
}
```

`setup` runs before the flow's own steps and `teardown` after them, once for
each scenario. Each holds steps, item calls and blocks, and a flow has at most
one of each. **The file is in run order**: `setup` is written above the first
step and `teardown` below the last -- `setup-after-step`,
`teardown-before-step` and `duplicate-phase` are compile errors otherwise -- so
the [scope](#what-is-in-scope) rule is unchanged: what setup writes is in
scope for every step below it, and teardown sees everything written above it.

| | |
| --- | --- |
| A setup step does not pass | The rest of setup and every one of the flow's own steps is **skipped**, each naming the setup step that failed. Teardown still runs |
| One of the flow's own steps fails | The steps after it run, as they always have. Teardown runs |
| Teardown | Always runs, every step of it -- after a failure as after a pass, and after a teardown step that failed |
| A teardown step reads a name no step wrote | It is **skipped**, naming it: there is nothing to clean up. `cancel the order` above is skipped when `pay` failed before writing `order_id` |
| A teardown step fails | The scenario fails: cleanup that did not happen is a fault, and the next run may meet what it left |

A skipped step is `"status": "skip"` in the [JSON report](#the-json-report),
with the reason in `error`, and setup and teardown steps carry `"phase":
"setup"` or `"phase": "teardown"`. The console prints them as `setup: make a
cart`, and JUnit names them `[setup] make a cart`.

### `config`

**Spec decision.** `config` takes a subject and settings for it. There are two:
`config browser`, which belongs to the browser step type and is specified with
it -- see [`config browser`](#config-browser) -- and `config http`, how the
flow's api steps talk:

```art
flow anonymous {
  config http { cookies = false }

  step "no session" {
    get "https://example.com/me"
    expect status == 401
  }
}
```

| Setting | Value | Default |
| --- | --- | --- |
| `cookies` | `true` or `false` | `true`: the api steps of each run share a [cookie jar](#cookies) |

A `config` block naming a subject that does not exist, or setting a key that
subject does not define, is a compile error.

### Cookies

The api steps of one run of a flow share one cookie jar, as a browser's pages
do: a cookie a response sets is sent on the later requests to the site that
set it, so an API that keeps its session in a cookie is tested by logging in
once.

```art
flow cookie_session {
  private var url = env("API_URL")

  step "log in" {
    post "${url}/login" { body = {"user": "alice", "password": env("PW")} }
    expect status == 200
    expect cookies.sid.http_only == true
  }

  step "read the profile" {
    get "${url}/me"
    expect status == 200
  }
}
```

Each scenario starts with an empty jar, and its cookies never reach another
scenario. A step that writes its own `header "Cookie" = ...` sends exactly that
and nothing from the jar -- a test of a forged or expired session must not be
handed the real one -- and what its response sets is still kept for the steps
after it. `config http { cookies = false }` keeps no jar: every request sends
only the headers it wrote.

`cookies` reads what *this* response set; what the jar holds is not a value a
step can read. A [trace](#tracing-a-step) withholds every cookie's value, in
`cookies` and in `headers["Set-Cookie"]`.

---

## Scenario files

```art
# scenarios/orders.art
flow "../flows/orders.art"

scenario "alice"        { user = "alice" }
scenario "bob, limit 1" { user = "bob"  limit = 1 }
scenario "from the env" {
  tags = ["smoke"]
  user = env("SMOKE_USER", "alice")
}
```

A scenario file names its flow **once**, on its first line: `flow "<path>"`,
relative to the scenario file. Every scenario in the file is one run of that
flow, named by its title, and the file's scenarios run in the order they are
written.

### Setting a var

Inside a scenario's braces each line is `<name> = <value>`, or `tags`, and
nothing else. Several may share a line, as in `bob, limit 1`.

| | |
| --- | --- |
| The name | A var at the top of the flow, not `private`. A name the flow has no such var for is `unknown-param`, with a did-you-mean or the list of the flow's vars; a private one is `private-param`; a name set twice is `duplicate-argument` |
| The value | Any expression that reads no name: a literal, an array or object of them, `env()`, `json()`, `file()` and the other [builtins](#builtins). A value that reads a name -- `total = quantity * 90` -- is `scenario-value-reads-name`: the flow's vars have no value yet where a scenario's are given |
| A var not set | Keeps the flow's default. A required var not set is `missing-param`, at the scenario's title |
| Anything else | A `step`, an `expect`, a `var` or an override line is `data-only-scenario`: **a scenario changes data, never the shape of the flow.** A test that needs other steps is another flow |

A scenario that sets nothing, `scenario "full price" {}`, runs the flow with
its defaults.

A scenario has no routing of its own. The flow does all of it, with
[`if` and `switch`](#blocks) on the values it is given -- `if paid == 200` in
[the whole test](#a-whole-test) above -- and a flow has to cover every case
its scenarios can cause.

### `tags`

```art
# scenarios/smoke.art
flow "../flows/orders.art"

scenario "smoke" {
  tags = ["smoke", "payments"]
  user = "alice"
}
```

`tags` is the words a run selects scenarios by: `artemis run scenarios/
--tags "smoke and not slow"`. They are written out -- a list of string
literals, because a run selects before it evaluates anything -- and each is
one lower-case word of letters, digits, `_`, `.` and `-`, so a tag is always
one word of a `--tags` expression. A tag that is not, or is listed twice, is
`bad-tag`; a second `tags` is `duplicate-tags`.

`--tags` takes an expression of tags joined by `and`, `or` and `not`, with
parentheses; `not` binds tightest and `or` loosest. `--grep` takes a regular
expression over the scenario's title. A scenario a run does not select is in
the [JSON report](#the-json-report) with `"status": "skip"` and why in `error`,
and is not on the console. A selection of nothing is an error, and a tag the
expression names that no scenario has is a warning naming the nearest one.

Only what is selected is checked before the run: an `env()` only an unselected
scenario reads is not [required](#builtins).

### `skip`

```art
# scenarios/any_total.art
flow "../flows/checkout.art"

scenario "any total" {
  coupon = "NOPE"
  total  = skip
}
```

A scenario can set a var to `skip`. It turns checks off, and is the one way a
scenario reaches past data:

| Where the flow reads the var | With `skip` |
| --- | --- |
| In an `expect` | The `expect` is removed, for this scenario |
| As an object field's value: `{"sku": sku, "coupon": coupon}` | The field is left out: `{"sku": "A-1"}` is sent |
| Anywhere else -- a URL, an interpolation, a condition, arithmetic | The compile error `skip-read`, naming the scenario |

`skip` reaches into the steps an item call brings in: a skipped var that
supplies an item's input, by name or [through the context](#how-an-item-gets-its-values),
removes the item's `expect`s and object fields that read it, by the same
rules.

A step that writes the name gives it a normal value again, and the `expect`s
after that step run as usual. Where only some paths have written it -- one
branch of an `if` -- an `expect` after the block that reads it is
`skip-maybe-written`: it may still be `skip` there.

`skip` is a reserved word, so no var is named it.

### Running scenario files

| Command | What runs |
| --- | --- |
| `artemis run scenarios/checkout.art` | Every scenario in the file, each one run of the flow |
| `artemis run scenarios/ --tags smoke` | Every scenario file under the folder, then the scenarios their tags select |
| `artemis run scenarios/checkout.art --grep "coupon"` | The scenarios whose titles match |
| `artemis run flows/checkout.art` | Nothing: `flow-not-runnable`, whose hint names the scenario files near it that name this flow |
| `artemis run collections/auth.art --item auth.login` | One item alone -- see [Running one item](#running-one-item) |

A folder is walked for `.art` files and each scenario file in it is run. A flow
file or a collection file found there is skipped, not an error: a folder holds
all three kinds, and only one kind runs. A flow file is an error only when it
is the path the command was given.

An environment file supplies `env("...")` and nothing else: it does not set a
flow's vars by name. A value that should come from the environment is written
`env("...")` in the scenario or in the flow.

Each scenario is one entry in the report, named by its title, carrying the
scenario file's path as `file` and the flow's name as `flow`.

---

## Steps and actions

Every step holds exactly **one action block**, and that block is what gives the
step its type. There is no `type:` key:

```art
step "fetch it" {
  get "https://example.com/items/1"    # <- an HTTP verb, so this is an api step
  expect status == 200
}

step "build it" {
  run "go" { args = ["build", "./..."] }   # <- run, so this is a terminal step
  expect exit_code == 0
}
```

| Action | Step type |
| --- | --- |
| `get`, `post`, `put`, `patch`, `delete`, `head`, `options` | `api` |
| `run "<command>" { ... }` | `terminal` |
| `browser { ... }` | `browser` -- see [`browser` steps](#browser-steps) |
| `db <url> { ... }` | `db` -- see [`db` steps](#db-steps) |

A step with no action block, and a step with two, are both compile errors. The
type is known before any name in the step is resolved, which is what makes
[scope](#what-is-in-scope) per step type.

Besides its action, a step may hold `expect`, `retry` and `timeout`
statements and [step vars](#step-vars), below the action, in any order and any
number:

```art
step "orders" {
  get "${url}/orders"
  timeout = "5s"
  retry { times = 3, delay = "2s" }
  expect status == 200
  var first = body.data[0].id
}
```

A step can also call an item a collection holds instead of writing its own
action: `step "log in" = auth.login { ... }`. See [Collections](#collections).

---

## `api` steps

The action is an HTTP verb and a URL. The URL is an expression, so it is usually
an interpolated string:

```art
step "create an order" {
  post "${url}/orders" {
    header "Content-Type" = "application/json"
    header "Authorization" = "Bearer ${token}"
    query "dry_run" = false
    body = {"item": "ABC-1", "quantity": 2}
  }
  expect status == 201
}
```

| Field | Form | Rules |
| --- | --- | --- |
| the URL | `<verb> <expr>` | Required, and the first thing in the block. Any expression that renders to a string |
| `header` | `header <expr> = <expr>` | Repeatable. The name is a string expression, as is the value. A repeated name keeps the last value |
| `query` | `query <expr> = <expr>` | Repeatable. Appended to the URL's query string and escaped. A non-string value renders by the [rendering rules](#how-a-value-renders) |
| `body` | `body = <expr>` | At most one. An object or array literal is serialised to JSON; a string is sent as it is; a [`file()`](#data-and-files) is sent as its bytes |
| `form` | `form <expr> = <expr>` | Repeatable. A urlencoded form field, in source order |
| `multipart` | `multipart <expr> = <expr>` | Repeatable. A `multipart/form-data` field: text, or a [`file()`](#data-and-files) sent as a file part |

A verb with no block -- `get "${url}/health"` -- sends no headers of its own and
no body, which is what you want for a health check.

### Bodies

`body` takes a value, not a string with values spliced into it:

```art
body = {"username": "alice", "roles": ["admin"], "quota": limit}
```

That is serialised to JSON. So an object a step wrote reaches a body as a value:

```art
step "read the user" {
  get "${url}/users/1"
  expect status == 200
  var user = body.data
}

step "write it back" {
  put "${url}/users/1" {
    body = {"user": user, "seen": true}
  }
  expect status == 200
}
```

A string body is still accepted, for a payload that is not JSON:

```art
body = "name=alice&roles=admin"
```

A form is fields rather than a body. `form` sends them urlencoded and
`multipart` as `multipart/form-data`, where a field may be a file:

```art
step "sign in" {
  post "https://example.com/login" {
    form "user" = "alice"
    form "password" = "s3cret"
  }
  expect status == 200
}

step "upload an avatar" {
  post "https://example.com/me/avatar" {
    multipart "avatar" = file("fixtures/avatar.png")
    multipart "caption" = "hello"
  }
  expect status == 201
}
```

A request has one body, so `body`, `form` and `multipart` exclude one another:
writing two of them is the compile error `body-conflict`. A file in a `form` is
a step error naming `multipart`, because a urlencoded form cannot carry one.

---

## `terminal` steps

The action is `run`, the command, and what to run it with:

```art
step "the tag exists" {
  run "git" {
    args = ["rev-parse", "--verify", "${tag}^{commit}"]
  }
  expect exit_code == 0
  expect stderr matches /^\s*$/
  var sha = match(stdout, /^([0-9a-f]{40})/)
  timeout = "5s"
}
```

| Field | Form | Rules |
| --- | --- | --- |
| the command | `run <expr>` | Required, and the first thing in the block. The program to execute |
| `args` | `args = [<expr>, ...]` | At most one. Each entry arrives at the command exactly as written |
| `cwd` | `cwd = <expr>` | At most one. Relative to where artemis was run from, **not** to the scenario file |
| `stdin` | `stdin = <expr>` | At most one. Text written to the command's standard input, or a [`file()`](#data-and-files) whose bytes are |
| `env` | `env { <NAME> = <expr>, ... }` | At most one. Variables added to the command's environment |

**There is no shell.** The command is executed directly with its arguments: no
word splitting, no globbing, no `~` expansion. `run "ls *.go"` looks for a binary
with a space in its name and does not find one. A step that wants a pipeline
or a glob names a shell itself:

```art
step "the changelog mentions the tag" {
  run "sh" { args = ["-c", "grep -c '${tag}' CHANGELOG.md"] }
  expect stdout matches /^[1-9]/
}
```

That is longer to write and impossible to misread.

`env` is **layered onto** the environment artemis itself was given, not a
replacement for it -- a replaced environment has no `PATH`. A name set here wins
over an inherited one. There is no way to unset a variable.

A step that sets no `stdin` gives the command a standard input that is
immediately at end of file, so a command that reads stdin cannot hang the run.

Each stream is kept up to **1 MiB per attempt**; a command that prints more than
that has the rest counted and dropped.

A whole flow of terminal steps:

```art
flow release_checks {
  var tag = env("RELEASE_TAG")

  step "the tag exists" {
    run "git" { args = ["rev-parse", "--verify", "${tag}^{commit}"] }
    expect exit_code == 0
    expect stderr matches /^\s*$/
    var sha = match(stdout, /^([0-9a-f]{40})/)
    timeout = "5s"
  }

  step "the changelog mentions it" {
    run "sh" { args = ["-c", "grep -c '${tag}' CHANGELOG.md"] }
    expect stdout matches /^[1-9]/
  }

  step "it builds" {
    run "go" {
      args = ["build", "./..."]
      env  { CGO_ENABLED = "0" }
    }
    expect exit_code == 0
    expect stderr matches /^\s*$/
  }
}
```

---

## `browser` steps

The action is `browser` and a block of what to do in the page:

```art
flow upgrade_to_pro {
  config browser { headless = true, viewport = "1280x720" }

  var url = env("APP_URL")

  step "upgrade the plan" {
    browser {
      goto "${url}/settings/billing"
      fill "#email" = "alice@example.com"
      select "#plan" = "pro"
      click "text=Upgrade"
      wait "1s"
    }
    expect page.url contains "/settings/billing"
    expect text("[role=status]") contains "Pro" within "10s"
    expect count(".invoice") > 0
    var plan = match(text(".plan-badge"), /plan: (\w+)/)
  }
}
```

The block is **required** -- a browser step with no actions does nothing at all
-- and it holds these eight statements, in any order and any number:

| Action | Shape | What it does |
| --- | --- | --- |
| `goto` | `goto "/orders"` | Navigates to a URL. A relative one resolves against the page's current address |
| `click` | `click "text=Sign in"` | Clicks the first element the selector matches |
| `fill` | `fill "#email" = "alice@example.com"` | Replaces an input's value with the text |
| `select` | `select "#plan" = "pro"` | Chooses an option of a `<select>`, by the option's value |
| `press` | `press "Enter"` | Sends one key to whatever has focus |
| `hover` | `hover ".plan-menu"` | Moves the pointer over the first match |
| `upload` | `upload "#avatar" = file("me.png")` | Attaches a [`file()`](#data-and-files), or a list of them, to a file input. A string is a path as the browser resolves it |
| `wait` | `wait "1s"` | Waits for a fixed duration |

Three of them -- `fill`, `select` and `upload` -- take a value after an `=`; the
other five take a selector and nothing else. Writing the wrong shape is a
compile error naming the action and showing the shape, rather than a syntax
error about the `=`:

```
upgrade.art:9:7: "click" takes a selector and no value
    9 |       click "text=Sign in" = "now"
      |       ^^^^^^^^^^^^^^^^^^^^^^^^^^^^
   hint: click "text=Sign in"
```

A statement that is not one of the eight is a compile error too, with a
did-you-mean when it is nearly one of them and the eight listed when it is not.

`wait`'s argument is a **duration**, the same `"30s"` / `"500ms"` / `"2m"` a
`timeout` takes, and a string that is not one is a compile error. Waiting for a
*condition* is not written here: it is [`within`](#within) on the assertion that
names the condition, which is the only form that says what is being waited for.

A selector is a string, and nothing in this language interprets it: it is passed
through to the browser as written. So `"#email"`, `".invoice"`,
`"[role=status]"` and `"text=Sign in"` are all just strings, and
[interpolation](#interpolation) works in one as it does anywhere -- `click
"text=${plan}"`.

### What a browser step binds

| Name | Type | What it is |
| --- | --- | --- |
| `page.url` | string | The page's current address, after any redirect or in-page navigation |
| `page.title` | string | The document's title |

`page` is the one root in the language whose members are a **closed set**, so
`page.titl` is a compile error with the fix. A response's `body.datta` is not,
because a response's shape is a run-time fact and checking it would reject
correct files.

The five **element functions** read the page. Each takes a selector, and `attr`
takes an attribute name as well:

| Function | Returns |
| --- | --- |
| `text("[role=status]")` | The first match's visible text, or `null` when nothing matches |
| `value("#email")` | The first match's value as a form control |
| `attr("#link", "href")` | The named attribute of the first match, or `null` when it has none |
| `count(".invoice")` | How many elements the selector matches -- `0` and not an error when none do |
| `visible(".modal")` | Whether the first match is visible |

**Spec decision.** What an element function answers when the selector matches
*nothing*: `count()` is `0`, `visible()` is `false`, and `text()`, `value()` and
`attr()` are **`null`**. The design document specifies the five functions and
their arities and leaves this open. They answer rather than erroring because
`expect count(".invoice") == 0` and `expect text(".error") is null` are the
assertions an author wants to be able to write, and a function that errored on
no match would make the absence of a thing unassertable. An absent attribute on
a present element is `null` for the same reason -- `exists` is the operator for
asking, and [absent paths](#paths) already say what `null` means in an
assertion.

Browser actions themselves auto-wait: a `click` on an element that is about to
appear waits for it, which is the browser's own behaviour and is inherited
rather than reimplemented. So an action does not need a `wait` in front of it;
`wait "1s"` is for a pause that has no element to wait on.

They are functions and not roots, so `expect visible` is a compile error that
names the call, and the wrong number of arguments is a compile error that shows
the signature. They are in scope in a browser step and **nowhere else**: in an
`api` step, in a `terminal` step or in a `var`'s value, each one is a compile
error listing what *is* in scope there. The same holds the other way --
`expect status == 200` in a browser step is an error naming `page.url`,
`page.title` and the five functions.

### `config browser`

| Setting | Type | Default |
| --- | --- | --- |
| `headless` | boolean | `true` |
| `viewport` | string, `"<width>x<height>"` | the browser's own default |

```art
config browser { headless = true, viewport = "1280x720" }
```

It holds for the whole flow: one browser and one context for each run of it,
shared by every browser step of that run in the order they are written, so a
step can sign in and the next step is still signed in. A flow with no browser
step opens no browser.

An assertion in a browser step has a default [`within`](#within) of **5s**,
because a page settles asynchronously and an assertion that reads it once is a
race. The other two step types have no default; see [`within`](#within).

### When a browser step fails

A browser step that does not pass leaves a **screenshot** of the page, and the
[JSON report](#the-json-report) names its path on the step and on the matching
`failures` entry. It is written after the last attempt, because that is the
attempt the report describes.

```
artemis run upgrade.art                            # artemis-screenshots/
artemis run upgrade.art --screenshots shots        # shots/
artemis run upgrade.art --screenshots ""           # none
```

### Running files at once

Steps within a flow are ordered, and so are the scenarios within one file.
Separate files share nothing at run time: what one scenario's steps write is
invisible to the next, and an item call copies the item's step into the flow
before anything runs rather than sharing a value. So separate scenario files
can run at the same time, and `--jobs` says how many:

```sh
artemis run scenarios/ --jobs 6      # six files at once
artemis run scenarios/               # one at a time, the default
```

This is a property of the runner. Steps of one flow at the same time are the
language's: a [`parallel`](#blocks) block.

The order of a report does not change. A scenario keeps the place its file had in
discovery order, however early or late that file finished, so a golden file and a
`jq` expression are unaffected.

What does change is the console. With more than one job, each file's output is
held and printed when its turn comes, instead of a step at a time as it happens.
Two files printing to one terminal at once produce something no reader can
follow. A run with one job still streams, which is why one is the default.

One is the default for a second reason: the limit on a run like this is the
service under test. A run pointed at a staging API meets a rate limit long
before it saturates the machine.

### Tracing a step

A report says what failed. A **trace** says what went over the wire, and
`--trace` asks for one file per step:

```sh
artemis run checkout.art --trace traces      # traces/checkout-get-a-token.json
artemis run checkout.art                     # no traces: off unless asked
```

Every step gets one, whether it passed or not, because comparing a passing step
with a failing one is half of how a reader works out what changed. A step that
could not run at all gets one too, holding what it sent and the reason: that is
the case with no response to read, and so the case a trace helps with most.

A trace records the step's observation under the names its type binds --
`status`, `body`, `raw`, `headers` for an api step, `exit_code`, `stdout`,
`stderr` for a terminal one -- beside what the step sent. Only the last attempt
of a retried step is recorded, because a later attempt replaces an earlier one
whole.

A cookie's value and the password in a db step's URL are withheld, written as
`***` rather than left out, so a withheld value and an absent one do not read
the same. Everything else the step sent and got back is recorded as it was --
a password in a request body, a token in a header -- because nothing in the
language marks a value as a credential. Masking credentials without marking
them is an open item on [the roadmap](ROADMAP.md).

A string longer than 64 KiB is cut, the value says so, and the trace's
`truncated` list names what was cut.

Tracing is off unless asked for, and a trace file holds whatever the step sent
and the service answered, credentials included, so treat the folder as
sensitive.

The name is `<scenario>-<step>.png`, lower-cased with everything that is not a
letter or a digit collapsed to a hyphen, and it has no timestamp: a rerun
overwrites the file from the run before it, so the path in the report is
predictable enough for a CI job to name the artifact it is about to upload. Two
steps that share a name in one run get `-2` and `-3`. The folder is created only
when a browser step actually fails, so a run of `api` steps never grows one.

---

## `db` steps

The action is `db`, the database to connect to, and the SQL to run there:

```art
flow order_saved {
  private var url = env("API_URL")
  private var orders_db = env("ORDERS_DB")   # postgres://app:pw@localhost:5432/orders

  setup {
    step "seed" {
      db orders_db { sql = file("fixtures/seed.sql") }
    }
  }

  step "place an order" {
    post "${url}/orders" { body = {"sku": "A-1"} }
    expect status == 201
    var order_id = body.id
  }

  step "the row is there" {
    db orders_db {
      sql  = "SELECT status, total FROM orders WHERE id = $1"
      args = [order_id]
    }
    retry { times = 5, delay = "1s" }
    expect len(rows) == 1
    expect rows[0].status == "paid"
    var total = rows[0].total
  }

  teardown {
    step "remove it" {
      db orders_db {
        sql  = "DELETE FROM orders WHERE id = $1"
        args = [order_id]
      }
      expect affected == 1
    }
  }
}
```

| Field | Form | Rules |
| --- | --- | --- |
| the connection | `db <expr>` | Required, and the first thing in the block. A URL; its scheme picks the database |
| `sql` | `sql = <expr>` | For a SQL database. The statement as text, or a [`file()`](#data-and-files) whose contents are the SQL |
| `args` | `args = [<expr>, ...]` | At most one, with `sql`. Values bound to the statement's placeholders, **keeping their types** |
| `command` | `command = <expr>` | For MongoDB and Redis, in place of `sql`. See [commands](#mongodb-and-redis-command) |

A step has exactly one of `sql` and `command`: neither is `db-needs-statement`,
both is `db-statement-conflict`, and a URL written out that is sent the kind it
does not take -- `sql` to `mongodb://` -- is `db-statement-kind`.

| Scheme | Database |
| --- | --- |
| `postgres://`, `postgresql://` | PostgreSQL, and what speaks its protocol: CockroachDB, Supabase, Neon, Timescale |
| `mysql://` | MySQL, and what speaks its protocol: MariaDB, TiDB, PlanetScale |
| `sqlite:` | SQLite: `sqlite:app.db`, `sqlite:///abs/app.db`, `sqlite::memory:`. A relative path is relative to where artemis was run, like a `run` step's `cwd` |
| `sqlserver://` | SQL Server and Azure SQL: `sqlserver://user:pw@host:1433?database=shop`. The URL's path names an instance, not a database |
| `oracle://` | Oracle: `oracle://user:pw@host:1521/SERVICE` |
| `mongodb://`, `mongodb+srv://` | MongoDB: `mongodb://user:pw@host:27017/shop?authSource=admin`. The path names the database, and is required |
| `redis://`, `rediss://` | Redis and Valkey: `redis://:pw@host:6379/0`, `rediss://` over TLS |

PostgreSQL, MySQL and SQLite are in every build of artemis. SQL Server, Oracle,
MongoDB and Redis are in a full build -- `make build-full`, or `go build -tags
db_all` -- because their drivers add about 28 MB; a step naming one a binary was
built without is a step error naming the build tag that adds it.

A connection is a value, so it lives where a credential does: a flow's var,
`private` so no scenario prints it into a scenario file, read with `env()`. A
URL written out is checked when the file compiles -- `postgress://` is `unknown-db-scheme`
with the fix -- and one from a variable when the step runs.

**SQL is sent as written.** Artemis never rewrites a statement, so its
placeholders are the database's own: `$1` for PostgreSQL, `?` for MySQL and
SQLite, `@p1` for SQL Server, `:1` for Oracle. An `args` value keeps its type -- a whole number is bound as an
integer, a string as text, `null` as SQL `NULL` -- because a placeholder is
compared against a typed column. An object or an array is bound as its JSON
text, for a `json` column; a `file()` is bound as its bytes.

### What a db step binds

| Name | Type | What it is |
| --- | --- | --- |
| `rows` | array of objects | The rows a query returned, each keyed by column name. `[]` for a statement that returns none |
| `affected` | number or null | How many rows a statement changed, or a query returned; `null` when the database does not say |
| `result` | any | A command's reply. `null` for `sql` |

A row's values arrive as the scenario would have written them:

| Column | Value |
| --- | --- |
| `NULL` | `null` |
| an integer | a number, or its digits as a string past 2^53, where a number would round |
| `numeric`, `decimal` | a number when the number reads back as the same decimal -- `12.50` is `12.5` -- and the text when it would not, so money is never rounded |
| a float, a boolean, text | a number, a boolean, a string |
| `date` | `"2026-10-26"` |
| `timestamp`, `datetime` | an RFC 3339 time in UTC |
| `json`, `jsonb` | the value it holds |
| `bytea`, `blob`, `varbinary`, `raw` | base64 |
| SQL Server `uniqueidentifier` | its usual text, `6f1c0c5e-6c3c-...` |

A `date` with a time of day -- Oracle's `DATE` holds one -- is a time like any
other. MySQL's `BOOLEAN` is a `TINYINT(1)` and reads as `1` or `0`, and Oracle
returns an unquoted column name in capitals: `SELECT id` is `rows[0].ID`, and
`SELECT id AS "id"` is `rows[0].id`.

**A query or a statement.** A database cannot be asked which a statement is
before it runs, and running one twice would repeat its writes, so its first
keyword decides: `SELECT`, `WITH`, `SHOW`, `EXPLAIN`, `VALUES`, `PRAGMA`,
`DESCRIBE` and `TABLE` -- and on SQL Server `EXEC` -- are queried for `rows`, as
is a write with a `RETURNING` clause, or on SQL Server an `OUTPUT inserted.` /
`deleted.` one; anything else is executed for `affected`. Oracle's `RETURNING ...
INTO` binds out-parameters and returns no rows, so it is a statement there.

An SQL file with no `args` is a **script**: every statement in it runs, in
order, which is how a database is seeded, and `affected` is their total. A
script is read the way the database's own command-line tool reads one: on SQL
Server a line holding only `GO` ends a batch, as in `sqlcmd` and SSMS; on Oracle
a statement ends with a `;` at the end of a line and a PL/SQL block with a line
holding only `/`, as in SQL*Plus. A failing statement stops the script and is
named by its place in it. On Oracle a single statement's trailing `;`, which the
database would reject, is dropped.

| | |
| --- | --- |
| Connections | One per URL for the whole run, opened by the first step that names it and closed when the run ends, shared by every scenario and `--jobs` worker. A step has no connection of its own, so a `SET` or a temporary table may not be there for the next step |
| Transactions | None: every statement commits on its own. A test cleans up in [teardown](#setup-and-teardown) |
| More than 10,000 rows | A step error: a cut `rows` would make `len(rows)` a quiet lie. Ask for a `LIMIT`, or `count(*)` |
| Two columns alike | `SELECT a.id, b.id` is a step error naming the column: a row holds one `id`. Name one with `AS` |
| `timeout` | The statement's deadline. A query that outlives it is cancelled, and the step is an error naming it |
| `within` | Evaluated once, as for an api step: a result is complete when it arrives. Waiting for a row to appear is [`retry`](#retry) |

A database that refuses the connection, SQL it rejects and a timeout are
**step errors**: the step could not run. A row that holds the wrong value is an
**assertion failure**. A [trace](#tracing-a-step) of a db step records the URL
with its password replaced by `***`, the
statement, and the args.

### MongoDB and Redis: `command`

```art
flow order_cached {
  private var mongo = env("MONGO_URL")
  private var cache = env("REDIS_URL")

  step "the order" {
    db mongo {
      command = {"find": "orders", "filter": {"status": "paid"}, "sort": {"_id": -1}, "limit": 1}
    }
    expect len(rows) == 1
    var order_id = rows[0]._id
  }

  step "by its id" {
    db mongo { command = {"find": "orders", "filter": {"_id": {"$oid": order_id}}} }
    expect rows[0].status == "paid"
  }

  step "the cache has it" {
    db cache { command = ["GET", "order:${order_id}"] }
    expect result == "paid"
  }
}
```

A database that takes no SQL takes its own command, written the way its own
shell takes it:

| Database | `command` | `rows` | `affected` | `result` |
| --- | --- | --- | --- | --- |
| MongoDB | The document `db.runCommand()` takes, its first key the command: `{"find": "orders", "filter": {...}}` | The documents of a command that answers with a cursor -- `find`, `aggregate`, `listCollections`, `listIndexes` -- read to the end, past the first batch | The reply's `n` -- what an `insert`, `update` or `delete` counted -- or the number of documents a cursor returned | Any other command's reply document; `null` for a cursor |
| Redis | Its words as an array, as `redis-cli` takes them: `["SET", "k", "v", "EX", 60]` | `[]` | `null` | The reply: a string, a number, an array, an object for a map reply, and `null` for a key that is not there, which is an answer and not an error |

**An object written out keeps its key order**, because MongoDB reads it: a
command's name is its first key, and a `sort`'s keys are its order. An object
from a var has no written order and is sent with its keys sorted. A whole
number is sent as a 32-bit integer when it fits and a double when not, as
`mongosh` sends them. A value of a BSON type JSON has no spelling for is written
in **Extended JSON**: `{"$oid": id}` is an ObjectId -- how a filter finds a
document by an `_id` a step wrote -- `{"$date": "2026-10-26T09:30:15Z"}` a
date, `{"$numberDecimal": "19.99"}` a decimal, `{"$numberLong": "42"}` a 64-bit
integer. A reply reads the other way: an ObjectId is its hex string, a date an
RFC 3339 time, a decimal a number by the rule above.

---

## Blocks

```art
flow checkout {
  private var url = env("API_URL")

  step "log in" {
    post "${url}/login" { body = {"user": "alice", "password": env("PW")} }
    var token = body.token
  }

  parallel {
    step "cart" {
      post "${url}/carts" { header "Authorization" = "Bearer ${token}" }
      var cart_id = body.id
    }
    step "profile" {
      get "${url}/me" { header "Authorization" = "Bearer ${token}" }
      var tier = body.tier
    }
  }

  if tier == "gold" {
    step "perks" {
      get "${url}/perks"
      expect len(body) > 0
    }
  } else {
    step "upsell" {
      get "${url}/upsell"
      expect status == 200
    }
  }

  try {
    step "pay" {
      post "${url}/carts/${cart_id}/pay"
      expect status == 201
      var order_id = body.id
    }
  } catch {
    step "refund" {
      post "${url}/carts/${cart_id}/refund"
    }
  } finally {
    step "unlock" {
      delete "${url}/carts/${cart_id}/lock"
    }
  }

  repeat every "2s" within "2m" {
    step "order status" {
      get "${url}/orders/${order_id}"
      var state = body.state
    }
  } until state == "shipped" or state == "failed"

  step "shipped" {
    get "${url}/orders/${order_id}"
    expect body.state == "shipped"
  }
}
```

A flow's steps run one after another. A **block** runs the steps inside it
another way, and holds steps, item calls and other blocks; it can be written
wherever a step can, setup and teardown included.

| Block | Form | What runs |
| --- | --- | --- |
| `parallel` | `parallel { ... }` | Every item inside -- a step, or a block -- **at the same time**. The block ends when all of them have (the join) |
| `if` | `if <cond> { ... } else if <cond> { ... } else { ... }` | The first branch whose condition holds; none, when none does and there is no `else` |
| `switch` | `switch <expr> { case <value> { ... } default { ... } }` | The first `case` whose value equals the subject by `==`'s rules, else the `default` |
| `try` | `try { ... } catch { ... } finally { ... }` | The try's steps until one does not pass; then the `catch`, when one did not; then the `finally`, whatever happened. Either of catch and finally may be left out, not both |
| `repeat` | `repeat [every <d>] [within <d>] [max <n>] { ... } until <cond>` | The body, a round at a time, until the condition holds after a round |

A condition, a subject, a case value and a repeat's options read the
[context](#the-context) above the block; a block has no observation of its own, so
`status` is not in scope in one. A condition that is not `true` or `false` is a
step error, reported where the block is.

### What each block reports

Every step of the flow is in the report, run or not, in the order it is
written -- whatever order parallel branches finished in -- so a report reads top
to bottom like the file.

| | |
| --- | --- |
| A branch not taken | Its steps are skipped, saying which branch ran instead: `not taken: the if at line 18 ran if tier == "gold"` |
| A failed step in a try | The rest of the try is skipped, naming it; the catch runs. A **catch is compensation, not a cure**: the step that failed still fails the scenario |
| A failed step anywhere else | The steps after it run, as they do outside a block |
| A parallel branch | Its steps run one after another, as a flow's do; a failure in one branch does not stop the others |
| A repeat | Only its **last round** is reported, each step with `"round": n` in the [JSON report](#the-json-report) and `(round n)` on the console -- the rule [`retry`](#retry) follows for attempts. A round with a step that did not pass ends the loop |
| A repeat that runs out | A step error named for its condition, giving the bound and what the condition read last: `until state == "shipped" never held within 2m0s (60 rounds); state was "packed"` |

### Polling

`repeat` is how a flow polls. `every` is the pause between rounds -- never
before the first -- and `within` and `max` are its bounds: **at least one is
required**, so a condition that never holds cannot run a flow forever, and
a repeat with neither is the compile error `repeat-unbounded`. With both, the
first to run out ends it.

`retry` still polls a single step whose assertions should come to hold. A
repeat is for what retry cannot do: several steps a round, a stop condition that
is not an assertion -- `until state == "shipped" or state == "failed"`, so the
step after it can assert which -- and a bound in time rather than in tries.

### What a block lets the steps after it read

The [scope rule](#what-is-in-scope) -- a name is in scope below the step that
writes it, and [the last write wins](#the-context) -- follows what each block
guarantees:

| Block | Inside it | After it |
| --- | --- | --- |
| `parallel` | Each branch sees the context above the block and **nothing its siblings write**, which may not be written yet: reading a name a sibling writes is `parallel-sibling-capture`, even when the name existed before the block. Two branches writing one name is `parallel-capture-clash`, whether or not it existed before | What every branch wrote |
| `if`, `switch` | Each branch sees the context above it, and may overwrite a name in it | A name that existed before the block, holding the value the branch that ran wrote, or its old value when that branch did not write it. A name created in **every** branch, which needs an `else` or a `default`. One created in only some branches may not exist, and reading it is `capture-maybe-unbound` -- except in teardown or a finally, where a step whose name is missing is skipped, as teardown already does |
| `try` | The try's steps as a sequence; the catch and finally see the try's | What the try and the finally wrote; what a catch wrote, as an `if` branch's |
| `repeat` | The body as a sequence. A name is written again every round, and a read before the write in a round sees the previous round's value, or the value from before the block in the first | The last round's values. The body runs at least once |

Two branches of a parallel block cannot both drive the browser: a run of a
flow has one page, and two steps cannot drive it at once (`parallel-browser`). The cookie
jar, database connections and traces are shared safely. Each branch draws its
[random values](#making-and-converting-values) from a generator of its own,
seeded from the run's seed and the branch, so `--seed` draws the same values
whatever order branches finished in.

---

## Collections

A **collection** is a named set of items, each one unit of work -- one action,
with its expects and the vars it writes -- written once and called from many
flows. A step calls an item by name, may pass it values, and may change what it
sends and what it expects. Steps in order are a flow's job: a collection holds
no multi-step item, and a flow does not call another flow.

`collections/auth.art` is in [the whole test](#a-whole-test). With two more:

```art
# collections/orders.art
collection "orders" {

  request create {
    var url
    var token
    var sku
    var qty = 1

    post "${url}/orders" {
      header "Content-Type" = "application/json"
      header "Authorization" = "Bearer ${token}"
      body = {"sku": sku, "qty": qty}
    }
    expect status == 201
    var order_id = body.data.id
  }

  request pay {
    var url
    var token
    var order_id

    post "${url}/orders/${order_id}/pay" {
      header "Authorization" = "Bearer ${token}"
    }
    expect status == 200
  }
}
```

```art
# collections/ops.art
collection "ops" {

  command migrate {
    var dir = "db"

    run "make" {
      args = ["migrate"]
      cwd  = dir
    }
    expect exit_code == 0
  }

  query order_status {
    var orders_db
    var order_id

    db orders_db {
      sql  = "SELECT status FROM orders WHERE id = $1"
      args = [order_id]
    }
    expect len(rows) == 1
    var order_state = rows[0].status
  }
}
```

```art
# flows/rush.art
import "../collections/auth.art"
import "../collections/orders.art"
import "../collections/ops.art"

flow rush {
  var sku = "A-1"
  private var url = env("API_URL")
  private var orders_db = env("ORDERS_DB")

  setup {
    step "migrate" = ops.migrate {}
    step "log in" = auth.login { password = env("PW") }
  }

  step "a wrong password" = auth.login as bad {
    password = "wrong"
    drop expects
    drop vars
    expect status == 401
  }

  step "rush order" = orders.create as rush {
    header "X-Priority" = "high"
    body.qty = 5
    expect body.data.eta < 3600
  }

  step "pay for it" = orders.pay { order_id = rush_order_id }

  step "it is paid" = ops.order_status { order_id = rush_order_id }

  step "check it" {
    get "${url}/orders/${rush_order_id}" {
      header "Authorization" = "Bearer ${token}"
    }
    expect status == 200
    expect body.state == order_state
  }
}
```

**An item call is expanded, not called.** Before a flow is checked, every item
call in it is replaced by the plain step it stands for, with its inputs
substituted and its overrides applied. What is checked, run and reported is
made only of vars, steps and blocks -- the language the rest of this document
specifies. There is no runtime function, no call stack, and no value shared
between scenarios. Expansion works on the syntax tree, not on text: a value is
substituted as an expression, so `qty = limit + 1` means what it says, and
every copied line keeps its place in the collection file, so an error in it
points there.

### A collection file

| Form | What it is |
| --- | --- |
| `collection "<title>" { ... }` | A set of items. The title is any text, for people: a flow reaches the collection by the name its file is [imported](#importing) as |
| `request <name> { ... }` | An item whose action is an HTTP verb: an `api` step |
| `command <name> { ... }` | An item whose action is `run`: a `terminal` step |
| `query <name> { ... }` | An item whose action is `db`: a `db` step |
| `var <ident>` above the action | A required **input** |
| `var <ident> = <expr>` above the action | An input with a default. The default may name an earlier input, as a flow's var may name an earlier var |
| `private var <ident> = <expr>` above the action | An input no step can pass: it takes only its own default |
| the action, then `expect`, `var`, `retry` and `timeout` | Exactly the body of a step. A `var` after the action is an **output**: a [step var](#step-vars) the item writes |

The keyword is the item's type, and its action has to be one that type holds:
`run` in a `request` is `item-action-mismatch`, whose hint names the keyword
that would hold it. (`browser` items are not in the language yet.)

A collection file holds **one** collection and nothing else, so an import
names exactly one collection, the file's own. Each of these is a compile
error, wherever the file is read from:

| Error | Code | Hint |
| --- | --- | --- |
| a second `collection` in the file | `second-collection` | move it to a file of its own |
| a `scenario` in the file | `scenario-in-collection-file` | move the scenario to a scenario file |

A collection has no var of its own. Anything its items share is an input with
a default, so every name an item reads is in its own body.

An item's input may not be named `header`, `query`, `body`, `drop` or `expect`.
A [step's line](#lines-of-an-item-call) is read by its first word, so such an
input could never be passed. Declaring one is a compile error at the input:

```
collections/orders.art:3:9: an input cannot be named body: a step that calls the item reads body as an override
   3 |     var body
     |         ^^^^
   hint: rename it, for example body_value
```

### Importing

| | |
| --- | --- |
| Form | `import "<path>" [as <ident>]` |
| Where | At the top of a flow file, before the flow |
| Path | Relative to the importing file. Local files only: no URL, no package, no version |
| Effect | The file's collection is in scope by the name after `as`, or else by the file's name less `.art` -- and less a `.collection` before it, so `orders.art` and `orders.collection.art` are both `orders` |
| Not transitive | A flow reaches only the collections it imports itself |

```art
# flows/buy.art
import "../collections/orders.art" as shop
import "../collections/auth.art"

flow buy {
  private var url = env("API_URL")

  step "log in" = auth.login { password = env("PW") }
  step "order" = shop.create { sku = "A-1", qty = 2 }
}
```

A file named `Backend APIs.art` is imported the same way, `as` a name a step
can read.

The collection's title plays no part: the import line alone says what a step
reads. Each of these is a compile error at the import:

| Error | Code | Hint |
| --- | --- | --- |
| no `as`, and a file name that cannot be one, like `Backend APIs.art` | `import-needs-name` | name it: import "collections/Backend APIs.art" as Backend_APIs |
| a file with no collection | `import-no-collection` | import a collection file: a flow or a scenario file is never imported |
| two imports under one name | `duplicate-import-name` | name one of them another way with as |

So is an import cycle, named as its chain: `a.art -> b.art -> a.art`.

### Calling an item

```art
step "rush order" = orders.create as rush {
  sku = "A-1"
  body.qty = 5
}
```

| | |
| --- | --- |
| Form | `step "<name>" = <collection>.<item> [as <ident>] { <line> ... }` |
| Where | In a flow, wherever a `step` may be: setup, teardown and blocks included |
| Effect | Replaced, in place, by the item's step, named `<name>`. It runs where it is written |

The name in quotes is required, and it is the step's name in every report.
Two steps may share a name, as they may anywhere else. The braces are required
too, and may be empty: `step "pay" = orders.pay {}`.

### How an item gets its values

Each input of the item takes its value from the first of these that has one:

1. **The value the step passes**: `step "log in" = auth.login { user = "bob" }`.
2. **The name in the flow's [context](#what-is-in-scope)** at that point: a
   flow var, or a var an earlier step wrote. In `rush` above, `orders.create`
   reads `url` from the flow and `token` from what `log in` wrote, and neither
   is passed.
3. **The item's own default**.

With none of them, it is `missing-argument` at the step, naming the input. A
private input takes only its own default, and passing it is `private-input`.

The context is read where the item reads the input, so a name the context has
on only some paths -- written in one branch of an `if` -- is
`capture-maybe-unbound`, reported in the item at the line that reads it, with
the step that called it as its chain. It is never a run-time surprise.

The item's **outputs** go into the context, so the steps after the call read
them by name: `token` after `auth.login`. Its inputs' names do not.

### Lines of an item call

A line is read by its first word. A line that opens with `header`, `query`,
`body`, `drop` or `expect` is an override; any other line passes an input.
Lines apply in the order they are written, and a block may put several on one
line, separated by commas.

| Line | Target | Effect |
| --- | --- | --- |
| `<input> = <expr>` | any | Passes an input. An unknown input is `unknown-argument`, with a did-you-mean; one passed twice is `duplicate-argument` |
| `header "<name>" = <expr>` | api step | Replaces the header of that name, compared case-insensitively, or adds one |
| `query "<name>" = <expr>` | api step | Replaces the same key, or appends one |
| `body = <expr>` | api step | Replaces the whole body |
| `body.<path> = <expr>` | api step whose body is an object literal | Sets or adds that field. An item body that is not an object literal is a compile error: override it whole with `body = ...` |
| `drop expects` | any step | Removes every `expect` the item wrote. It must come before the call's own `expect` lines (`drop-after-expect`) |
| `drop vars` | any step | Removes every output the item writes. It may come anywhere in the block |
| `expect <expr> [within <dur>]` | any step | Added after the item's remaining expects |

An override that does not fit its target is a compile error: `header` on a
terminal step, for one.

An override applies to an `api` step only. To vary a `command` or a `query`,
make the value an input and pass it. The error for an override on such a step
gives the same hint.

A negative test drops both. `auth.login` writes a token, and a 401 has none: a
step var the response cannot supply is an [errored assertion](#step-vars), so
`a wrong password` above writes `drop vars` beside `drop expects`.

### `as`

`as <ident>` prefixes every output of one call with `<ident>_`.
`orders.create as rush` writes `rush_order_id`, and the steps after it read
that name; `auth.login as bad` would write `bad_token`.

Two calls of one item write the same outputs, and [the last write
wins](#what-is-in-scope): a second `auth.login` replaces the first one's
`token` for every step after it. `as` is how a flow keeps both.

### Checking a collection

Every collection is checked on its own, once, before any call of it expands.
An error in it is reported once, against the collection file, and not again per
call. A collection with an error is never expanded: its calls report nothing
more.

An item may read **only** its inputs and its own step's observation. Any other
name -- one a flow would have to supply -- is a compile error, in the
collection, whose hint is to add it as an input:

```
collections/orders.art:11:42: unknown name "session"
   11 |       header "Authorization" = "Bearer ${session}"
      |                                          ^^^^^^^
   hint: add it as an input of orders.create: a var above the action
         an api step binds status, body, raw, headers, cookies, duration_ms
         also in scope: url, token, sku, qty
```

That is the price of a collection that checks on its own and means the same
thing from every flow that calls it: everything it needs from outside is an
input.

An item may not write an output named like a step's observation -- `status`,
`body`, `raw`, `headers`, `exit_code`, `stdout`, `stderr`, `page` -- because
[`as`](#as) renames a call's outputs and every read of them, and a later
step's `body` would be ambiguous. It is the compile error `root-capture`, at
the output's name.

Errors that depend on the call -- inputs, overrides, a context name only some
paths have -- are reported at the line in the collection they concern,
followed by the step that called it and the scenario file that ran the flow:

```
collections/orders.art:11:42: "token" is written in only some branches of the if at line 7 of flows/maybe.art, so it may not exist here
   11 |       header "Authorization" = "Bearer ${token}"
      |                                          ^^^^^
   hint: write it in every branch, give the block an else or a default that does, or read it inside the branch
   used from flows/maybe.art:14:3: step "order" = orders.create { sku = "A-1" }
   used from scenarios/maybe.art:1:1: flow "../flows/maybe.art"
```

`artemis parse --json` carries those lines as `usedFrom`: a list of spans,
absent when the diagnostic was written where it is reported.

### Running one item

An item runs alone with `--item`, which is what an API client's Send needs:

```sh
artemis run collections/auth.art --item auth.login --arg url=https://api.test \
  --args login.json --trace traces --captures out.json
artemis run collections/orders.art --item orders      # every item, in the order written
```

| Flag | |
| --- | --- |
| `--item <name>.<item>` | Run that item |
| `--item <name>` | Run every item of the collection in order, the outputs of one in the context of the next, as in a flow. Each item is passed the values it has an input for |
| `--arg name=value` | An input. The value is a string and is sent as written: `${` in it is not an interpolation. Repeatable |
| `--args <file>` | Inputs from a JSON object, so a password stays off the command line. Values keep their JSON type. `--arg` wins over it |
| `--captures <file>` | Every value the item's outputs got, as `{"values": {...}, "secret": [...]}`, mode 0600 |

`<name>` is any name an import can give it: the run imports the collection
file `as` it, whatever the collection is titled. The run is made in memory,
beside the collection file, so every line of the collection keeps its number
and nothing is written to disk; it is reported under the collection file's
path, named for the item (`auth.login`). A missing or unknown input is the
checker's error and stops the run before anything is sent.

### `artemis expand`

`artemis expand scenarios/checkout.art` prints what will run: each scenario of
the file as the flow with that scenario's values in its vars, every import gone
and every item call replaced by its step. A comment above each scenario names
the flow, where it is declared, and the scenario that ran it, and one above
each step an item call brought in names the item, where it is declared, and
the line that called it. For [the whole test](#a-whole-test), the third
scenario starts:

```text
# from checkout (flows/checkout.art:4) via scenarios/checkout.art:13
scenario "bad coupon" {
  tags = ["errors"]
  var sku = "A-1"
  var quantity = 1
  var coupon = "NOPE"
  var code = 422
  var url = env("API_URL")

  setup {
    # from auth.login (collections/auth.art:4) via flows/checkout.art:13
    step "log in" {
      post "${url}/token" {
        header "Content-Type" = "application/json"
        body = {"username": "alice", "password": env("API_PASSWORD")}
      }
      expect status == 200
      var token = body.data.access_token
    }
  }

  step "pay" {
    post "${url}/cart/pay" {
      header "Authorization" = "Bearer ${token}"
      body = {"sku": sku, "quantity": quantity, "coupon": coupon}
    }
    timeout = "5s"
    retry { times = 3, delay = "2s" }

    expect status == code
    var paid = status
  }

  if paid == 200 {
    step "the order is there" {
      get "${url}/orders/latest" {
        header "Authorization" = "Bearer ${token}"
        query "limit" = 1
      }
      expect status == 200
      expect body.items[0].sku == sku
      expect body.email matches /.+@.+/
      expect body.id is number
    }
  } else {
    step "the error says why" {
      get "${url}/errors/last" { header "Authorization" = "Bearer ${token}" }
      expect body.code exists
    }
  }
}
```

`total = skip` has removed the var and the two `expect`s that read it, and the
private `url` is a plain var: what runs has no private var. The output is for
reading: a scenario that holds its steps is not a form this language compiles.
The file is checked first, exactly as `artemis run` checks it, and a file with
errors prints nothing; a flow file is `flow-not-runnable`, as it is for
`artemis run`. `-o <out.art>` writes the result to a file instead, and
refuses to replace one that is there unless `--force` is given.

A failure in an expanded step names the file its line was written in -- the
flow's or the collection's, not the scenario file's -- on the console, in the
reports, in `--events` and in `--trace`. The JSON report's step and assertion
entries carry that file as `file`; see [The JSON report](#the-json-report).

---

## What is in scope

An expression can name three things: the step's own observation, the flow's
vars, and whatever an earlier step wrote.

What the observation binds depends on the step's type:

| Step type | Bound roots |
| --- | --- |
| `api` | `status`, `body`, `raw`, `headers`, `cookies` |
| `terminal` | `exit_code`, `stdout`, `stderr` |
| `browser` | `page.url`, `page.title`, `text()`, `value()`, `attr()`, `count()`, `visible()` -- [specified with the step type](#what-a-browser-step-binds) |
| `db` | `rows`, `affected`, `result` -- [specified with the step type](#what-a-db-step-binds) |
| every step | `duration_ms` |

| Root | Type | What it is |
| --- | --- | --- |
| `status` | number | The HTTP status code |
| `body` | object, array or null | The response body parsed as JSON, and `null` when it was not JSON |
| `raw` | string | The response body as unparsed text. This is what a regex reads when the body is not JSON |
| `headers` | object | The response headers, keyed by name. Lookup is case-insensitive |
| `cookies` | object | The cookies this response set, keyed by name. Each is `{value, domain, path, expires, secure, http_only, same_site}`; an attribute the response did not send is `null`, and `expires` is an RFC 3339 time in UTC. See [cookies](#cookies) |
| `duration_ms` | number | How long the attempt took, in milliseconds: for an api step, from sending the request to having read the whole body; for a terminal step, the command's run; for a browser step, its actions. Bound in every step, and in no flow var's value |
| `exit_code` | number | The command's exit code |
| `stdout`, `stderr` | string | The two streams, as text |
| `page.url` | string | The page's current address |
| `page.title` | string | The document's title |
| `rows` | array | The rows a db step's query returned, each an object keyed by column name |
| `affected` | number or null | How many rows a db step's statement changed, or its query returned |
| `result` | any | A db step's command reply, for MongoDB and Redis |

Naming a root that the step's type does not bind is a **compile error**, and the
message lists the roots that are in scope:

```
flows/release.art:31:10: "status" is not in scope in a terminal step
   31 |   expect status == 200
      |          ^^^^^^
   hint: a terminal step binds exit_code, stdout, stderr, duration_ms
```

### The context

Each run of a flow has one set of named values, its **context**. A name gets
into it in two ways:

1. **A flow var**: its default, or the value the scenario gives it.
2. **A step**: a [step var](#step-vars), written after the action of a step in
   the flow or brought in as an item's output, creates a name or overwrites
   one.

Every name in the context above a step is in scope in it. Naming a value a
later step writes, or one this step writes, is a compile error -- the value
does not exist yet.

**The last write wins.** A step that writes a name that already exists
overwrites it, and the steps after it read the new value. A scenario sets
`code = 400`; a step says `var code = status` and the server answers 201; from
that step on, `code` is 201. An item that reads `code` through the context
after that step is given 201 too.

The one error is a **duplicate declaration in the same place**: two vars of one
name at the top of a flow, or two inputs of one item, are `duplicate-binding`,
because both would be evaluated before any step runs and leave one value for
two lines.

**A write that fails** -- the response has no `body.data.access_token` --
**unsets the name**, even if it had a value before. A later step that reads it
fails, naming the step whose write failed, rather than quietly using the old
value. Teardown and a `finally` skip a step that reads an unset name, as they
skip one that reads a name no step wrote.

How each block changes this is in
[What a block lets the steps after it read](#what-a-block-lets-the-steps-after-it-read).

---

## Expressions

### Literals

| Form | Example |
| --- | --- |
| number | `200`, `-1`, `3.5` |
| string | `"ok"`, `"${url}/orders"` |
| regex | `/.+@.+/`, `/(?i)ready/` |
| boolean | `true`, `false` |
| null | `null` |
| array | `[1, 2, 3]`, `["admin", role]` |
| object | `{"username": "alice", "quota": limit}` |

An object's keys are strings. Trailing commas are permitted in both.

A regex literal is a [Go regular expression](https://pkg.go.dev/regexp/syntax)
between slashes. It is unanchored, so `/ok/` matches `"not ok"`. Flags go
inline: `(?i)` for case-insensitive, `(?s)` for a dot that matches newlines,
`(?m)` for `^` and `$` at line boundaries rather than at the ends of the subject.
A `/` inside the pattern is written `\/`. A pattern that will not compile is a
compile error.

### Paths

A value is reached with `.` and `[]`:

```art
expect body.data.items[0].name == "ABC-1"
expect headers["content-type"] contains "json"
expect body["total-count"] > 0
```

`.name` and `["name"]` are the same thing; the bracket form is what you use for a
key that is not an identifier. An index into an array takes a number.

A path that does not resolve against the actual observation is **not** a compile
error -- nothing knows the shape of the response until it arrives. It is an
[errored assertion](#the-four-kinds-of-failure) with the reason.

### Operators

| Operator | Checks | Errors when |
| --- | --- | --- |
| `==` | The two sides are equal. Numbers compare as numbers however they are written; arrays and objects compare element by element | never |
| `!=` | The two sides are not equal | never |
| `>`, `>=`, `<`, `<=` | The left side is a number greater than, at least, less than or at most the right | either side is not a number |
| `contains` | A string holds the right side as a substring, an array holds it as an element, or an object holds it as a key | the left side is not a string, array or object |
| `matches` | The left side, as a string, matches the regex on the right | the left side is not a string, or the right is not a regex |
| `exists` | The path on the left resolves to a value that is not null | never |
| `is <type>` | The left side's JSON type is the named one | the name is not a type |
| `not <expr>` | The expression is false | never |
| `and`, `or` | Both sides, or either side | never |

The seven type names for `is`: `string`, `number`, `boolean`, `object`, `array`,
`null`, and `file` -- the type of a value [`file()`](#data-and-files) makes.

```art
expect status == 200
expect status != 500
expect body.data.count > 0
expect body.data.roles contains "admin"
expect body.data.email matches /.+@.+/
expect body.data.token exists
expect not body.data.error exists
expect body.data.count is number
expect status == 200 and body.data.ok == true
```

`not` binds looser than `exists` and `is`, so `not body.x exists` means
`not (body.x exists)` -- the only reading anyone wants. It binds tighter than
`and` and `or`.

Precedence, loosest first:

```
or
and
not
== != < <= > >= contains matches, and the exists / is predicates
unary -
. and [] 
```

Parentheses group: `expect (a == 1 or a == 2) and b exists`.

### Builtins

| Builtin | Returns |
| --- | --- |
| `env("NAME")` | The environment variable `NAME`. A variable that is not set, that is empty, or that holds only space characters has no value, and a call with no default is an error |
| `env("NAME", "default")` | The same, with `default` when `NAME` has no value. `env("FLAG", "")` is how a scenario asks for an empty value |
| `match(<text>, /re/)` | The text the regex matched: capturing group 1 when the pattern has one, the whole match when it does not |
| `uuid()`, `now()`, `random_int()`, `random_string()` | A value the run makes up: [test data](#making-and-converting-values) |
| `base64()`, `base64_decode()`, `url_encode()`, `sha256()`, `hmac_sha256()`, `jwt_claims()` | An encoding, a hash or a token's claims: [test data](#making-and-converting-values) |
| `to_json()`, `from_json()`, `len()`, `number()`, `string()` | A value turned into another: [test data](#making-and-converting-values) |
| `text()`, `value()`, `attr()`, `count()`, `visible()` | What an element on the page says -- [in a `browser` step only](#what-a-browser-step-binds) |

`env()`, `match()` and the value builtins are callable in every scope,
including a `var`'s value. The five element functions are callable in a
`browser` step and nowhere else: calling one anywhere else is a compile error
naming what is in scope there.

`env()` is an ordinary expression and is legal wherever an expression is -- in a
`var`, in a URL, in a header, in a `body`, in an `expect`.

Environment variables come from the process environment and from the `.env` file
in the working directory, or from the file given with `-e`. A file named with
`-e` that cannot be read stops the run with an error that names the file, before
any step; a missing default `.env` is silent.

Before the first step of a run, artemis reads every `env()` call whose name is
a plain string literal and reports every one of those variables that has no
value, in one message, and runs nothing. The check covers the whole run and not
one file at a time: a file that will not compile is a fault in that file, and
an absent variable is a fault in the environment every file of the run shares.

A name artemis cannot read before the run -- `env(which)`,
`env("${prefix}_URL")` -- is reported when the step reaches it, as a step
error, with the same reason. `env("")` is a compile error: no environment can
supply a variable with no name.

The check reads the files that compiled. A file that will not compile
contributes none of its `env()` names, so a run of a broken file beside a good
one runs the good one and reports the absent variable only once the broken file
compiles -- two runs for two faults. A file that will not compile is already
reported, with its line, and correcting it is the next move either way.

```dotenv
API_URL=https://localhost:8000
API_PASSWORD=my_secret_key
```

**Spec decision.** `match()` is how a regex capture is written. It is the one
addition this spec makes to the design's builtins, and it exists because the
design adopts the old format's regex captures wholesale without giving a way to
spell one:

```art
var sha      = match(stdout, /^([0-9a-f]{40})/)   # group 1
var itemId   = match(raw, /\/items\/([0-9]+)/)    # group 1
var whole    = match(stdout, /v[0-9.]+/)          # the whole match
```

Five things about it, each settled here because the design document left the
surface syntax unwritten:

| | |
| --- | --- |
| Which group | **Group 1** when the pattern has one capturing group, the **whole match** when it has none |
| More than one group | A **compile error**. Make the ones you do not want non-capturing: `(?:...)` |
| No match | An **errored assertion** naming the pattern. Not an empty string, and not a panic |
| Type | Always a **string** |
| Where it is legal | Anywhere an expression is, exactly like `env()` |

A pattern with two or more capturing groups has an author who meant one of
them, and guessing which is how a value goes quietly wrong -- so the checker
rejects it rather than taking the first, and names the rewrite:

```art
var id = match(raw, /\/(items|orders)\/([0-9]+)/)      # error: two groups
var id = match(raw, /\/(?:items|orders)\/([0-9]+)/)    # one group, group 1
```

The pattern is counted at compile time when it is a regex literal, which it
almost always is. A pattern that arrives as a string in a variable is counted
at run time and gives the same reason as an errored assertion.

`match()` returns a string -- the text that matched, not a guess at what it
meant, because reading `"007"` as seven loses data. So `var itemId =
match(raw, /\/items\/([0-9]+)/)` against `moved to /items/42` makes `itemId`
the string `"42"`: `itemId is string` is true and `itemId is number` is false,
and `"${itemId}"` renders `42` either way.

A pattern that matches nothing is an evaluation that could not be performed, not
a value: in a step var it is one errored assertion naming the var, and in
an `expect` it is an errored assertion naming the expression. It is not an
*absent path*, so `expect match(raw, /x/) exists` reports the reason rather than
answering false -- the text was there and the question was asked.

The first argument is the text to search and the second is the pattern. The
pattern may be a regex literal or a string, which is the same rule `matches`
follows, so a pattern can live in a `var`.

### Making and converting values

```art
flow sign_up {
  private var url = env("API_URL")
  var id    = uuid()
  var email = "user-${random_string(8)}@example.test"
  var basic = base64("admin:secret")

  step "create the user" {
    post "${url}/users" {
      header "Authorization" = "Basic ${basic}"
      header "X-Signature" = hmac_sha256(env("SIGNING_KEY"), to_json({"id": id}))
      body = {"id": id, "email": email, "created": now()}
    }
    expect status == 201
    expect len(body.roles) > 0
    expect jwt_claims(body.token).sub == id
  }
}
```

| Builtin | Returns |
| --- | --- |
| `uuid()` | A random version 4 UUID, as a string |
| `now()`, `now("<format>")` | The time in UTC. `"rfc3339"`, the default, is `2026-10-26T09:30:15Z`; `"date"` is `2026-10-26`; `"unix"` and `"unix_ms"` are seconds and milliseconds since the epoch, as numbers |
| `random_int(min, max)` | A whole number from `min` to `max`, both included |
| `random_string(n)` | `n` characters of `a`-`z` and `0`-`9`, `n` from 1 to 256 |
| `base64(text)` | Standard base64, padded |
| `base64_decode(text)` | The text base64 holds. Standard and URL-safe, padded or not, are all read |
| `url_encode(text)` | Query escaping: `a b&c` is `a+b%26c` |
| `sha256(text)` | The SHA-256 of the text's UTF-8 bytes, in lower-case hex |
| `hmac_sha256(key, text)` | The HMAC-SHA256 of the text under the key, in lower-case hex |
| `jwt_claims(token)` | A JWT's payload as an object, **without** checking its signature: a test asserts what a token says, and checking who signed it is the service's job |
| `to_json(value)` | The value as compact JSON, keys in order |
| `from_json(text)` | The value JSON text holds |
| `len(value)` | The characters in a string, the items in an array or the keys of an object |
| `number(value)` | A number, or a string that reads as one |
| `string(value)` | The text the value has in an interpolation: `string(x)` is `"${x}"` |

A literal argument a builtin cannot use is a compile error: `now("iso")` with
a did-you-mean, `random_int(10, 1)`, `random_int(1.5, 2)`, `random_string(0)`.
The same mistake arriving through a `var`, and every argument a builtin cannot
answer for -- `number("abc")`, `len(1)`, `base64_decode("%%")`, a token that is
not a JWT -- is an errored assertion with the reason.

A value is made where it is written and then kept. A `var` is evaluated once,
so `var id = uuid()` is one UUID for each run of the flow; a step's request is
evaluated once, so a [`retry`](#retry) resends the same `random_string()`; an
`expect` is evaluated on every attempt.

**Random values are reproducible.** Each scenario draws from its own generator,
seeded from the run's seed, the scenario's name and its file's path within what
was run -- not from the order scenarios run in, so `--jobs 8` draws what
`--jobs 1` does, and `artemis run scenarios` and `artemis run .` inside
`scenarios/` draw alike. A run that
drew a random value and failed prints its seed, and `artemis run --seed <n>`
draws the same values again. The [JSON report](#the-json-report) carries it as
`seed`. `now()` reads the clock and is not reproducible; assert on its shape.

### Data and files

```art
flow import_users {
  private var url = env("API_URL")
  var users = json("data/users.json")
  var plans = csv("data/plans.csv")

  step "upload the sheet" {
    post "${url}/imports" {
      multipart "sheet" = file("data/users.csv")
    }
    expect status == 202
    expect body.count == len(users)
  }

  step "seed the database" {
    run "psql" {
      args  = ["-v", "ON_ERROR_STOP=1"]
      stdin = file("fixtures/seed.sql")
    }
    expect exit_code == 0
  }
}
```

| Builtin | Returns |
| --- | --- |
| `json(path)` | The value a JSON file holds |
| `csv(path)` | A CSV file as an array of objects: the first row names the columns, each later row is an object of those names to its cells, and every cell is a string |
| `file(path)` | The file, as a value of type `file` |

A path is relative to **the file it is written in**: `json("data/users.json")`
in `flows/login.art` reads `flows/data/users.json`, one in a scenario's value
is read next to the scenario file, and a `file()` inside a collection's item is
found next to the collection, not next to the flow that called it. An absolute
path is itself.

A literal path is read **when the file compiles**, because the data is in the
same repository as the scenario: a path that names nothing is the compile error
`data-file-missing`, a JSON file that does not parse is `data-not-json` with its
line and column, and a CSV file with a row of the wrong width or a column named
twice is `data-not-csv`. A path built at run time -- `json(env("DATA"))` -- is
read when the step reaches it, and the same faults are errored assertions.

A `file` is a path, not its contents. **Where a step sends content, a file sends
its bytes; everywhere else it is its absolute path:**

| Written as | What is sent |
| --- | --- |
| `body = file(p)` | The file's bytes, labelled with a `Content-Type` from its extension unless a `header` sets one |
| `multipart "k" = file(p)` | A file part, with the file's name and a type from its extension |
| `stdin = file(p)` | The file's bytes, as the command's standard input |
| `upload "#x" = file(p)`, `[file(a), file(b)]` | The file, or the files, attached to the input |
| `base64(file(p))`, `sha256(file(p))`, `hmac_sha256(k, file(p))` | Computed over the file's bytes |
| `args = ["-f", file(p)]`, `"${file(p)}"` | The absolute path |

A file cannot go inside a JSON body -- `body = {"logo": file(p)}` is a step error
that names the two ways to send one: as a `multipart` part, or its contents as
`base64(file(p))`. `is file` asks whether a value is one, and two files are
equal when they are the same path. A trace records a file it sent by its path
and size, never its contents.

### How a value renders

Wherever a value becomes text -- inside a `"${ }"`, in a query parameter -- it
renders the way a scenario would have written it:

| Value | Renders as |
| --- | --- |
| a string | itself |
| a number | as written: `42`, not `42.000000` or `4.2e+01` |
| a boolean | `true` or `false` |
| null | `null` |
| an object or array | compact JSON |

---

## Interpolation

A double-quoted string may hold `${ <expr> }`, with any expression inside:

```art
var root = "${base}/${version}"

step "orders" {
  get "${root}/orders/${orderId}?since=${since}" {
    header "Authorization" = "Bearer ${token}"
  }
  expect status == 200
}
```

| | |
| --- | --- |
| Form | `"${ <expr> }"`, inside a double-quoted string. Surrounding spaces are ignored |
| Literal dollar | `\$` |
| Rendering | By the [rendering rules](#how-a-value-renders) above |
| Unclosed `${` | A compile error naming its position |
| An unknown name inside it | A compile error naming the name |

Both errors are caught when the file is read, before anything is sent. A typo'd
variable is never sent to a server as literal `${tokn}`.

---

## `expect`

One `expect` is one assertion:

```art
expect status == 200
expect body.data.count > 0
```

Those are two assertions in the report. This is one:

```art
expect status == 200 and body.data.ok == true
```

An `and` chain inside a single `expect` is still one assertion, so the line you
wrote and the assertion you read about are one to one. Two assertions means two
`expect` lines.

A failure's message is generated from the expression and the values it evaluated
to, so `expect body.data.count > 0` reports:

```
body.data.count > 0, got 0
```

### `within`

An assertion can be given time to come true:

```art
expect body.data.state == "ready" within "10s"
```

`within` re-evaluates **that single assertion** until it holds or the budget
expires. It is the right tool when something is already in flight and only one
condition has to settle.

| Step type | Default `within` |
| --- | --- |
| `browser` | 5s |
| `api`, `terminal`, `db` | none -- the assertion is evaluated once |

**Spec decision.** An explicit `within` on an `api` or a `terminal` step is
accepted and the assertion is still evaluated **once**. Those two observations
are complete when the step returns -- a response has arrived, a command has
exited -- so nothing a re-evaluation reads can have changed, and honouring the
budget literally would make a failure exactly `within` slower and no more likely
to pass. That is the same reason those types have no default. A budget that will
not parse is still a fault either way: as a compile error for a literal, and as
an errored assertion for one that came out of a `var`. What re-runs an `api` step
that is not ready yet is [`retry`](#retry).

In a browser step the waiting is real: the assertion is re-evaluated every 100ms
until it holds or the budget expires, reading the page again each time. An
assertion that is already true costs nothing -- the 5s default is a ceiling on a
failure, not a delay on a pass.

`within` and `retry` compose: a step may retry, and its assertions may wait.
They are not the same thing -- see [the three timers](#retry-timeout-and-within).

---

## Step vars

A step can pull a value out of what it produced and leave it for the steps after
it, with a `var` after its action:

```art
step "login" {
  post "${url}/token" {
    body = {"username": "alice", "password": pw}
  }
  expect status == 200
  var token = body.data.access_token
}

step "orders" {
  get "${url}/orders" {
    header "Authorization" = "Bearer ${token}"
  }
  expect status == 200
}
```

| | |
| --- | --- |
| Form | `var <ident> = <expr>`, below the step's action. Above it, it is `action-not-first`, as any statement above the action is |
| Scope | In the [context](#the-context) from the next step on, until a later step writes the name again |
| Step types | Every type. It is not an HTTP-only statement |
| Name | Any name, a flow var's included: [the last write wins](#the-context). In a collection, not a root of the step's observation (`root-capture`) |

The expression is evaluated against the same observation an `expect` sees, so
every root in scope is available:

```art
var id    = body.data.id                        # from a parsed JSON body
var sha   = match(stdout, /^([0-9a-f]{40})/)    # from a command's output
var type  = headers["content-type"]             # from a response header
var count = body.data.items[0].quantity
```

A step var **keeps its type**: a number stays a number, an object stays an
object, so it can be compared with `>` or templated into a `body` as a value.
[`match()`](#builtins) is the exception and is always a string, even when the
text it matched is all digits.

A step var is plumbing, not a check. One that is written records nothing -- a
run that printed a line per token would bury the assertions that matter. One
that cannot be written is a single **errored assertion** naming the var, and
the step fails. The step's other vars are still attempted, so two mistyped
paths take one run to find rather than two.

A write that fails **unsets the name**, even when it held a value before, so a
later step naming it fails for the honest reason that the value was never
written -- not on an older value that is quietly wrong. The failure names the
step whose write failed.

---

## `retry`, `timeout` and `within`

Three different clocks. Getting them confused is the most common mistake in a
flow, so:

| Statement | Scope | Default |
| --- | --- | --- |
| `retry { times, delay }` | Re-runs the **whole step**, action included | one attempt, no delay |
| `timeout = "<duration>"` | Bounds **one attempt** of the step | `30s` |
| `expect ... within "<duration>"` | Re-evaluates **one assertion**, re-reading the page | none for `api`, `terminal` and `db`; 5s for `browser` |

```art
step "wait for the import" {
  get "${url}/imports/${importId}"
  timeout = "5s"
  retry { times = 10, delay = "3s" }
  expect status == 200
  expect body.data.state == "done"
}
```

That step sends at most ten requests, gives each one five seconds, and sleeps
three seconds between them.

### `retry`

| Field | Meaning |
| --- | --- |
| `times` | The **total** number of attempts, not the number of retries after the first. `times = 3` sends at most three requests. Omitted, zero or negative means one attempt: a step is never attempted zero times |
| `delay` | A duration slept **between** attempts -- never before the first, never after the last. Omitted, there is no wait at all |

Retrying stops as soon as an attempt is worth stopping on: it ran, and every
assertion it made passed. A later attempt replaces an earlier one whole, so a
reported step is never a mix of two tries, and the report says how many
`attempts` it took.

### `timeout`

A duration string: `"500ms"`, `"5s"`, `"1m30s"`. It is **per attempt**, so a step
with `timeout = "5s"` and `retry { times = 3 }` may take fifteen seconds.

There is no spelling of "wait forever", by design: a request with no deadline is
how a CI job hangs until someone notices. `timeout = "0s"` is not that spelling
either -- zero means the 30s default, the same as writing no `timeout` at all.

A step that runs out of time is an **errored step** with the deadline named --
`no response within 5s` -- and is retried if the step asked for retries. A
duration that will not parse is a compile error.

---

## Reserved keywords

These are reserved and cannot be used as a name. Using one is a parse error
saying what it is reserved for, or, for the ones the language now uses, what it
does:

```
if  else  for  in  while  parallel  group  fn  return  import  use  let
setup  teardown  ai  skip
```

`if`, `else` and `parallel` open [blocks](#blocks); `switch`, `case`, `default`,
`try`, `catch`, `finally`, `repeat`, `until`, `every` and `max` are positional
keywords of the same blocks and are not reserved.

| Word | Purpose |
| --- | --- |
| `import` | Imports a collection file into a flow: `import "../collections/auth.art"`, or `import "../collections/Backend APIs.art" as api` |
| `setup`, `teardown` | Steps that run before a flow's own, and after them whatever happened -- see [setup and teardown](#setup-and-teardown) |
| `if`, `else`, `parallel` | Open a [block](#blocks): `if cond { ... } else { ... }`, `parallel { ... }` |
| `skip` | A scenario's value that turns off the checks reading a var -- see [`skip`](#skip) |
| `use`, `for`, `in` | How an item was called, and a scenario made one per row of data, before flows. A file that still uses them is a compile error that names `artemis migrate` -- see [The old forms](#the-old-forms) |
| `tags` | A scenario's tags: `tags = ["smoke"]` -- see [tags](#tags). Positional, not reserved, and not a flow var's name |

`ai` is reserved for a possible future agentic assertion. It is **not
implemented** and will not parse. A CI gate's most valuable property is
determinism, and an assertion that flakes for unreproducible reasons is worse
than a missing one.

The keywords the language actually uses are positional, not reserved:
`collection`, `request`, `command`, `query`, `flow`, `scenario`, `config`,
`var`, `private`, `step`, `run`, `browser`, `db`, `sql`, `expect`, `retry`,
`timeout`, `within`, `header`, `body`, `args`, `cwd`, `stdin`, `env`,
`times`, `delay`, `as`, `drop`, `expects` and `vars` (only after `drop`), the
seven HTTP verbs, and the operators `and`, `or`, `not`, `contains`, `matches`,
`exists`, `is`. Each means the keyword only where the grammar expects it, so
`var request = 1` and `var body_text = raw` are legal. The literals `true`,
`false` and `null` are the exception: they are never a name.

---

## Grammar

The complete grammar. `artemis grammar` prints the one the parser is built
from, with its lexical tokens and the rules the checker enforces after
parsing.

```ebnf
File          = CollectionFile | FlowFile | ScenarioFile ;
Import        = "import" String [ "as" Ident ] ;

CollectionFile = "collection" String "{" { ItemDecl } "}" ;
ItemDecl      = ( "request" | "command" | "query" ) Ident
                "{" { VarDecl } Action { StepStmt } "}" ;

FlowFile      = { Import } FlowDecl ;
FlowDecl      = "flow" Ident "{" { ConfigDecl | VarDecl | StepDecl | ItemCall | PhaseDecl | Block } "}" ;
PhaseDecl     = ( "setup" | "teardown" ) BlockBody ;
Block         = "parallel" BlockBody
              | "if" Expr BlockBody { "else" "if" Expr BlockBody } [ "else" BlockBody ]
              | "switch" Expr "{" { "case" Expr BlockBody } [ "default" BlockBody ] "}"
              | "try" BlockBody [ "catch" BlockBody ] [ "finally" BlockBody ]
              | "repeat" { RepeatOption } BlockBody "until" Expr ;
RepeatOption  = ( "every" | "within" | "max" ) Expr ;
BlockBody     = "{" { StepDecl | ItemCall | Block } "}" ;
ItemCall      = "step" String "=" ItemRef [ "as" Ident ] "{" { CallLine } "}" ;
ItemRef       = Ident "." Ident ;
CallLine      = Ident "=" Expr
              | ReqField
              | "body" "." Ident { "." Ident } "=" Expr
              | "drop" ( "expects" | "vars" )
              | Expect ;

ScenarioFile  = FlowRef { DataScenario } ;
FlowRef       = "flow" String ;
DataScenario  = "scenario" String "{" { TagsDecl | Set } "}" ;
TagsDecl      = "tags" "=" "[" [ String { "," String } ] "]" ;
Set           = Ident "=" ( Expr | "skip" ) ;

ConfigDecl    = "config" Ident "{" [ Setting { Sep Setting } ] "}" ;
Setting       = Ident "=" Expr ;
VarDecl       = [ "private" ] "var" Ident [ "=" Expr ] ;
StepDecl      = "step" String "{" Action { StepStmt } "}" ;

Action        = Request | Run | Browser | DB ;
Request       = Method String [ "{" { ReqField } "}" ] ;
Method        = "get" | "post" | "put" | "patch" | "delete" | "head" | "options" ;
ReqField      = "header" String "=" Expr
              | "query"  String "=" Expr
              | "body"   "=" Expr
              | "form"   String "=" Expr
              | "multipart" String "=" Expr ;
Run           = "run" Expr [ "{" { RunField } "}" ] ;
RunField      = "args" "=" Array | "cwd" "=" Expr | "stdin" "=" Expr
              | "env" "{" [ Setting { Sep Setting } ] "}" ;
DB            = "db" Expr [ "{" { DBField } "}" ] ;
DBField       = "sql" "=" Expr | "args" "=" Array | "command" "=" Expr ;
Browser       = "browser" "{" { BrowserAct } "}" ;
BrowserAct    = "goto"   Expr
              | "click"  Expr
              | "fill"   Expr "=" Expr
              | "select" Expr "=" Expr
              | "press"  Expr
              | "hover"  Expr
              | "upload" Expr "=" Expr
              | "wait"   Expr ;

StepStmt      = Expect | StepVar | Retry | Timeout ;
Expect        = "expect" Expr [ "within" String ] ;
StepVar       = "var" Ident "=" Expr ;
Retry         = "retry" "{" [ RetryField { Sep RetryField } ] "}" ;
RetryField    = "times" "=" Int | "delay" "=" String ;
Timeout       = "timeout" "=" String ;
Sep           = "," | newline ;

Expr          = Or ;
Or            = And { "or" And } ;
And           = Not { "and" Not } ;
Not           = "not" Not | Cmp ;
Cmp           = Unary [ BinOp Unary | "exists" | "is" TypeName ] ;
BinOp         = "==" | "!=" | "<" | "<=" | ">" | ">="
              | "contains" | "matches" ;
TypeName      = "string" | "number" | "boolean" | "object" | "array" | "null" | "file" ;
Unary         = "-" Unary | Postfix ;
Postfix       = Primary { "." Ident | "[" Expr "]" } ;
Primary       = Number | String | Regex | Bool | "null" | Ident
              | Object | Array | Call | "(" Expr ")" ;
Call          = Ident "(" [ Expr { "," Expr } ] ")" ;
Object        = "{" [ String ":" Expr { "," String ":" Expr } ] "}" ;
Array         = "[" [ Expr { "," Expr } ] "]" ;
```

Comments are `#` to end of line. Trailing commas are permitted in objects and
arrays. Statements separate by newline; no semicolons.

The lines of an item call separate like statements: by a newline or a comma.
So do a scenario's sets, and several may share a line. A flow file holds one
flow and its imports, a collection file one collection, and a scenario file
one header and its scenarios ([The three files](#the-three-files)).

`BrowserAct`'s semantics -- which actions take a value, what `wait` waits for,
what a selector means -- are specified with [`browser` steps](#browser-steps)
and not here, because a grammar says the shape and not the meaning.

---

## The four kinds of failure

Artemis keeps four things apart everywhere -- on the terminal, in the reports,
and in what it decides to retry:

| | What it is | Examples |
| --- | --- | --- |
| **Compile error** | The file is not one artemis can run. Nothing executes | a parse error, an unknown field, an out-of-scope root, a reserved word, an unclosed `${`, an unknown name, a regex that will not compile, a duration that will not parse, an import or a flow header that does not resolve, an item call whose inputs or overrides do not fit, a scenario that sets a var its flow does not have |
| **Environment error** | The run cannot start: a variable an `env()` call needs has no value. Nothing executes | an absent `API_URL`, an `API_URL=` with no value |
| **Step error** | The step could not run, or could not be judged | a refused connection, a timeout, a command not on the `PATH`, SQL the database rejects, a `cwd` that does not exist, a path that did not resolve, `>` against an object, a step var that read nothing |
| **Assertion failure** | The step ran and gave the wrong answer | `status == 200` against a 404 |

A compile error is reported before anything executes, and **every** error in the
file is reported rather than only the first: a file with three mistakes takes one
run to fix. Nothing panics -- a malformed file produces diagnostics and a
non-zero exit, never a stack trace.

An environment error keeps both habits. Every absent variable of the whole run
is reported, in one block, before the first step -- so an incomplete
environment takes one run to correct rather than one run per variable.

The diagnostic bar is a compiler's:

```
flows/checkout.art:12:10: unknown field "statu"
   12 |   expect statu == 200
      |          ^^^^^
   hint: did you mean "status"?
```

The distinction between a step error and an assertion failure is worth caring
about: a flaky environment should not read as a broken API. `status` in every
report is `error` for the first and `fail` for the second.

An environment error is a fault of the run and not of a scenario, so it is the
*run* level that carries it: `status` is `error` and `error` holds the reason,
naming the variables. No scenario that compiled is started, so `scenarios` is
empty -- bar a file that would not compile, which is recorded as an errored
scenario whatever the environment says, because that fault was known first and
belongs to the file.

---

## The exit code

| Code | When |
| --- | --- |
| `0` | Everything passed. Every scenario loaded, every step ran, every assertion held |
| `1` | Anything else |

Anything else is: a failed assertion, an errored step, a scenario whose file
would not compile, a variable an `env()` call needs and the environment does not
supply, a bad flag, a path that matched no scenario file, and a report that could
not be written. There is no third code -- a caller that needs to tell
those apart reads the [report](#the-json-report), where the shape of the failure
is spelled out.

A report that could not be written fails the command **even when the run
passed**: a CI job whose artifact silently vanished is worse off than one that
went red.

```sh
artemis run scenarios/ && echo "everything passed"
```

---

## The JSON report

`--report json` writes the whole outcome of a run as one JSON document, for a CI
job or an agent to read. Its value is `format[=path]`:

```sh
artemis run scenarios/ --report json               # the document on stdout
artemis run scenarios/ --report json=results.json  # the document in a file
artemis run scenarios/ --report junit=junit.xml    # JUnit XML for a CI reporter
artemis run scenarios/ --report json --report junit=junit.xml   # both
```

With no path the document goes to stdout and the console report moves to stderr,
so stdout holds exactly one document and `artemis run scenarios/ --report json | jq`
works. With a path, the document is written there, truncating whatever was there
before, and the console report stays on stdout. A document is written whether the
run passed or failed. The exit code is unchanged by the flag.

The document mirrors the result tree -- a run of scenarios, a scenario of steps,
a step of assertions -- and holds everything that ran, passing things included,
so a reader can tell "nothing failed" from "nothing ran":

```json
{
  "schema_version": 1,
  "started_at": "2026-03-04T05:06:07Z",
  "duration_ms": 14,
  "status": "fail",
  "passed": false,
  "error": "",
  "counts": {
    "scenarios":  { "total": 1, "passed": 0, "failed": 1, "errored": 0, "skipped": 0 },
    "steps":      { "total": 1, "passed": 0, "failed": 1, "errored": 0, "skipped": 0 },
    "assertions": { "total": 2, "passed": 1, "failed": 1, "errored": 0, "skipped": 0 }
  },
  "scenarios": [
    {
      "name": "items",
      "file": "scenarios/items.art",
      "flow": "items",
      "status": "fail",
      "duration_ms": 13,
      "error": "",
      "steps": [
        {
          "name": "get item",
          "status": "fail",
          "duration_ms": 12.5,
          "attempts": 1,
          "line": 5,
          "file": "flows/items.art",
          "error": "",
          "assertions": [
            {
              "kind": "expect",
              "path": "status",
              "operator": "==",
              "expected": 200,
              "actual": 200,
              "status": "pass",
              "error": "",
              "line": 11,
              "file": "flows/items.art"
            },
            {
              "kind": "expect",
              "path": "body.data.state",
              "operator": "==",
              "expected": "ready",
              "actual": "pending",
              "status": "fail",
              "error": "",
              "line": 14,
              "file": "flows/items.art"
            }
          ]
        }
      ]
    }
  ],
  "failures": [
    {
      "file": "flows/items.art",
      "line": 14,
      "scenario": "items",
      "step": "get item",
      "status": "fail",
      "kind": "expect",
      "path": "body.data.state",
      "operator": "==",
      "expected": "ready",
      "actual": "pending",
      "error": ""
    }
  ]
}
```

| Key | Where | Meaning |
| --- | --- | --- |
| `schema_version` | run | `1`. It goes up when a key is removed or its meaning changes; a new key does not change it |
| `started_at` | run | When the run began, RFC 3339 |
| `duration_ms` | run, scenario, step | Milliseconds, to microsecond precision. Not nanoseconds, and not the seconds the JUnit report uses |
| `status` | run, scenario, step, assertion | `pass`, `fail`, `error` or `skip`. Every level above an assertion is the worst of its children, and a `skip` never drags a parent down. A step is skipped when a [setup](#setup-and-teardown) step before it did not pass, or when it is a teardown step reading a name no step wrote; its `error` says which |
| `phase` | step | `"setup"` or `"teardown"` for a step of a flow's [setup or teardown](#setup-and-teardown) block. **Omitted** on the flow's own steps |
| `round` | step | For a step of a [`repeat`](#blocks) block, which round its result is from: the last that ran. **Omitted** on every other step |
| `passed` | run | `false` if anything failed or errored. The same thing the exit code says, for a consumer that does not want to learn the vocabulary |
| `error` | run | Why the run could not proceed at all, and `""` for every run that reached its scenarios. An environment error is the one thing that sets it |
| `seed` | run | The seed the run's `uuid()`, `random_int()` and `random_string()` drew from, and absent when no scenario called one. Passed back as `artemis run --seed <n>`, it draws the same values |
| `counts` | run | Totals per level, each a `{total, passed, failed, errored, skipped}` tally, so nobody walks the tree to say "2 of 5 assertions failed" |
| `total`, `passed`, `failed`, `errored`, `skipped` | each tally | How many of that level ended that way |
| `scenarios` | run | One entry per scenario the run reached, in run order |
| `tags` | scenario | The scenario's [tags](#tags). **Omitted** when it has none |
| `flow` | scenario | The name of the flow the scenario ran: `"checkout"`. **Omitted** when it ran none, as for a file that would not compile |
| `name` | scenario, step | As written: a scenario's title, a step's name. A scenario whose file would not compile has no name, so it is `""` and `file` identifies it |
| `file` | scenario, failure, step, assertion | On a scenario, the scenario file it was read from. On a failure, the file its `line` was written in: the flow's, or the [collection's](#collections) an item call brought the step in from. On a step or an assertion, the same, and **omitted** when it is the scenario's own file |
| `error` | scenario, step, assertion, failure | Why it could not run, or `""`. A scenario's error is a file that would not compile, and such a scenario has no steps |
| `steps` | scenario | One entry per step the scenario reached |
| `attempts` | step | How many times the step was tried; `1` unless `retry` asked for more |
| `line` | step, assertion, failure | The 1-based line it was written on, in the file `file` names, and `0` when artemis does not know |
| `assertions` | step | One entry per `expect` the step evaluated, plus one per [step var](#step-vars) that could not be written, in the order they were made |
| `kind` | assertion, failure | What made the assertion: `expect` for every `expect`, whatever its expression does, and `capture` for a step var that could not be written. There is no per-operator kind and no per-step-type one |
| `path` | assertion, failure | The source text of what was inspected: `body.data.count`. For an expression with no single subject -- an `or` chain, a bare call -- the whole expression; for a step var, its name |
| `operator` | assertion, failure | The expression's top-level comparison token -- `==`, `contains`, `exists`, `is`, or `not` prefixed to one of them -- and `""` for an expression with no single one |
| `expected`, `actual` | assertion, failure | The two sides as evaluated, each keeping its JSON type. `null` when there was no such value -- a path that did not resolve |
| `failures` | run | Everything the run says to go and fix, flat and in run order. One entry per failing assertion, per step that could not run, and per file that would not compile, each naming its own `file`, `line`, `scenario` and `step` so an entry stands alone. `[]` for a run that passed |
| `scenario`, `step` | failure | The names of the two things the failure sits under, repeated so the entry stands alone |
| `trace` | step | The path to the file holding what the step sent and what it saw, and `""` unless `--trace` asked for one. The path is as the run was given it, so a CI job uploading it uses the string verbatim. See [Tracing a step](#tracing-a-step) |
| `screenshot` | step, failure | The path to a picture of the page, for a [browser step that did not pass](#when-a-browser-step-fails), and `""` for every other step. The path is as the run was given it -- relative to the working directory unless `--screenshots` named an absolute one -- so a CI job uploading it uses the string verbatim. Repeated on the failure entry so the entry stands alone |

Every key is always present, with its zero value rather than omitted, so a `jq`
expression never has to tell absent from empty. `expected` and `actual` are one
exception: they are `null`, because either may legitimately be any JSON type. A
step's and an assertion's `file` is the other: it is absent unless the line is in
another file than the scenario's -- which, for a scenario of a scenario file,
it always is. The `--events` stream's step results carry it
the same way.
Every list is a list, empty rather than `null`.

**Spec decision.** `kind`, `path` and `operator` are what they are above because
an `expect` is an expression and has no single path or operator the way the old
`operator:`/`value:`/`path:` record did. `kind` names the statement rather than
the step type: a type is a property of the step, and repeating it on every
assertion under that step says nothing new. `path` and `operator` are read off
the expression -- the subject's source text, and the comparison when there is
exactly one. The schema does not change: a consumer reading `path` still gets
the one string that identifies what was checked.

`pkg/cli/testdata/art/report_json.golden` is a whole document from a real run,
and `pkg/shared/spec_test.go` holds this section to it -- both the keys and the
`kind` vocabulary -- so a report that gained a key without gaining a row here
fails the build.

What is **not** in the document is anything artemis does not record: no request
bodies, no response bodies and no headers. The line a failure came from it does
record.

`--report junit` writes the same run as JUnit XML, where a scenario is a
`<testsuite>` and a step is a `<testcase>` whose `classname` is the scenario's
file. Its `time` attribute is in **seconds**, not the milliseconds of
`duration_ms`. [docs/reports.md](docs/reports.md#junit-xml) specifies that dialect.

---

## The toolchain

Five commands read the language without running any of it, and they read it
through the same front end `artemis run` does -- the same lexer, the same
parser, the same checker -- so none of them can disagree with this document
about what a file means. [docs/cli.md](docs/cli.md) specifies their flags
and their output.

| Command | What it does |
| --- | --- |
| `artemis parse -f x.art` | Every diagnostic the file produces, and a non-zero exit if any is an error. `--json` writes them as one document instead, for an editor or a UI |
| `artemis fmt [-w] x.art` | The file in canonical layout. Layout only: no string requoted, no expression reassociated, no comment dropped |
| `artemis ast -f x.art` | The syntax tree as JSON, with every node's span and every step's inferred type. `--from-json` goes the other way, tree to source |
| `artemis expand x.art [-o out.art] [--force]` | What will run: each scenario of a scenario file as its flow with the scenario's values, every item call replaced by its step, each preceded by a comment naming where it came from. See [`artemis expand`](#artemis-expand) |
| `artemis grammar` | The grammar below, with its precedence and its post-parse rules, as one piece of text. `--json` gives the enumerable choice points -- methods, operators, type names, block fields, reserved words, diagnostic codes -- read out of the tables the parser and the checker themselves consult |

A test is converted into this language once, never translated on the fly:
`artemis migrate -f old.yaml -o new/` for a YAML scenario, which writes a flow
and a scenario file, `artemis migrate -f old.art -o new/` for a `.art` file in
[the old forms](#the-old-forms), and `artemis
generate -f collection.json -o x.art` for a Postman collection, which writes a
collection, a flow and a scenario file. All of them write through `artemis
fmt`'s printer, so what they produce is already canonical.

---

## Not in this spec

Everything here is deliberate. A file that uses one of these does not run.

Reuse of requests is [collections](#collections), whose items expand into plain
steps before anything runs, and reuse of steps is a [flow](#inside-a-flow) that
scenarios run with other data. There is still no runtime function or module
concept. Copying the same login into every file was the reuse an author needed
most, it needs no real abstraction, and expansion delivers it without adding
one: after expansion the language is unchanged.

| | |
| --- | --- |
| **A flow calling a flow** | A flow calls collection items, and nothing else. Steps two flows share are an item, or a `setup` written in each |
| **Scenarios from a data file** | A scenario's values are written in its scenario file. There is no `for row in json(...)`: each row is a scenario, written out |
| **Masking credentials in reports** | A report prints both operands of a failed `expect`, so one against a password prints the password -- on the console and in `report.json`. Withholding a credential without new syntax is an open item on [the roadmap](ROADMAP.md) |
| **More control flow** | `while` and `group` stay reserved. Looping is a bounded [`repeat`](#polling) |
| **Functions** | `fn`, `return`, `let`. Reserved words. A general-purpose language -- a test framework an AI assistant can translate a flow into -- is the answer to "I need real abstraction"; what runs before and after a flow's steps is [setup and teardown](#setup-and-teardown) |
| **Agentic assertions** | `ai`. Reserved, not implemented, and a parse error with a pointer |
| **Parallelism outside a block** | A flow's steps run one after another unless a [`parallel`](#blocks) block says otherwise; scenarios in one file run in order, and files at once only with `--jobs` |
| **A `wait_until` statement** | Waiting for a *condition* is per assertion and is spelled [`within`](#within), which names the condition being waited for. A browser block's [`wait "1s"`](#browser-steps) is a fixed pause and not a condition. There is no step-level wait: re-running a whole step is what [`retry`](#retry) is for, and the two are not interchangeable |
| **Request and response detail in a report** | Neither report carries a body or a header |
| **YAML** | Not a runtime format. See below |

---

## The old forms

`.art` files written before flows have a scenario that holds its own steps, and
forms this language no longer has. Each is a compile error whose hint says
what replaced it and that `artemis migrate -f <file> -o <dir>` rewrites the
file:

| Old form | What replaces it |
| --- | --- |
| `scenario "x" { steps }`, in a file with no `flow "..."` header | A flow file, and a scenario file that names it |
| `capture x = ...` | `var x = ...` after the step's action: a [step var](#step-vars) |
| `use a.b { ... }` | `step "<name>" = a.b { ... }`: [calling an item](#calling-an-item) |
| `drop captures` | `drop vars` |
| `request name(params)` | `var`s above the item's action: [its inputs](#a-collection-file) |
| `suite.art` and `suite { }` | Folders and `--tags`. A suite's setup, which ran once per run, is a `setup` in each flow, which runs once per scenario |
| `secret var`, `secret capture`, `secret` parameters, `for secret` | Nothing, for now: see **Masking credentials in reports** above |
| `scenario "..." for row in json(...)` | A scenario for each row, written out |

[docs/migrating.md](docs/migrating.md#old-art-files) shows what `artemis
migrate` writes for each.

---

## Coming from YAML

YAML survives as an input to `artemis migrate -f old.yaml`, which is the only
YAML reader left in the binary. Nothing runs it. Six habits do not carry over:

| In YAML | Here |
| --- | --- |
| `type: api` on a step | Nothing. The step's type comes from its action block |
| `{{name}}` | `"${name}"`, and the braces hold a whole expression |
| `{{env.NAME}}`, legal only in a variable's `value:` | `env("NAME")`, legal wherever an expression is |
| `operator:` / `value:` / `type:` as three sibling keys | One expression: `expect body.x > 0` |
| An unknown variable failing the step at run time | A compile error, before anything runs |
| `operator: empty` on a stream | `expect stderr matches /^\s*$/`. `== ""` is the check that is right in theory and wrong in practice, because almost every command ends its output with a newline |
