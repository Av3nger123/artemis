# Checking what landed in the database

An API that answered 201 has said it saved the order; a `db` step checks that
it did. The action is `db` and the connection URL, whose scheme picks the
database -- `postgres://`, `mysql://`, `sqlite:`, `sqlserver://` or `oracle://`
-- and the block holds the
SQL and the values bound to its placeholders:

```art
scenario "an order is saved as paid" {
  var url = env("API_URL")
  secret var orders_db = env("ORDERS_DB")

  setup {
    step "seed" {
      db orders_db { sql = file("fixtures/seed.sql") }
    }
  }

  step "pay" {
    post "${url}/orders" { body = {"sku": "A-1", "pay": true} }
    expect status == 201
    capture order_id = body.id
  }

  step "the row says paid" {
    db orders_db {
      sql  = "SELECT status, total FROM orders WHERE id = $1"
      args = [order_id]
    }
    retry { times = 5, delay = "1s" }
    expect rows[0].status == "paid"
    expect rows[0].total > 0
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

`rows` is the query's rows, each an object keyed by column name, and `affected`
is how many rows a statement changed. SQL goes to the database as written, so
its placeholders are the database's own: `$1` for PostgreSQL, `?` for MySQL and
SQLite, `@p1` for SQL Server, `:1` for Oracle. An SQL file run with no `args` is
a script, every statement in it -- split at `GO` lines on SQL Server and the
SQL*Plus way on Oracle -- which is how the setup above seeds the database. A connection is opened once per run
and shared by every step that names the same URL, and a trace never prints the
password in it. [SPEC.md's *`db` steps*](../SPEC.md#db-steps) specifies how each
column type reads.

`make build` includes PostgreSQL, MySQL and SQLite; `make build-full`, which
the release is built as, adds SQL Server, Oracle, MongoDB and Redis.

MongoDB and Redis take their own commands instead of SQL -- a command document
as `db.runCommand()` takes it, the words `redis-cli` takes -- and the reply is
`result`:

```art
scenario "the session is cached" {
  secret var mongo = env("MONGO_URL")
  secret var cache = env("REDIS_URL")

  step "the user" {
    db mongo { command = {"find": "users", "filter": {"email": "a@example.test"}} }
    expect len(rows) == 1
    capture user_id = rows[0]._id
  }

  step "their session" {
    db cache { command = ["GET", "session:${user_id}"] }
    expect result == "active"
  }
}
```
