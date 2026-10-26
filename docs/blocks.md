# Parallel steps, branches and polling

A scenario's steps run in order. A block runs the steps inside it another way:

```art
scenario "an import finishes" {
  var url = env("API_URL")

  parallel {
    step "upload the users" {
      post "${url}/imports" { multipart "file" = file("fixtures/users.csv") }
      capture users_job = body.id
    }
    step "upload the orders" {
      post "${url}/imports" { multipart "file" = file("fixtures/orders.csv") }
      capture orders_job = body.id
    }
  }

  repeat every "2s" within "5m" {
    step "users status" {
      get "${url}/imports/${users_job}"
      capture users = body.state
    }
    step "orders status" {
      get "${url}/imports/${orders_job}"
      capture orders = body.state
    }
  } until users != "running" and orders != "running"

  if users == "done" {
    step "users are there" {
      get "${url}/users?limit=1"
      expect len(body) == 1
    }
  } else {
    step "the import says why" {
      get "${url}/imports/${users_job}/errors"
      expect len(body) > 0
    }
  }
}
```

`parallel` runs its steps at the same time and waits for all of them; `if` and
`switch` run one branch; `try { } catch { } finally { }` runs compensation when a
step fails and cleanup either way; `repeat` polls a round at a time until its
condition holds, and needs a `within` or a `max` so it cannot run forever. The
report stays in the order the file is written, and a repeat reports its last
round. [SPEC.md's *Blocks*](../SPEC.md#blocks) has the rules, including what each
block lets the steps after it read.
