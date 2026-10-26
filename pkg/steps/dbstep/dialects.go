package dbstep

import (
	"database/sql"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	_ "modernc.org/sqlite"             // registers "sqlite"
)

// The databases a db step can reach. Each is a dialect row and a Register
// call. PostgreSQL, MySQL and SQLite are in every build; SQL Server and Oracle
// register from files of their own, under build tags -- see optional.go -- and
// their dialect rows are here, with no driver behind them, so what they mean
// is tested in every build.

var postgres = dialect{
	name:   "postgres",
	driver: "pgx",
	// pgx reads postgres:// and postgresql:// URLs itself.
	dsn:       asIs,
	returning: returningClause,
}

var mySQL = dialect{
	name:   "mysql",
	driver: "mysql",
	dsn:    mysqlDSN,
	// MySQL has no RETURNING and MariaDB does: classifying it as a query
	// costs MySQL nothing, since there it is a syntax error either way.
	returning: returningClause,
}

var sqlite = dialect{
	name:   "sqlite",
	driver: "sqlite",
	dsn:    sqliteDSN,
	// One connection: a :memory: database is one per connection, so a second
	// would be an empty database, and a file is written by one writer at a
	// time anyway.
	tune:      func(db *sql.DB) { db.SetMaxOpenConns(1) },
	returning: returningClause,
}

// sqlServer reads its own sqlserver://user:pw@host:1433?database=shop URLs;
// the path of one names an instance, not a database.
var sqlServer = dialect{
	name:      "sqlserver",
	driver:    "sqlserver",
	dsn:       asIs,
	returning: regexp.MustCompile(`(?i)\bOUTPUT\s+(INSERTED|DELETED|\$ACTION)\b`),
	// A procedure is usually called to read what it selects.
	queries: []string{"EXEC", "EXECUTE"},
	split:   splitBatches,
}

// oracle reads its own oracle://user:pw@host:1521/service URLs.
var oracle = dialect{
	name:   "oracle",
	driver: "oracle",
	dsn:    asIs,
	split:  splitOracle,
	tidy:   tidyOracle,
}

func init() {
	Register("postgres", sqlDriver{postgres})
	Register("postgresql", sqlDriver{postgres})
	Register("mysql", sqlDriver{mySQL})
	Register("sqlite", sqlDriver{sqlite})
}

// asIs is the DSN of a driver that reads the step's URL itself.
func asIs(u string) (string, error) { return u, nil }

// batchSeparator is a line holding only GO: the batch separator of sqlcmd and
// SQL Server Management Studio, which the server itself never sees.
var batchSeparator = regexp.MustCompile(`(?im)^[ \t]*GO[ \t]*;?[ \t]*\r?$`)

// splitBatches cuts a SQL Server script at its GO lines, as sqlcmd does, so a
// script written for SSMS runs unchanged. A script with none is one batch.
func splitBatches(script string) []string {
	var out []string
	for _, b := range batchSeparator.Split(script, -1) {
		if strings.TrimSpace(b) != "" {
			out = append(out, b)
		}
	}
	return out
}

// plsqlStart is how a PL/SQL block begins: one that holds semicolons of its
// own, so it ends at a line holding only a slash, as SQL*Plus has it.
var plsqlStart = regexp.MustCompile(`(?i)^(DECLARE|BEGIN|CREATE\s+(OR\s+REPLACE\s+)?((NON)?EDITIONABLE\s+)?(FUNCTION|PROCEDURE|PACKAGE|TRIGGER|TYPE))\b`)

// splitOracle cuts an Oracle script the way SQL*Plus reads one: a statement
// ends with a semicolon at the end of a line, which is not sent, and a PL/SQL
// block ends at a line holding only "/". Oracle takes one statement per call.
func splitOracle(script string) []string {
	var out, cur []string
	plsql := false
	flush := func() {
		stmt := strings.TrimSpace(strings.Join(cur, "\n"))
		if !plsql {
			stmt = strings.TrimSpace(strings.TrimSuffix(stmt, ";"))
		}
		if stmt != "" {
			out = append(out, stmt)
		}
		cur = nil
	}
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(cur) == 0 {
			if trimmed == "" || trimmed == "/" || strings.HasPrefix(trimmed, "--") {
				continue
			}
			plsql = plsqlStart.MatchString(trimmed)
		}
		if plsql {
			if trimmed == "/" {
				flush()
				continue
			}
			cur = append(cur, line)
			continue
		}
		cur = append(cur, line)
		if strings.HasSuffix(trimmed, ";") {
			flush()
		}
	}
	flush()
	return out
}

// tidyOracle drops the semicolon a statement is habitually ended with, which
// Oracle rejects outside PL/SQL -- where the END; is part of the block.
func tidyOracle(text string) string {
	t := strings.TrimSpace(text)
	if plsqlStart.MatchString(t) {
		return text
	}
	return strings.TrimSpace(strings.TrimSuffix(t, ";"))
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
