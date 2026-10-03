# The Artemis scenario language

This is the normative reference for `.art`, the language artemis scenarios are
written in. It is written to be read start to finish by whoever -- or whatever --
is about to author a scenario: every section opens with an example you can copy
and then states the rules that example obeys.

If this document and anything else disagree, this document is right.

> **Status.** `artemis run` reads this language. The front end -- lexer, parser,
> checker, printer -- is built, and every example in this document is compiled by
> `pkg/shared/spec_test.go` on every CI run: an example here either works or the
> build is red.
>
> The language was designed and approved in `docs/artemis-dsl-design.md`; four
> places where this spec decides something that design left open are marked
> **spec decision**. All three step types are specified here; `browser` steps
> compile and format but do not run yet -- see [`browser` steps](#browser-steps).
> YAML is not a runtime format: it is an input to `artemis migrate`, and the
> [README](README.md) documents the commands.

---

## A whole scenario

```art
scenario "checkout" {
  var url = env("API_URL")
  var pw  = env("API_PASSWORD")

  step "seed the database" {
    run "psql" {
      args = ["-f", "seed.sql"]
      cwd  = "db"
      env  { PGPASSWORD = pw }
    }
    expect exit_code == 0
    expect stdout contains "COPY 42"
  }

  step "login" {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"username": "alice", "password": pw}
    }
    expect status == 200
    capture token = body.data.access_token
  }

  step "orders" {
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
    expect body.data.count is number
  }
}
```

Everything in this document is a rule that example follows.

---

## The file

A scenario file has the extension `.art` and holds one or more scenarios:

```art
scenario "first" {
  step "ping" {
    get "https://example.com/health"
    expect status == 200
  }
}

scenario "second" {
  step "ping again" {
    get "https://example.com/health"
    expect status == 200
  }
}
```

| | |
| --- | --- |
| Extension | `.art` |
| Top level | One or more `scenario "<name>" { ... }` blocks, run in the order they are written |
| Comments | `#` to the end of the line |
| Statement separator | A newline, or a comma inside `{ }` where the grammar allows one. No semicolons |
| Trailing commas | Permitted in objects and arrays |
| Encoding | UTF-8 |

Nothing is shared between scenarios: each gets its own variables, so what one
captures is invisible to the next. Nothing is shared between files either.

---

## Inside a scenario

A scenario holds three kinds of declaration, in any order:

```art
scenario "orders" {
  config browser { headless = true }   # settings for a step type

  var url = env("API_URL")             # a value the steps can name
  var limit = 10

  step "list" {                        # a thing to do, and what to expect of it
    get "${url}/orders?limit=${limit}"
    expect status == 200
  }
}
```

| Declaration | Form | What it is |
| --- | --- | --- |
| `config` | `config <ident> { <key> = <expr>, ... }` | Settings for one step type, named by the ident |
| `var` | `var <ident> = <expr>` | A value every step of the scenario can name |
| `step` | `step "<name>" { ... }` | One action and what to expect of it |

Steps run in the order they are written, one after another. There is no way to
ask for parallelism.

### `var`

A `var`'s value is any expression, evaluated once, before the first step runs:

```art
var base    = env("API_URL")
var version = "v2"
var root    = "${base}/${version}"
var retries = 3
var headers = {"Accept": "application/json"}
```

A `var` may name an earlier `var`, as `root` does. It may not name a later one,
and it may not name a `capture`: captures do not exist yet when variables are
evaluated. Both are compile errors.

### `config`

**Spec decision.** `config` takes the name of a step type and settings for it.
No setting is defined for `api` or `terminal` scenarios; the only block with keys
today is `config browser`, which belongs to the browser step type and is
specified with it -- see [`config browser`](#config-browser). A `config` block naming a step type that does not exist, or
setting a key that type does not define, is a compile error.

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

A step with no action block, and a step with two, are both compile errors. The
type is known before any name in the step is resolved, which is what makes
[scope](#what-is-in-scope) per step type.

Besides its action, a step may hold `expect`, `capture`, `retry` and `timeout`
statements, in any order and any number:

```art
step "orders" {
  get "${url}/orders"
  timeout = "5s"
  retry { times = 3, delay = "2s" }
  expect status == 200
  capture first = body.data[0].id
}
```

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
| `body` | `body = <expr>` | At most one. An object or array literal is serialised to JSON; a string is sent as it is |

A verb with no block -- `get "${url}/health"` -- sends no headers of its own and
no body, which is what you want for a health check.

### Bodies

`body` takes a value, not a string with values spliced into it:

```art
body = {"username": "alice", "roles": ["admin"], "quota": limit}
```

That is serialised to JSON. So a captured object reaches a body as a value:

```art
step "read the user" {
  get "${url}/users/1"
  expect status == 200
  capture user = body.data
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
  capture sha = match(stdout, /^([0-9a-f]{40})/)
  timeout = "5s"
}
```

| Field | Form | Rules |
| --- | --- | --- |
| the command | `run <expr>` | Required, and the first thing in the block. The program to execute |
| `args` | `args = [<expr>, ...]` | At most one. Each entry arrives at the command exactly as written |
| `cwd` | `cwd = <expr>` | At most one. Relative to where artemis was run from, **not** to the scenario file |
| `stdin` | `stdin = <expr>` | At most one. Text written to the command's standard input |
| `env` | `env { <NAME> = <expr>, ... }` | At most one. Variables added to the command's environment |

**There is no shell.** The command is executed directly with its arguments: no
word splitting, no globbing, no `~` expansion. `run "ls *.go"` looks for a binary
with a space in its name and does not find one. A scenario that wants a pipeline
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

A whole scenario of terminal steps:

```art
scenario "release checks" {
  var tag = env("RELEASE_TAG")

  step "the tag exists" {
    run "git" { args = ["rev-parse", "--verify", "${tag}^{commit}"] }
    expect exit_code == 0
    expect stderr matches /^\s*$/
    capture sha = match(stdout, /^([0-9a-f]{40})/)
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
scenario "upgrade to pro" {
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
    capture plan = match(text(".plan-badge"), /plan: (\w+)/)
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
| `upload` | `upload "#avatar" = "me.png"` | Attaches a file to a file input |
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

It is scenario-wide: one browser, one context, shared by every browser step of
that scenario in the order they are written, so a step can sign in and the next
step is still signed in. A scenario with no browser step opens no browser.

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

The name is `<scenario>-<step>.png`, lower-cased with everything that is not a
letter or a digit collapsed to a hyphen, and it has no timestamp: a rerun
overwrites the file from the run before it, so the path in the report is
predictable enough for a CI job to name the artifact it is about to upload. Two
steps that share a name in one run get `-2` and `-3`. The folder is created only
when a browser step actually fails, so a suite of `api` steps never grows one.

---

## What is in scope

An expression can name three things: the step's own observation, the scenario's
variables, and whatever an earlier step captured.

What the observation binds depends on the step's type:

| Step type | Bound roots |
| --- | --- |
| `api` | `status`, `body`, `raw`, `headers` |
| `terminal` | `exit_code`, `stdout`, `stderr` |
| `browser` | `page.url`, `page.title`, `text()`, `value()`, `attr()`, `count()`, `visible()` -- [specified with the step type](#what-a-browser-step-binds) |

| Root | Type | What it is |
| --- | --- | --- |
| `status` | number | The HTTP status code |
| `body` | object, array or null | The response body parsed as JSON, and `null` when it was not JSON |
| `raw` | string | The response body as unparsed text. This is what a regex reads when the body is not JSON |
| `headers` | object | The response headers, keyed by name. Lookup is case-insensitive |
| `exit_code` | number | The command's exit code |
| `stdout`, `stderr` | string | The two streams, as text |
| `page.url` | string | The page's current address |
| `page.title` | string | The document's title |

Naming a root that the step's type does not bind is a **compile error**, and the
message lists the roots that are in scope:

```
checkout.art:31:10: "status" is not in scope in a terminal step
   31 |   expect status == 200
      |          ^^^^^^
   hint: a terminal step binds exit_code, stdout, stderr
```

Every `var` of the scenario and every `capture` of an **earlier** step is in
scope in every step, by name. Naming a capture from a later step, or from this
step, is a compile error -- the value does not exist yet.

A name that is in scope twice is not possible: a `capture` may not reuse the name
of a `var` or of an earlier capture, because silently shadowing a value is how a
scenario comes to assert against the wrong one.

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
[errored assertion](#the-three-kinds-of-failure) with the reason.

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

The six type names for `is`: `string`, `number`, `boolean`, `object`, `array`,
`null`.

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
| `env("NAME")` | The environment variable `NAME`, or `""` when it is not set |
| `match(<text>, /re/)` | The text the regex matched: capturing group 1 when the pattern has one, the whole match when it does not |
| `text()`, `value()`, `attr()`, `count()`, `visible()` | What an element on the page says -- [in a `browser` step only](#what-a-browser-step-binds) |

`env()` and `match()` are callable in every scope, including a `var`'s value.
The five element functions are callable in a `browser` step and nowhere else:
calling one anywhere else is a compile error naming what is in scope there.

`env()` is an ordinary expression and is legal wherever an expression is -- in a
`var`, in a URL, in a header, in a `body`, in an `expect`. An unset name is the
empty string rather than an error: an absent variable is how a scenario says "no
token".

Environment variables come from the process environment and from the `.env` file
in the working directory, or from the file given with `-e`. A file named with
`-e` that cannot be read is a warning; a missing default `.env` is silent.

```dotenv
API_URL=https://localhost:8000
API_PASSWORD=my_secret_key
```

**Spec decision.** `match()` is how a regex capture is written. It is the one
addition this spec makes to the design's builtins, and it exists because the
design adopts the old format's regex captures wholesale without giving a way to
spell one:

```art
capture sha      = match(stdout, /^([0-9a-f]{40})/)   # group 1
capture itemId   = match(raw, /\/items\/([0-9]+)/)    # group 1
capture whole    = match(stdout, /v[0-9.]+/)          # the whole match
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
them, and guessing which is how a capture goes quietly wrong -- so the checker
rejects it rather than taking the first, and names the rewrite:

```art
capture id = match(raw, /\/(items|orders)\/([0-9]+)/)      # error: two groups
capture id = match(raw, /\/(?:items|orders)\/([0-9]+)/)    # one group, group 1
```

The pattern is counted at compile time when it is a regex literal, which it
almost always is. A pattern that arrives as a string in a variable is counted
at run time and gives the same reason as an errored assertion.

`match()` returns a string -- the text that matched, not a guess at what it
meant, because reading `"007"` as seven loses data. So `capture itemId =
match(raw, /\/items\/([0-9]+)/)` against `moved to /items/42` makes `itemId`
the string `"42"`: `itemId is string` is true and `itemId is number` is false,
and `"${itemId}"` renders `42` either way.

A pattern that matches nothing is an evaluation that could not be performed, not
a value: in a `capture` it is one errored assertion naming the capture, and in
an `expect` it is an errored assertion naming the expression. It is not an
*absent path*, so `expect match(raw, /x/) exists` reports the reason rather than
answering false -- the text was there and the question was asked.

The first argument is the text to search and the second is the pattern. The
pattern may be a regex literal or a string, which is the same rule `matches`
follows, so a pattern can live in a `var`.

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
| `api`, `terminal` | none -- the assertion is evaluated once |

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

## `capture`

A step can pull a value out of what it produced and leave it for the steps after
it:

```art
step "login" {
  post "${url}/token" {
    body = {"username": "alice", "password": pw}
  }
  expect status == 200
  capture token = body.data.access_token
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
| Form | `capture <ident> = <expr>` |
| Scope | Available by name in every later step of the same scenario, and nowhere else |
| Step types | Every type. It is not an HTTP-only statement |
| Name | May not reuse a `var`'s name or an earlier capture's |

The expression is evaluated against the same observation an `expect` sees, so
every root in scope is available:

```art
capture id    = body.data.id                        # from a parsed JSON body
capture sha   = match(stdout, /^([0-9a-f]{40})/)    # from a command's output
capture type  = headers["content-type"]             # from a response header
capture count = body.data.items[0].quantity
```

A captured value **keeps its type**: a number stays a number, an object stays an
object, so it can be compared with `>` or templated into a `body` as a value.
[`match()`](#builtins) is the exception and is always a string, even when the
text it matched is all digits.

A capture is plumbing, not a check. One that succeeds records nothing -- a run
that printed a line per captured token would bury the assertions that matter, and
a captured value is often a secret. One that fails is a single **errored
assertion** naming the capture, and the step fails. The other captures of the
step are still attempted, so two mistyped paths take one run to find rather than
two.

A failed capture writes nothing, so a later step naming it fails for the honest
reason that the value was never captured -- not on a value that is quietly wrong.

---

## `retry`, `timeout` and `within`

Three different clocks. Getting them confused is the most common mistake in a
scenario, so:

| Statement | Scope | Default |
| --- | --- | --- |
| `retry { times, delay }` | Re-runs the **whole step**, action included | one attempt, no delay |
| `timeout = "<duration>"` | Bounds **one attempt** of the step | `30s` |
| `expect ... within "<duration>"` | Re-evaluates **one assertion**, re-reading the page | none for `api` and `terminal`; 5s for `browser` |

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
saying what it is reserved for:

```
if  else  for  in  while  parallel  group  fn  return  import  use  let
setup  teardown  ai
```

`ai` is reserved for a possible future agentic assertion. It is **not
implemented** and will not parse. A CI gate's most valuable property is
determinism, and an assertion that flakes for unreproducible reasons is worse
than a missing one.

The keywords the language actually uses are reserved too: `scenario`, `config`,
`var`, `step`, `run`, `browser`, `expect`, `capture`, `retry`, `timeout`,
`within`, `header`, `query`, `body`, `args`, `cwd`, `stdin`, `env`, `times`,
`delay`, the seven HTTP verbs, the operators `and`, `or`, `not`, `contains`,
`matches`, `exists`, `is`, and the literals `true`, `false`, `null`.

---

## Grammar

The complete grammar, carried over from the approved design
(`docs/artemis-dsl-design.md`) rather than paraphrased, because a paraphrase of a
grammar is how two grammars come to disagree.

```ebnf
File        = { Scenario } ;
Scenario    = "scenario" String "{" { ConfigDecl | VarDecl | StepDecl } "}" ;
ConfigDecl  = "config" Ident "{" [ Setting { Sep Setting } ] "}" ;
Setting     = Ident "=" Expr ;
VarDecl     = "var" Ident "=" Expr ;
StepDecl    = "step" String "{" Action { StepStmt } "}" ;

Action      = Request | Run | Browser ;
Request     = Method String [ "{" { ReqField } "}" ] ;
Method      = "get" | "post" | "put" | "patch" | "delete" | "head" | "options" ;
ReqField    = "header" String "=" Expr
            | "query"  String "=" Expr
            | "body"   "=" Expr ;
Run         = "run" Expr [ "{" { RunField } "}" ] ;
RunField    = "args" "=" Array | "cwd" "=" Expr | "stdin" "=" Expr
            | "env" "{" [ Setting { Sep Setting } ] "}" ;
Browser     = "browser" "{" { BrowserAct } "}" ;
BrowserAct  = "goto"   Expr
            | "click"  Expr
            | "fill"   Expr "=" Expr
            | "select" Expr "=" Expr
            | "press"  Expr
            | "hover"  Expr
            | "upload" Expr "=" Expr
            | "wait"   Expr ;

StepStmt    = Expect | Capture | Retry | Timeout ;
Expect      = "expect" Expr [ "within" String ] ;
Capture     = "capture" Ident "=" Expr ;
Retry       = "retry" "{" [ RetryField { Sep RetryField } ] "}" ;
RetryField  = "times" "=" Int | "delay" "=" String ;
Timeout     = "timeout" "=" String ;
Sep         = "," | newline ;

Expr        = Or ;
Or          = And { "or" And } ;
And         = Not { "and" Not } ;
Not         = "not" Not | Cmp ;
Cmp         = Unary [ BinOp Unary | "exists" | "is" TypeName ] ;
BinOp       = "==" | "!=" | "<" | "<=" | ">" | ">="
            | "contains" | "matches" ;
TypeName    = "string" | "number" | "boolean" | "object" | "array" | "null" ;
Unary       = "-" Unary | Postfix ;
Postfix     = Primary { "." Ident | "[" Expr "]" } ;
Primary     = Number | String | Regex | Bool | "null" | Ident
            | Object | Array | Call | "(" Expr ")" ;
Call        = Ident "(" [ Expr { "," Expr } ] ")" ;
Object      = "{" [ String ":" Expr { "," String ":" Expr } ] "}" ;
Array       = "[" [ Expr { "," Expr } ] "]" ;
```

Comments are `#` to end of line. Trailing commas are permitted in objects and
arrays. Statements separate by newline; no semicolons.

`BrowserAct`'s semantics -- which actions take a value, what `wait` waits for,
what a selector means -- are specified with [`browser` steps](#browser-steps)
and not here, because a grammar says the shape and not the meaning.

---

## The three kinds of failure

Artemis keeps three things apart everywhere -- on the terminal, in the reports,
and in what it decides to retry:

| | What it is | Examples |
| --- | --- | --- |
| **Compile error** | The file is not one artemis can run. Nothing executes | a parse error, an unknown field, an out-of-scope root, a reserved word, an unclosed `${`, an unknown name, a regex that will not compile, a duration that will not parse |
| **Step error** | The step could not run, or could not be judged | a refused connection, a timeout, a command not on the `PATH`, a `cwd` that does not exist, a path that did not resolve, `>` against an object, a capture that read nothing |
| **Assertion failure** | The step ran and gave the wrong answer | `status == 200` against a 404 |

A compile error is reported before anything executes, and **every** error in the
file is reported rather than only the first: a file with three mistakes takes one
run to fix. Nothing panics -- a malformed file produces diagnostics and a
non-zero exit, never a stack trace.

The diagnostic bar is a compiler's:

```
checkout.art:12:10: unknown field "statu"
   12 |   expect statu == 200
      |          ^^^^^
   hint: did you mean "status"?
```

The distinction between a step error and an assertion failure is worth caring
about: a flaky environment should not read as a broken API. `status` in every
report is `error` for the first and `fail` for the second.

---

## The exit code

| Code | When |
| --- | --- |
| `0` | Everything passed. Every scenario loaded, every step ran, every assertion held |
| `1` | Anything else |

Anything else is: a failed assertion, an errored step, a scenario whose file
would not compile, a bad flag, a path that matched no scenario file, and a report
that could not be written. There is no third code -- a caller that needs to tell
those apart reads the [report](#the-json-report), where the shape of the failure
is spelled out.

A report that could not be written fails the command **even when the run
passed**: a CI job whose artifact silently vanished is worse off than one that
went red.

```sh
artemis run ./suite && echo "everything passed"
```

---

## The JSON report

`--report json` writes the whole outcome of a run as one JSON document, for a CI
job or an agent to read. Its value is `format[=path]`:

```sh
artemis run ./suite --report json               # the document on stdout
artemis run ./suite --report json=results.json  # the document in a file
artemis run ./suite --report junit=junit.xml    # JUnit XML for a CI reporter
artemis run ./suite --report json --report junit=junit.xml   # both
```

With no path the document goes to stdout and the console report moves to stderr,
so stdout holds exactly one document and `artemis run ./suite --report json | jq`
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
  "counts": {
    "scenarios":  { "total": 1, "passed": 0, "failed": 1, "errored": 0, "skipped": 0 },
    "steps":      { "total": 1, "passed": 0, "failed": 1, "errored": 0, "skipped": 0 },
    "assertions": { "total": 2, "passed": 1, "failed": 1, "errored": 0, "skipped": 0 }
  },
  "scenarios": [
    {
      "name": "items",
      "file": "suite/items.art",
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
              "line": 11
            },
            {
              "kind": "expect",
              "path": "body.data.state",
              "operator": "==",
              "expected": "ready",
              "actual": "pending",
              "status": "fail",
              "error": "",
              "line": 14
            }
          ]
        }
      ]
    }
  ],
  "failures": [
    {
      "file": "suite/items.art",
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
| `status` | run, scenario, step, assertion | `pass`, `fail`, `error` or `skip`. Every level above an assertion is the worst of its children, and a `skip` never drags a parent down. Nothing produces `skip` today; it is in the vocabulary |
| `passed` | run | `false` if anything failed or errored. The same thing the exit code says, for a consumer that does not want to learn the vocabulary |
| `counts` | run | Totals per level, each a `{total, passed, failed, errored, skipped}` tally, so nobody walks the tree to say "2 of 5 assertions failed" |
| `total`, `passed`, `failed`, `errored`, `skipped` | each tally | How many of that level ended that way |
| `scenarios` | run | One entry per scenario the run reached, in run order |
| `name` | scenario, step | As written in the file. A scenario whose file would not compile has no name, so it is `""` and `file` identifies it |
| `file` | scenario, failure | The path the scenario was read from |
| `error` | scenario, step, assertion, failure | Why it could not run, or `""`. A scenario's error is a file that would not compile, and such a scenario has no steps |
| `steps` | scenario | One entry per step the scenario reached |
| `attempts` | step | How many times the step was tried; `1` unless `retry` asked for more |
| `line` | step, assertion, failure | The 1-based line of the scenario file it was written on, and `0` when artemis does not know |
| `assertions` | step | One entry per `expect` the step evaluated, plus one per capture that failed, in the order they were made |
| `kind` | assertion, failure | What made the assertion: `expect` for every `expect`, whatever its expression does, and `capture` for a capture that could not be read. There is no per-operator kind and no per-step-type one |
| `path` | assertion, failure | The source text of what was inspected: `body.data.count`. For an expression with no single subject -- an `or` chain, a bare call -- the whole expression; for a capture, the name it captures under |
| `operator` | assertion, failure | The expression's top-level comparison token -- `==`, `contains`, `exists`, `is`, or `not` prefixed to one of them -- and `""` for an expression with no single one |
| `expected`, `actual` | assertion, failure | The two sides as evaluated, each keeping its JSON type. `null` when there was no such value -- a path that did not resolve |
| `failures` | run | Everything the run says to go and fix, flat and in run order. One entry per failing assertion, per step that could not run, and per file that would not compile, each naming its own `file`, `line`, `scenario` and `step` so an entry stands alone. `[]` for a run that passed |
| `scenario`, `step` | failure | The names of the two things the failure sits under, repeated so the entry stands alone |
| `screenshot` | step, failure | The path to a picture of the page, for a [browser step that did not pass](#when-a-browser-step-fails), and `""` for every other step. The path is as the run was given it -- relative to the working directory unless `--screenshots` named an absolute one -- so a CI job uploading it uses the string verbatim. Repeated on the failure entry so the entry stands alone |

Every key is always present, with its zero value rather than omitted, so a `jq`
expression never has to tell absent from empty. `expected` and `actual` are the
exception: they are `null`, because either may legitimately be any JSON type.
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
`duration_ms`. The [README](README.md#junit-xml) specifies that dialect.

---

## The toolchain

Four commands read the language without running any of it, and they read it
through the same front end `artemis run` does -- the same lexer, the same
parser, the same checker -- so none of them can disagree with this document
about what a file means. The [README](README.md#commands) specifies their flags
and their output.

| Command | What it does |
| --- | --- |
| `artemis parse -f x.art` | Every diagnostic the file produces, and a non-zero exit if any is an error. `--json` writes them as one document instead, for an editor or a UI |
| `artemis fmt [-w] x.art` | The file in canonical layout. Layout only: no string requoted, no expression reassociated, no comment dropped |
| `artemis ast -f x.art` | The syntax tree as JSON, with every node's span and every step's inferred type. `--from-json` goes the other way, tree to source |
| `artemis grammar` | The grammar below, with its precedence and its post-parse rules, as one piece of text. `--json` gives the enumerable choice points -- methods, operators, type names, block fields, reserved words, diagnostic codes -- read out of the tables the parser and the checker themselves consult |
| `artemis build --lang=python [-o dir]` | The scenarios exported as tests for another runner. Python is pytest with `requests`, `subprocess` and `playwright.sync_api`: one scenario becomes one test function, a `capture` a local variable, an `expect` a bare assert, a `within` on a browser assertion playwright's own timeout. The export is **one way** -- artemis never reads generated code back, and every generated file says so. A `js` target is specified and not built; a `go` one is reserved and will not be |

A scenario is converted into this language once, never translated on the fly:
`artemis migrate -f old.yaml` for a YAML scenario and `artemis generate -f
collection.json` for a Postman collection. Both write through `artemis fmt`'s
printer, so what they produce is already canonical.

---

## Not in this spec

Everything here is deliberate. A scenario that uses one of these does not run.

| | |
| --- | --- |
| **`db` steps** | Designed, specified with the database step type |
| **Control flow** | `if`, `else`, loops, data-driven tables, `parallel`, `group`. Reserved words, not features |
| **Functions, imports, fixtures** | `fn`, `return`, `import`, `use`, `let`, `setup`, `teardown`. Reserved words. Reuse is what the host language is for, and `artemis build --lang=...` is the answer to "I need real abstraction" |
| **Agentic assertions** | `ai`. Reserved, not implemented, and a parse error with a pointer |
| **Parallelism** | Scenarios and steps run one after another, in the order they are written |
| **A `wait_until` statement** | Waiting for a *condition* is per assertion and is spelled [`within`](#within), which names the condition being waited for. A browser block's [`wait "1s"`](#browser-steps) is a fixed pause and not a condition. There is no step-level wait: re-running a whole step is what [`retry`](#retry) is for, and the two are not interchangeable |
| **Request and response detail in a report** | Neither report carries a body or a header |
| **YAML** | Not a runtime format. See below |

---

## Coming from YAML

YAML survives as an input to `artemis migrate -f old.yaml -o new.art`, which is
the only YAML reader left in the binary. Nothing runs it. Six habits do not
carry over:

| In YAML | Here |
| --- | --- |
| `type: api` on a step | Nothing. The step's type comes from its action block |
| `{{name}}` | `"${name}"`, and the braces hold a whole expression |
| `{{env.NAME}}`, legal only in a variable's `value:` | `env("NAME")`, legal wherever an expression is |
| `operator:` / `value:` / `type:` as three sibling keys | One expression: `expect body.x > 0` |
| An unknown variable failing the step at run time | A compile error, before anything runs |
| `operator: empty` on a stream | `expect stderr matches /^\s*$/`. `== ""` is the check that is right in theory and wrong in practice, because almost every command ends its output with a newline |
