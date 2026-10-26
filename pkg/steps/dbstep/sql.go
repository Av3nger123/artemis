package dbstep

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// dialect is what one SQL database differs from the others by. Everything else
// -- running a statement, reading rows -- is database/sql's and shared.
type dialect struct {
	// name is how an error names the database: "postgres: ...".
	name string
	// driver is the database/sql driver name the library registered.
	driver string
	// dsn turns the step's URL into what the driver opens.
	dsn func(rawURL string) (string, error)
	// tune adjusts the pool once it is open, or is nil.
	tune func(*sql.DB)
}

// sqlDriver opens one dialect's connections.
type sqlDriver struct{ d dialect }

// Open opens a pool and pings it, so a database that is not there is a step
// error at the step that first needs it, naming the URL without its password.
func (s sqlDriver) Open(ctx context.Context, rawURL string) (Conn, error) {
	dsn, err := s.d.dsn(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.d.name, err)
	}
	db, err := sql.Open(s.d.driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.d.name, err)
	}
	if s.d.tune != nil {
		s.d.tune(db)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to %s: %w", Redact(rawURL), err)
	}
	return &sqlConn{db: db, d: s.d}, nil
}

// sqlConn is an open database/sql pool.
type sqlConn struct {
	db *sql.DB
	d  dialect
}

func (c *sqlConn) Close() error { return c.db.Close() }

// SQL runs st: a script whole, a query for its rows, a statement for its
// count. See the package doc for which is which.
func (c *sqlConn) SQL(ctx context.Context, st Statement, args []any) (Result, error) {
	if st.Script || !isQuery(st.Text) {
		res, err := c.db.ExecContext(ctx, st.Text, args...)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", c.d.name, err)
		}
		out := Result{Rows: []any{}}
		if n, err := res.RowsAffected(); err == nil {
			f := float64(n)
			out.Affected = &f
		}
		return out, nil
	}
	return c.query(ctx, st.Text, args)
}

// query reads every row a query returns, each as an object of its columns.
func (c *sqlConn) query(ctx context.Context, text string, args []any) (Result, error) {
	rows, err := c.db.QueryContext(ctx, text, args...)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", c.d.name, err)
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", c.d.name, err)
	}
	if err := distinct(cols); err != nil {
		return Result{}, err
	}

	out := Result{Rows: []any{}}
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	for rows.Next() {
		if len(out.Rows) == MaxRows {
			return Result{}, fmt.Errorf("the query returned more than %d rows; add a LIMIT, or count them with count(*)", MaxRows)
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Result{}, fmt.Errorf("%s: %w", c.d.name, err)
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col.Name()] = value(col.DatabaseTypeName(), raw[i])
		}
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("%s: %w", c.d.name, err)
	}
	n := float64(len(out.Rows))
	out.Affected = &n
	return out, nil
}

// distinct refuses a query that names two columns alike: a row is an object,
// so one would silently replace the other.
func distinct(cols []*sql.ColumnType) error {
	seen := make(map[string]bool, len(cols))
	for _, c := range cols {
		if seen[c.Name()] {
			return fmt.Errorf("the query returns two columns named %q, and a row holds one; name one with AS", c.Name())
		}
		seen[c.Name()] = true
	}
	return nil
}

// queryWords are the first keywords of a statement that returns rows.
var queryWords = map[string]bool{
	"SELECT": true, "WITH": true, "SHOW": true, "EXPLAIN": true, "VALUES": true,
	"PRAGMA": true, "DESCRIBE": true, "DESC": true, "TABLE": true,
}

// returning is the clause that makes a write return rows.
var returning = regexp.MustCompile(`(?i)\bRETURNING\b`)

// isQuery reports whether text returns rows, by its first keyword or a
// RETURNING clause, so it is run once, the right way.
func isQuery(text string) bool {
	return queryWords[firstWord(text)] || returning.MatchString(text)
}

// firstWord is the statement's first keyword, upper-cased, past any leading
// space, comments and parentheses.
func firstWord(text string) string {
	s := text
	for {
		s = strings.TrimLeftFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '(' })
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
				continue
			}
			return ""
		}
		end := strings.IndexFunc(s, func(r rune) bool { return !unicode.IsLetter(r) })
		if end < 0 {
			end = len(s)
		}
		return strings.ToUpper(s[:end])
	}
}
