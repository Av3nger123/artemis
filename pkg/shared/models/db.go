package models

import (
	"net/url"
	"strings"
)

// This file is the `db` step type's action: the statement the step runs and
// the database it runs it against. What it expects of the rows is an `expect`
// expression held by pkg/dsl/lower, not a field here (ART-40).

// DB is what a `db` step runs, with every value already resolved by
// pkg/dsl/lower.
//
// URL is the connection, and its scheme -- postgres://, mysql://, sqlite: --
// is what picks the driver. SQL is sent as written: artemis never rewrites a
// statement, so its placeholders are the database's own ($1 for Postgres, ?
// for MySQL and SQLite).
//
// Args are bound to those placeholders and keep their types, unlike a terminal
// step's args, which are text: `WHERE id = $1` against an integer column wants
// the number 42, not the string "42". Each is nil, a bool, an int64 for a whole
// number, a float64, a string, or a FileArg; an object or an array has already
// been rendered to its JSON text.
type DB struct {
	URL string
	SQL string
	// SQLFile is the absolute path of `sql = file(...)`, whose contents are
	// the SQL, read when the step runs. A file with no Args is run as a
	// script: every statement in it, in order.
	SQLFile string
	Args    []any

	// Command is what a database that takes no SQL runs, in place of SQL: a
	// Doc for MongoDB, its command document; an []any for Redis, its argv.
	// Values inside it are in the evaluator's domain, with an object written
	// out as a Doc so its keys keep the order they were written in -- a Mongo
	// command's name is its first key, and a sort's keys are its order.
	Command any
}

// Doc is an object whose keys are in the order the scenario wrote them.
type Doc []Field

// Field is one key of a Doc and its value.
type Field struct {
	Key   string
	Value any
}

// Map is the Doc as a plain object, its order dropped: what a trace writes.
func (d Doc) Map() map[string]any {
	out := make(map[string]any, len(d))
	for _, f := range d {
		out[f.Key] = Plain(f.Value)
	}
	return out
}

// Plain is v with every Doc in it made a plain object.
func Plain(v any) any {
	switch t := v.(type) {
	case Doc:
		return t.Map()
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = Plain(e)
		}
		return out
	}
	return v
}

// FileArg is a `file()` bound to a placeholder: its bytes are the value, read
// when the step runs.
type FileArg string

// RedactURL is a connection URL as a report or a trace may print it: its
// password replaced with ***, whether or not the scenario declared the URL
// secret, because a URL is where a database password usually lives.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		if strings.Contains(raw, "@") {
			// Credentials in something that is not a URL: print none of it
			// rather than guess where the password ends.
			scheme, _, _ := strings.Cut(raw, ":")
			return scheme + "://***"
		}
		return raw
	}
	if _, has := u.User.Password(); !has {
		return u.String()
	}
	// A placeholder the URL encoder leaves alone, swapped for the *** every
	// report writes: url.String would escape the asterisks themselves.
	const mark = "artemis-redacted"
	u.User = url.UserPassword(u.User.Username(), mark)
	return strings.Replace(u.String(), ":"+mark+"@", ":***@", 1)
}
