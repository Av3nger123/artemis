package dbstep

import (
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	_ "modernc.org/sqlite"             // registers "sqlite"
)

// The databases phase one ships. Each is a dialect row and a Register call;
// token.DBSchemes lists the same schemes, and a test holds the two equal.

var postgres = dialect{
	name:   "postgres",
	driver: "pgx",
	// pgx reads postgres:// and postgresql:// URLs itself.
	dsn: func(u string) (string, error) { return u, nil },
}

var mySQL = dialect{
	name:   "mysql",
	driver: "mysql",
	dsn:    mysqlDSN,
}

var sqlite = dialect{
	name:   "sqlite",
	driver: "sqlite",
	dsn:    sqliteDSN,
	// One connection: a :memory: database is one per connection, so a second
	// would be an empty database, and a file is written by one writer at a
	// time anyway.
	tune: func(db *sql.DB) { db.SetMaxOpenConns(1) },
}

func init() {
	Register("postgres", sqlDriver{postgres})
	Register("postgresql", sqlDriver{postgres})
	Register("mysql", sqlDriver{mySQL})
	Register("sqlite", sqlDriver{sqlite})
}

// mysqlDSN turns mysql://user:pw@host:3306/db?param=v into the driver's own
// user:pw@tcp(host:3306)/db?param=v. Times are read as times, so a DATETIME
// column reads as an RFC 3339 string like Postgres's does, and an SQL file may
// hold more than one statement.
func mysqlDSN(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("the URL does not parse")
	}
	host := u.Host
	if u.Port() == "" {
		host += ":3306"
	}
	rest := "tcp(" + host + ")/" + strings.TrimPrefix(u.Path, "/")
	if u.RawQuery != "" {
		rest += "?" + u.RawQuery
	}
	cfg, err := mysql.ParseDSN(rest)
	if err != nil {
		return "", err
	}
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Passwd, _ = u.User.Password()
	}
	cfg.ParseTime = true
	cfg.MultiStatements = true
	return cfg.FormatDSN(), nil
}

// sqliteDSN is the file a sqlite: URL names -- sqlite:app.db, sqlite://app.db
// and sqlite:///abs/app.db, relative to where artemis was run like a run
// step's cwd -- or sqlite::memory:. A wait for a lock held by another writer
// is given five seconds rather than failing at once.
func sqliteDSN(raw string) (string, error) {
	_, rest, _ := strings.Cut(raw, ":")
	rest = strings.TrimPrefix(rest, "//")
	if rest == "" || strings.HasPrefix(rest, "?") {
		return "", errors.New("the URL names no file; write sqlite:path/to/app.db or sqlite::memory:")
	}
	sep := "?"
	if strings.Contains(rest, "?") {
		sep = "&"
	}
	return rest + sep + "_pragma=busy_timeout(5000)", nil
}
