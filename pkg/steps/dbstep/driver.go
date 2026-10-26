package dbstep

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"artemis/pkg/shared/models"
)

// Driver opens connections to one kind of database.
type Driver interface {
	// Open connects to the database url names. The url is the step's own,
	// scheme included; how it becomes the database library's DSN is the
	// driver's business.
	Open(ctx context.Context, url string) (Conn, error)
}

// Conn is an open connection to one database, safe for concurrent use.
//
// A database takes one of the two statements: a SQL database SQL, MongoDB and
// Redis a Command. The other method answers with an error saying which to
// write, so one interface serves every database.
type Conn interface {
	// SQL runs one statement, or a whole script, and reports what it did.
	SQL(ctx context.Context, st Statement, args []any) (Result, error)
	// Command runs a database's own command: a models.Doc for MongoDB, an
	// argv for Redis.
	Command(ctx context.Context, cmd any) (Result, error)
	// Close releases the connection. Only Pools calls it.
	Close() error
}

// Statement is the SQL a step runs.
type Statement struct {
	Text string
	// Script is true for an SQL file run with no args: every statement in
	// it, in order, rather than one statement with placeholders.
	Script bool
}

// Result is what a statement did, in the evaluator's JSON domain.
type Result struct {
	// Rows are the rows a query returned, each keyed by column name. Never
	// nil: a statement that returns none has [].
	Rows []any
	// Affected is how many rows a statement changed, or a query returned,
	// and nil when the driver cannot say.
	Affected *float64
	// Value is a command's reply, in the evaluator's domain; nil for SQL.
	Value any
}

// drivers is the scheme table: which Driver a URL's scheme opens.
var drivers = struct {
	sync.RWMutex
	byScheme map[string]Driver
}{byScheme: map[string]Driver{}}

// Register makes d the driver for scheme. Like executor.Register it panics on
// a scheme registered twice, which is a wiring mistake.
func Register(scheme string, d Driver) {
	drivers.Lock()
	defer drivers.Unlock()
	if _, dup := drivers.byScheme[scheme]; dup {
		panic("dbstep: scheme " + scheme + " is registered twice")
	}
	drivers.byScheme[scheme] = d
}

// Schemes returns every registered scheme, sorted.
func Schemes() []string {
	drivers.RLock()
	defer drivers.RUnlock()
	out := make([]string, 0, len(drivers.byScheme))
	for s := range drivers.byScheme {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// driverFor is the Driver for rawURL's scheme, or an error naming the schemes
// there are. A URL written out was checked when the file compiled, so reaching
// the error means one from a var or env() -- an environment that says
// postgress:// -- and listing the real ones is the fix.
func driverFor(rawURL string) (Driver, error) {
	scheme, _, found := strings.Cut(rawURL, ":")
	scheme = strings.ToLower(scheme)
	drivers.RLock()
	d, ok := drivers.byScheme[scheme]
	drivers.RUnlock()
	if found && ok {
		return d, nil
	}
	return nil, fmt.Errorf("artemis has no database driver for %s; a db URL starts with one of: %s",
		Redact(rawURL), strings.Join(Schemes(), ":, ")+":")
}

// Redact is a connection URL with its password replaced: see
// models.RedactURL.
func Redact(rawURL string) string { return models.RedactURL(rawURL) }
