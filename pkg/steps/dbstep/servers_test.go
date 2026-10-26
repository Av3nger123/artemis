package dbstep

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"artemis/pkg/shared/models"
)

// These run against real servers, and only when one is named:
//
//	ARTEMIS_TEST_POSTGRES_URL=postgres://app:pw@localhost:5432/shop
//	ARTEMIS_TEST_MYSQL_URL=mysql://app:pw@localhost:3306/shop
//	ARTEMIS_TEST_SQLSERVER_URL=sqlserver://sa:pw@localhost:1433?database=master
//	ARTEMIS_TEST_ORACLE_URL=oracle://app:pw@localhost:1521/FREEPDB1
//
// `go test ./...` with neither set skips them; SQLite, in-process, covers the
// shared adapter there. What they hold each driver to is the value table in
// SPEC.md's db steps section, read off that database's own column types.

type serverCase struct {
	env      string
	ddl      string // creates t; every column of the table
	insert   string // one row, from args
	args     []any
	selectQ  string
	literalQ string // selectQ with its placeholder written out
	want     map[string]any
	script   string // two statements, the second an insert into t2
	placeOne string // a query with one placeholder, for typed binding
}

var servers = []serverCase{
	{
		env: "ARTEMIS_TEST_POSTGRES_URL",
		ddl: `CREATE TABLE t (
			id bigint PRIMARY KEY, big bigint, label text, total numeric(12,2), money numeric(30,2),
			ratio double precision, ok boolean, born date, at timestamptz, meta jsonb, raw bytea, uid uuid, gone text)`,
		insert: "INSERT INTO t VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)",
		args: []any{int64(1), int64(1) << 60, "paid", "12.50", "1234567890123456789012345678.25", 0.25, true,
			"2026-10-26", "2026-10-26T09:30:15+05:30", `{"tier": "gold"}`, []byte{0, 255}, "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d", nil},
		selectQ:  "SELECT * FROM t WHERE id = $1",
		literalQ: "SELECT * FROM t WHERE id = 1",
		want: map[string]any{
			"id": 1.0, "big": "1152921504606846976", "label": "paid", "total": 12.5, "money": "1234567890123456789012345678.25",
			"ratio": 0.25, "ok": true, "born": "2026-10-26", "at": "2026-10-26T04:00:15Z", "meta": map[string]any{"tier": "gold"},
			"raw": "AP8=", "uid": "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d", "gone": nil,
		},
		script:   "CREATE TABLE t2 (x int); INSERT INTO t2 VALUES (1); INSERT INTO t2 VALUES (2);",
		placeOne: "SELECT count(*) AS n FROM t WHERE id = $1",
	},
	{
		env: "ARTEMIS_TEST_MYSQL_URL",
		ddl: `CREATE TABLE t (
			id bigint PRIMARY KEY, big bigint, label text, total decimal(12,2), money decimal(30,2),
			ratio double, ok boolean, born date, at datetime, meta json, raw blob, uid char(36), gone text)`,
		insert: "INSERT INTO t VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		args: []any{int64(1), int64(1) << 60, "paid", "12.50", "1234567890123456789012345678.25", 0.25, true,
			"2026-10-26", "2026-10-26 04:00:15", `{"tier": "gold"}`, []byte{0, 255}, "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d", nil},
		selectQ:  "SELECT * FROM t WHERE id = ?",
		literalQ: "SELECT * FROM t WHERE id = 1",
		want: map[string]any{
			"id": 1.0, "big": "1152921504606846976", "label": "paid", "total": 12.5, "money": "1234567890123456789012345678.25",
			// MySQL has no boolean: a BOOLEAN column is TINYINT(1), and reads as 1.
			"ratio": 0.25, "ok": 1.0, "born": "2026-10-26", "at": "2026-10-26T04:00:15Z", "meta": map[string]any{"tier": "gold"},
			"raw": "AP8=", "uid": "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d", "gone": nil,
		},
		script:   "CREATE TABLE t2 (x int); INSERT INTO t2 VALUES (1); INSERT INTO t2 VALUES (2);",
		placeOne: "SELECT count(*) AS n FROM t WHERE id = ?",
	},
	{
		env: "ARTEMIS_TEST_SQLSERVER_URL",
		ddl: `CREATE TABLE t (
			id bigint PRIMARY KEY, big bigint, label nvarchar(100), total decimal(12,2), money decimal(30,2),
			ratio float, ok bit, born date, at datetimeoffset, meta nvarchar(max), raw varbinary(10), uid uniqueidentifier, gone nvarchar(10))`,
		insert: "INSERT INTO t VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12, @p13)",
		args: []any{int64(1), int64(1) << 60, "paid", "12.50", "1234567890123456789012345678.25", 0.25, true,
			"2026-10-26", "2026-10-26T09:30:15+05:30", `{"tier": "gold"}`, []byte{0, 255}, "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d", nil},
		selectQ:  "SELECT * FROM t WHERE id = @p1",
		literalQ: "SELECT * FROM t WHERE id = 1",
		want: map[string]any{
			"id": 1.0, "big": "1152921504606846976", "label": "paid", "total": 12.5, "money": "1234567890123456789012345678.25",
			// SQL Server has no JSON column type: the text is what comes back.
			"ratio": 0.25, "ok": true, "born": "2026-10-26", "at": "2026-10-26T04:00:15Z", "meta": `{"tier": "gold"}`,
			"raw": "AP8=", "uid": "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d", "gone": nil,
		},
		script:   "CREATE TABLE t2 (x int)\nGO\nINSERT INTO t2 VALUES (1)\nINSERT INTO t2 VALUES (2)\nGO\n",
		placeOne: "SELECT count(*) AS n FROM t WHERE id = @p1",
	},
	{
		env: "ARTEMIS_TEST_ORACLE_URL",
		ddl: `CREATE TABLE t (
			id NUMBER(19) PRIMARY KEY, big NUMBER(19), label VARCHAR2(100), total NUMBER(12,2), money NUMBER(30,2),
			ratio BINARY_DOUBLE, ok NUMBER(1), born DATE, at TIMESTAMP WITH TIME ZONE, meta CLOB, bin RAW(10), guid VARCHAR2(36), gone VARCHAR2(10))`,
		insert: `INSERT INTO t VALUES (:1, :2, :3, :4, :5, :6, :7, TO_DATE(:8, 'YYYY-MM-DD'),
			TO_TIMESTAMP_TZ(:9, 'YYYY-MM-DD"T"HH24:MI:SSTZH:TZM'), :10, :11, :12, :13)`,
		// Oracle has no boolean in a table before 23ai's, so ok is a 1.
		args: []any{int64(1), int64(1) << 60, "paid", "12.50", "1234567890123456789012345678.25", 0.25, int64(1),
			"2026-10-26", "2026-10-26T09:30:15+05:30", `{"tier": "gold"}`, []byte{0, 255}, "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d", nil},
		selectQ:  "SELECT * FROM t WHERE id = :1",
		literalQ: "SELECT * FROM t WHERE id = 1",
		want: map[string]any{
			"ID": 1.0, "BIG": "1152921504606846976", "LABEL": "paid", "TOTAL": 12.5, "MONEY": "1234567890123456789012345678.25",
			// Oracle upper-cases an unquoted column name, and RAW and UID are
			// reserved words there.
			"RATIO": 0.25, "OK": 1.0, "BORN": "2026-10-26", "AT": "2026-10-26T04:00:15Z", "META": `{"tier": "gold"}`,
			"BIN": "AP8=", "GUID": "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d", "GONE": nil,
		},
		script: `CREATE TABLE t2 (x NUMBER);
INSERT INTO t2 VALUES (1);
BEGIN
  INSERT INTO t2 VALUES (2);
END;
/
`,
		placeOne: `SELECT count(*) AS "n" FROM t WHERE id = :1`,
	},
}

func TestAgainstServers(t *testing.T) {
	for _, c := range servers {
		t.Run(c.env, func(t *testing.T) {
			url := os.Getenv(c.env)
			if url == "" {
				t.Skipf("%s is not set", c.env)
			}
			skipUnbuilt(t, url)
			p := NewPools()
			defer p.Close()
			ctx := WithPools(context.Background(), p)
			run := func(sql string, args ...any) map[string]any {
				t.Helper()
				return observeOK(t, ctx, url, sql, args)
			}
			run("DROP TABLE IF EXISTS t")
			run("DROP TABLE IF EXISTS t2")
			defer run("DROP TABLE IF EXISTS t")
			defer run("DROP TABLE IF EXISTS t2")

			run(c.ddl)
			if got := run(c.insert, c.args...)[RootAffected]; got != 1.0 {
				t.Errorf("insert affected = %v, want 1", got)
			}

			// Twice: once with an arg (a prepared statement, the binary
			// protocol) and once with none (MySQL's text protocol, where
			// every value arrives as bytes).
			for _, q := range []struct {
				sql  string
				args []any
			}{{c.selectQ, []any{int64(1)}}, {c.literalQ, nil}} {
				rows := run(q.sql, q.args...)[RootRows].([]any)
				if len(rows) != 1 {
					t.Fatalf("%s: rows = %v, want one", q.sql, rows)
				}
				for col, want := range c.want {
					if got := rows[0].(map[string]any)[col]; !reflect.DeepEqual(got, want) {
						t.Errorf("%s: %s = %#v, want %#v", q.sql, col, got, want)
					}
				}
			}

			// A float arg that is whole is bound as an integer.
			if n := run(c.placeOne, int64(1))[RootRows].([]any)[0].(map[string]any)["n"]; n != 1.0 {
				t.Errorf("count = %v, want 1", n)
			}

			script := filepath.Join(t.TempDir(), "seed.sql")
			os.WriteFile(script, []byte(c.script), 0o600)
			if _, err := (Executor{}).Observe(ctx, models.Step{Type: StepType, DB: models.DB{URL: url, SQLFile: script}}, nil); err != nil {
				t.Fatalf("script: %v", err)
			}
			if n := run(`SELECT count(*) AS "n" FROM t2`)[RootRows].([]any)[0].(map[string]any)["n"]; n != 2.0 {
				t.Errorf("after the script t2 has %v rows, want 2", n)
			}

			_, err := Executor{}.Observe(ctx, models.Step{Type: StepType, DB: models.DB{URL: url, SQL: "SELECT * FROM missing_table"}}, nil)
			if err == nil {
				t.Error("a query of a missing table did not error")
			}
		})
	}
}

// A wrong password is a step error that names the server and not the password.
func TestAWrongPasswordIsNamedWithoutIt(t *testing.T) {
	envs := []string{"ARTEMIS_TEST_MONGO_URL", "ARTEMIS_TEST_REDIS_URL"}
	for _, c := range servers {
		envs = append(envs, c.env)
	}
	for _, env := range envs {
		t.Run(env, func(t *testing.T) {
			url := os.Getenv(env)
			if url == "" {
				t.Skipf("%s is not set", env)
			}
			skipUnbuilt(t, url)
			if !strings.Contains(url, "@") {
				t.Skipf("%s has no credentials to get wrong", env)
			}
			bad := strings.Replace(url, "@", "x-wrong-hunter2@", 1)
			_, err := Executor{}.Observe(context.Background(), models.Step{Type: StepType, DB: models.DB{URL: bad, SQL: "SELECT 1"}}, nil)
			if err == nil {
				t.Fatal("a wrong password connected")
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("error %q prints the password", err)
			}
		})
	}
}

// skipUnbuilt skips a test of a database this build has no driver for.
func skipUnbuilt(t *testing.T, url string) {
	t.Helper()
	scheme, _, _ := strings.Cut(url, ":")
	for _, s := range Unbuilt() {
		if s == scheme {
			t.Skipf("this build has no %s driver; run with -tags db_all", scheme)
		}
	}
}
