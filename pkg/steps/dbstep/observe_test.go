package dbstep

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/token"
	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// The roots this package binds must be exactly the ones pkg/dsl/check lets a
// .art db step name: a root the checker admits and the runtime never binds is
// an expect that silently errors at run time.
func TestObservedRootsMatchTheChecker(t *testing.T) {
	roots := observeOK(t, context.Background(), tempDB(t), "SELECT 1 AS one", nil)
	got := make([]string, 0, len(roots))
	for k := range roots {
		got = append(got, k)
	}
	sort.Strings(got)
	var want []string
	for _, r := range check.Roots(check.DB) {
		if r != executor.RootDurationMS {
			want = append(want, r)
		}
	}
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("observed roots = %v, want check.Roots(check.DB) = %v", got, want)
	}
}

// The checker checks a literal URL against token.DBSchemes, so a scheme it
// accepts has to be one a driver is registered under, and the other way round.
func TestSchemesMatchTheChecker(t *testing.T) {
	want := append([]string(nil), token.DBSchemes...)
	sort.Strings(want)
	if got := Schemes(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("registered schemes = %v, want token.DBSchemes = %v", got, want)
	}
}

func TestAStatementReportsWhatItChanged(t *testing.T) {
	ctx, url := shared(t)
	observeOK(t, ctx, url, "CREATE TABLE orders (id INTEGER PRIMARY KEY, status TEXT, total NUMERIC, meta JSON, logo BLOB, placed DATE)", nil)

	roots := observeOK(t, ctx, url, "INSERT INTO orders (id, status, total) VALUES (?, ?, ?), (?, ?, ?)",
		[]any{int64(1), "paid", 12.5, int64(2), "pending", nil})
	if roots[RootAffected] != 2.0 {
		t.Errorf("affected = %#v, want 2", roots[RootAffected])
	}
	if rows := roots[RootRows].([]any); len(rows) != 0 {
		t.Errorf("rows = %v, want [] for a statement", rows)
	}
}

func TestAQueryReadsRowsByColumnName(t *testing.T) {
	ctx, url := shared(t)
	observeOK(t, ctx, url, "CREATE TABLE orders (id INTEGER PRIMARY KEY, status TEXT, total REAL, meta JSON, logo BLOB, note TEXT)", nil)
	observeOK(t, ctx, url, `INSERT INTO orders VALUES (1, 'paid', 12.5, '{"tier":"gold","n":[1,2]}', x'00ff', NULL)`, nil)

	roots := observeOK(t, ctx, url, "SELECT id, status, total, meta, logo, note FROM orders WHERE id = ?", []any{int64(1)})
	rows := roots[RootRows].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want one", rows)
	}
	want := map[string]any{
		"id":     1.0,
		"status": "paid",
		"total":  12.5,
		"meta":   map[string]any{"tier": "gold", "n": []any{1.0, 2.0}},
		"logo":   "AP8=",
		"note":   nil,
	}
	if !reflect.DeepEqual(rows[0], want) {
		t.Errorf("row = %#v\nwant %#v", rows[0], want)
	}
	if roots[RootAffected] != 1.0 {
		t.Errorf("affected = %#v, want 1: a query's count is its rows", roots[RootAffected])
	}
}

// INSERT ... RETURNING is a write that returns rows: run once, as a query.
func TestReturningIsRunOnceAsAQuery(t *testing.T) {
	ctx, url := shared(t)
	observeOK(t, ctx, url, "CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, v TEXT)", nil)
	roots := observeOK(t, ctx, url, "INSERT INTO t (v) VALUES (?) RETURNING id", []any{"a"})
	if rows := roots[RootRows].([]any); len(rows) != 1 || rows[0].(map[string]any)["id"] != 1.0 {
		t.Errorf("rows = %v, want [{id: 1}]", rows)
	}
	count := observeOK(t, ctx, url, "SELECT count(*) AS n FROM t", nil)
	if n := count[RootRows].([]any)[0].(map[string]any)["n"]; n != 1.0 {
		t.Errorf("count = %v, want 1: the insert ran twice", n)
	}
}

// An SQL file with no args is a script: every statement in it runs.
func TestAnSQLFileWithNoArgsRunsAsAScript(t *testing.T) {
	ctx, url := shared(t)
	seed := filepath.Join(t.TempDir(), "seed.sql")
	os.WriteFile(seed, []byte(`
		-- the seed
		CREATE TABLE users (id INTEGER, email TEXT);
		INSERT INTO users VALUES (1, 'a@example.test');
		INSERT INTO users VALUES (2, 'b@example.test');
	`), 0o644)
	_, err := Executor{}.Observe(ctx, models.Step{Type: StepType, DB: models.DB{URL: url, SQLFile: seed}}, nil)
	if err != nil {
		t.Fatalf("Observe(seed.sql) error = %v", err)
	}
	roots := observeOK(t, ctx, url, "SELECT email FROM users ORDER BY id", nil)
	if rows := roots[RootRows].([]any); len(rows) != 2 {
		t.Errorf("rows = %v, want the two the script inserted", rows)
	}
}

func TestAFileArgIsBoundAsItsBytes(t *testing.T) {
	ctx, url := shared(t)
	observeOK(t, ctx, url, "CREATE TABLE f (b BLOB)", nil)
	path := filepath.Join(t.TempDir(), "logo.bin")
	os.WriteFile(path, []byte{1, 2, 3}, 0o644)
	observeOK(t, ctx, url, "INSERT INTO f VALUES (?)", []any{models.FileArg(path)})
	roots := observeOK(t, ctx, url, "SELECT length(b) AS n FROM f", nil)
	if n := roots[RootRows].([]any)[0].(map[string]any)["n"]; n != 3.0 {
		t.Errorf("length = %v, want 3", n)
	}
}

func TestStepErrors(t *testing.T) {
	ctx, url := shared(t)
	cases := []struct {
		name string
		db   models.DB
		want string
	}{
		{"no url", models.DB{SQL: "SELECT 1"}, "no connection URL"},
		{"no sql", models.DB{URL: url}, "no sql to run"},
		{"unknown scheme", models.DB{URL: "postgress://app:hunter2@localhost/x", SQL: "SELECT 1"}, "no database driver for postgress://app:***@localhost/x"},
		{"sqlite with no file", models.DB{URL: "sqlite:", SQL: "SELECT 1"}, "names no file"},
		{"bad sql", models.DB{URL: url, SQL: "SELEC 1"}, "sqlite: "},
		{"two columns alike", models.DB{URL: url, SQL: "SELECT 1 AS id, 2 AS id"}, `two columns named "id"`},
		{"too many rows", models.DB{URL: url, SQL: "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 10001) SELECT x FROM c"}, "more than 10000 rows"},
		{"missing sql file", models.DB{URL: url, SQLFile: "/no/such/file.sql"}, "sql: "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Executor{}.Observe(ctx, models.Step{Type: StepType, DB: c.db}, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want one containing %q", err, c.want)
			}
			if err != nil && strings.Contains(err.Error(), "hunter2") {
				t.Errorf("error %q prints the password", err)
			}
		})
	}
}

// The run's Pools hands every step the same connection, which is what keeps a
// :memory: database alive from one step to the next.
func TestPoolsShareOneConnectionPerURL(t *testing.T) {
	ctx, url := shared(t)
	pools, _ := PoolsOf(ctx)
	a, err := pools.Conn(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pools.Conn(ctx, url)
	if a != b {
		t.Error("two asks for one URL opened two connections")
	}
}

// With no Pools on the context the step opens a connection for itself and
// closes it: a :memory: table made by one such step is gone for the next.
func TestAStepWithNoPoolsOpensAndClosesItsOwn(t *testing.T) {
	url := "sqlite::memory:"
	observeOK(t, context.Background(), url, "CREATE TABLE gone (x)", nil)
	_, err := Executor{}.Observe(context.Background(), models.Step{Type: StepType, DB: models.DB{URL: url, SQL: "SELECT x FROM gone"}}, nil)
	if err == nil {
		t.Error("the table outlived the step's own connection")
	}
}

func TestATimeoutIsAStepErrorNamingIt(t *testing.T) {
	ctx, url := shared(t)
	slow := "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 100000000) SELECT count(*) FROM c"
	_, err := Executor{}.Observe(ctx, models.Step{Type: StepType, Timeout: "20ms", DB: models.DB{URL: url, SQL: slow}}, nil)
	if err == nil || !strings.Contains(err.Error(), "within 20ms") {
		t.Errorf("error = %v, want one naming the 20ms deadline", err)
	}
}

// tempDB is a URL for a database file private to the test.
func tempDB(t *testing.T) string {
	t.Helper()
	return "sqlite:" + filepath.Join(t.TempDir(), "test.db")
}

// shared is a context carrying a Pools that the test closes, and a database
// for it.
func shared(t *testing.T) (context.Context, string) {
	t.Helper()
	p := NewPools()
	t.Cleanup(func() { p.Close() })
	return WithPools(context.Background(), p), tempDB(t)
}

func observeOK(t *testing.T, ctx context.Context, url, sql string, args []any) map[string]any {
	t.Helper()
	roots, err := Executor{}.Observe(ctx, models.Step{Type: StepType, DB: models.DB{URL: url, SQL: sql, Args: args}}, nil)
	if err != nil {
		t.Fatalf("Observe(%q) error = %v", sql, err)
	}
	return roots
}
