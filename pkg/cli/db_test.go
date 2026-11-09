package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
)

// dbScenario seeds a database from a script in setup, writes through an api
// stand-in -- here, a db step -- reads the row back with typed args, and
// cleans up in teardown: the shape a db step exists for.
const dbScenario = `flow an_order_lands {
  var db_url = env("ART_TEST_DB")
  var id = 42

  setup {
    step seed {
      db db_url { sql = file("seed.sql") }
    }
  }

  step place_it as "place it" {
    db db_url {
      sql  = "INSERT INTO orders (id, status, total, meta) VALUES (?, ?, ?, ?)"
      args = [id, "paid", 12.5, {"tier": "gold"}]
    }
    expect affected == 1
  }

  step read_it_back as "read it back" {
    db db_url {
      sql  = "SELECT id, status, total, meta FROM orders WHERE id = ?"
      args = [id]
    }
    expect len(rows) == 1
    expect rows[0].status == "paid"
    expect rows[0].total == 12.5
    expect from_json(rows[0].meta).tier == "gold"
    expect duration_ms >= 0
    var status = rows[0].status
  }

  teardown {
    step drop_it as "drop it" {
      db db_url {
        sql  = "DELETE FROM orders WHERE status = ?"
        args = [status]
      }
      expect affected == 1
    }
  }
}

scenario an_order_lands as "an order lands" {}
`

const dbSeed = `CREATE TABLE orders (id INTEGER PRIMARY KEY, status TEXT, total REAL, meta TEXT);
CREATE TABLE audit (note TEXT);
`

func TestArtRunDBStepsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orders.art")
	writeSplit(t, path, dbScenario)
	os.WriteFile(filepath.Join(dir, "seed.sql"), []byte(dbSeed), 0o600)
	t.Setenv("ART_TEST_DB", "sqlite:"+filepath.Join(dir, "shop.db"))

	var diags bytes.Buffer
	run := runFiles(executor.Default(), []string{path}, report.Discard(), &diags)
	if !run.Passed() {
		t.Fatalf("run did not pass:\n%s\n%s", diags.String(), describeRun(run))
	}
	sc := run.Scenarios[0]
	if len(sc.Steps) != 4 {
		t.Fatalf("steps = %d, want 4: seed, place, read, drop", len(sc.Steps))
	}
}

// A wrong row is an assertion failure; SQL the database rejects is a step
// error -- the step could not run -- and the report says which.
func TestArtRunDBFailuresKeepTheirKinds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ART_TEST_DB", "sqlite:"+filepath.Join(dir, "shop.db"))
	run, _ := runArt(t, `flow kinds {
  var url = env("ART_TEST_DB")

  step wrong_answer as "wrong answer" {
    db url { sql = "SELECT 'pending' AS status" }
    expect rows[0].status == "paid"
  }

  step bad_sql as "bad sql" {
    db url { sql = "SELEC 1" }
    expect affected == 1
  }
}

scenario kinds {}
`)
	steps := run.Scenarios[0].Steps
	if steps[0].Status != result.StatusFail {
		t.Errorf("wrong answer: status = %s, want fail", steps[0].Status)
	}
	if steps[1].Status != result.StatusError || !strings.Contains(steps[1].Error, "sqlite") {
		t.Errorf("bad sql: status = %s, error = %q; want an errored step naming the database", steps[1].Status, steps[1].Error)
	}
}

func describeRun(run *result.RunResult) string {
	var b strings.Builder
	for _, sc := range run.Scenarios {
		b.WriteString(sc.Name + ": " + string(sc.Status) + " " + sc.Error + "\n")
		for _, st := range sc.Steps {
			b.WriteString("  " + st.Name + ": " + string(st.Status) + " " + st.Error + "\n")
			for _, a := range st.Assertions {
				if a.Status != result.StatusPass {
					b.WriteString("    " + a.Path + " " + a.Error + "\n")
				}
			}
		}
	}
	return b.String()
}
