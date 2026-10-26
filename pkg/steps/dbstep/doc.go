// Package dbstep is the db step: it runs one statement against a database and
// reports the rows it returned and how many it changed. It is registered in the
// default registry under the step type "db", which is what pkg/dsl/lower stamps
// on a step whose action block is `db <url> { ... }`.
//
//	step "the order is paid" {
//	  db orders_db {
//	    sql  = "SELECT status FROM orders WHERE id = $1"
//	    args = [order_id]
//	  }
//	  expect rows[0].status == "paid"
//	}
//
// Like the other executors it makes one attempt and no assertions: the runner
// retries, times and judges, and an `expect` is an expression evaluated against
// the roots Observe returns -- rows and affected.
//
// # Drivers, and the one place that imports them
//
// The connection URL's scheme picks a Driver, which opens a Conn. A database is
// a Driver registered under its schemes by an init in its own file, so adding
// one is a file and a Register call; nothing outside this package imports a
// database library. Every SQL database shares one adapter over database/sql,
// and differs from the others by a dialect: its driver name, how its URL
// becomes a DSN, and how its column types read.
//
// # Connections belong to the run
//
// A Pools holds one pool per URL for a whole run, opened on the first step
// that asks and closed by the runner when the run ends. It reaches the executor
// on the context, as a browser session does, so the executor holds no state and
// a step borrows a connection it can never close. A step run with no Pools on
// its context -- outside `artemis run` -- opens one for itself and closes it.
//
// # A query or a statement
//
// database/sql cannot tell a SELECT from an INSERT before running it, and
// running one twice would repeat its writes. So the statement's first keyword
// decides, before it is sent: SELECT, WITH, SHOW, EXPLAIN, VALUES, PRAGMA,
// DESCRIBE and TABLE -- or a RETURNING or OUTPUT clause -- are queried for
// rows, anything else is executed for a count. An SQL file with no args is a
// script, run whole.
package dbstep
