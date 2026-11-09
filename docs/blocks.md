# Parallel steps, branches and polling

A flow's steps run in order. A block runs the steps inside it another way:

```art
# flows/import.art
flow import_finishes {
  var users_file = "fixtures/users.csv"
  private var url = env("API_URL")

  parallel {
    step upload_the_users as "upload the users" {
      post "${url}/imports" { multipart "file" = file(users_file) }
      var users_job = body.id
    }
    step upload_the_orders as "upload the orders" {
      post "${url}/imports" { multipart "file" = file("fixtures/orders.csv") }
      var orders_job = body.id
    }
  }

  repeat every "2s" within "5m" {
    step users_status as "users status" {
      get "${url}/imports/${users_job}"
      var users = body.state
    }
    step orders_status as "orders status" {
      get "${url}/imports/${orders_job}"
      var orders = body.state
    }
  } until users != "running" and orders != "running"

  if users == "done" {
    step users_are_there as "users are there" {
      get "${url}/users?limit=1"
      expect len(body) == 1
    }
  } else {
    step the_import_says_why as "the import says why" {
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

## Routing on a scenario's data

A scenario has no routing of its own: it sets values, and the flow decides
what they mean. So a block is also how one flow covers every case its
scenarios can cause:

```art
# scenarios/import.art
flow "../flows/import.art"

scenario good_users as "good users" {}
scenario a_broken_sheet as "a broken sheet" { users_file = "fixtures/broken.csv" }
```

Both scenarios run the same steps up to the `if`; the second takes the `else`
because of what the server said about its data, not because the scenario
asked for it. A step may also write a name the flow already has -- a
scenario's value, say -- and the last write wins: the steps after it, and the
conditions of the blocks after it, read the new value.
