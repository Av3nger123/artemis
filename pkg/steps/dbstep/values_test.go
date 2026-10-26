package dbstep

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValueMapping(t *testing.T) {
	ts := time.Date(2026, 10, 26, 9, 30, 15, 0, time.FixedZone("IST", 5*3600+1800))
	cases := []struct {
		name   string
		dbType string
		raw    any
		want   any
	}{
		{"null", "TEXT", nil, nil},
		{"int", "INT8", int64(42), 42.0},
		{"int past 2^53 keeps its digits", "INT8", int64(1) << 60, "1152921504606846976"},
		{"unsigned past 2^53", "UNSIGNED BIGINT", uint64(1) << 62, "4611686018427387904"},
		{"float", "FLOAT8", 1.5, 1.5},
		{"bool", "BOOL", true, true},
		{"timestamp in UTC", "TIMESTAMPTZ", ts, "2026-10-26T04:00:15Z"},
		{"date as a date", "DATE", time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), "2026-10-26"},
		{"an oracle date with a time", "DATE", ts, "2026-10-26T04:00:15Z"},
		{"sql server guid", "UNIQUEIDENTIFIER", []byte{0x5e, 0x0c, 0x1c, 0x6f, 0x3c, 0x6c, 0x8e, 0x4c, 0x9f, 0x39, 0x0a, 0x4f, 0x1f, 0x7f, 0x2b, 0x1d}, "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d"},
		{"money", "MONEY", []byte("19.9900"), 19.99},
		{"oracle number", "NUMBER", "42", 42.0},
		{"exact decimal", "NUMERIC", "12.50", 12.5},
		{"inexact decimal stays text", "DECIMAL(30,2)", "123456789012345678901.25", "123456789012345678901.25"},
		{"jsonb", "JSONB", `{"a": [1, true]}`, map[string]any{"a": []any{1.0, true}}},
		{"json that is not", "JSON", `{oops`, `{oops`},
		{"bytea", "BYTEA", []byte{0, 255}, "AP8="},
		{"invalid utf-8 bytes", "TEXT", []byte{0xff, 0xfe}, "//4="},
		{"mysql text protocol int", "INT", []byte("7"), 7.0},
		{"mysql text protocol decimal", "DECIMAL", []byte("0.10"), 0.1},
		{"mysql text protocol double", "DOUBLE", []byte("2.5"), 2.5},
		{"text", "VARCHAR", []byte("paid"), "paid"},
		{"uuid", "UUID", "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d", "6f1c0c5e-6c3c-4c8e-9f39-0a4f1f7f2b1d"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := value(c.dbType, c.raw); !reflect.DeepEqual(got, c.want) {
				t.Errorf("value(%q, %#v) = %#v, want %#v", c.dbType, c.raw, got, c.want)
			}
		})
	}
}

func TestIsQuery(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":               true,
		"  select * from t":      true,
		"-- a comment\nSELECT 1": true,
		"/* hint */ WITH x AS (SELECT 1) SELECT * FROM x": true,
		"(SELECT 1) UNION (SELECT 2)":                     true,
		"SHOW TABLES":                                     true,
		"EXPLAIN SELECT 1":                                true,
		"PRAGMA table_info(t)":                            true,
		"INSERT INTO t VALUES (1)":                        false,
		"insert into t values (1) returning id":           true,
		"UPDATE t SET x = 1":                              false,
		"DELETE FROM t":                                   false,
		"CREATE TABLE t (x int)":                          false,
		"-- only a comment":                               false,
	}
	for sql, want := range cases {
		if got := postgres.isQuery(sql); got != want {
			t.Errorf("isQuery(%q) = %v, want %v", sql, got, want)
		}
	}
	for d, cases := range map[string]map[string]bool{
		"sqlserver": {
			"INSERT INTO t (v) OUTPUT inserted.id VALUES (@p1)": true,
			"UPDATE jobs SET output = 1":                        false,
			"EXEC list_orders @p1":                              true,
			"INSERT INTO t VALUES (1) RETURNING id":             false,
		},
		"oracle": {
			"INSERT INTO t VALUES (1) RETURNING id INTO :2": false,
			"SELECT 1 FROM dual":                            true,
		},
	} {
		dl := map[string]dialect{"sqlserver": sqlServer, "oracle": oracle}[d]
		for sql, want := range cases {
			if got := dl.isQuery(sql); got != want {
				t.Errorf("%s: isQuery(%q) = %v, want %v", d, sql, got, want)
			}
		}
	}
}

func TestSplitBatches(t *testing.T) {
	script := "CREATE TABLE t (x int)\nGO\n\nINSERT INTO t VALUES (1)\ngo  \nINSERT INTO t VALUES (2)\n-- GOing on\nGO\n"
	got := splitBatches(script)
	if len(got) != 3 || !strings.Contains(got[2], "GOing on") {
		t.Errorf("batches = %q, want three, the comment kept in the third", got)
	}
}

func TestSplitOracle(t *testing.T) {
	script := `-- seed
CREATE TABLE t (x NUMBER);
INSERT INTO t
  VALUES (1);
CREATE OR REPLACE PROCEDURE bump AS
BEGIN
  UPDATE t SET x = x + 1;
END;
/
BEGIN
  bump;
END;
/
INSERT INTO t VALUES (2)
`
	want := []string{
		"CREATE TABLE t (x NUMBER)",
		"INSERT INTO t\n  VALUES (1)",
		"CREATE OR REPLACE PROCEDURE bump AS\nBEGIN\n  UPDATE t SET x = x + 1;\nEND;",
		"BEGIN\n  bump;\nEND;",
		"INSERT INTO t VALUES (2)",
	}
	if got := splitOracle(script); !reflect.DeepEqual(got, want) {
		t.Errorf("splitOracle =\n%q\nwant\n%q", got, want)
	}
	if got := tidyOracle("SELECT 1 FROM dual;"); got != "SELECT 1 FROM dual" {
		t.Errorf("tidyOracle = %q", got)
	}
	if got := tidyOracle("BEGIN bump; END;"); got != "BEGIN bump; END;" {
		t.Errorf("tidyOracle kept a block's END; = %q", got)
	}
}

func TestDSNs(t *testing.T) {
	got, err := mysqlDSN("mysql://app:p%40ss@db.internal/shop?tls=skip-verify")
	if err != nil {
		t.Fatal(err)
	}
	want := "app:p@ss@tcp(db.internal:3306)/shop?multiStatements=true&parseTime=true&tls=skip-verify"
	if got != want {
		t.Errorf("mysqlDSN = %q\nwant %q", got, want)
	}

	for in, want := range map[string]string{
		"sqlite:app.db":                         "app.db?_pragma=busy_timeout(5000)",
		"sqlite://app.db":                       "app.db?_pragma=busy_timeout(5000)",
		"sqlite:///abs/app.db":                  "/abs/app.db?_pragma=busy_timeout(5000)",
		"sqlite::memory:":                       ":memory:?_pragma=busy_timeout(5000)",
		"sqlite:app.db?_pragma=foreign_keys(1)": "app.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	} {
		if got, err := sqliteDSN(in); err != nil || got != want {
			t.Errorf("sqliteDSN(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://app:hunter2@localhost:5432/orders": "postgres://app:***@localhost:5432/orders",
		"postgres://app@localhost/orders":              "postgres://app@localhost/orders",
		"sqlite:app.db":                                "sqlite:app.db",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}
