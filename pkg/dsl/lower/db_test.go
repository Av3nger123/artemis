package lower

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
	"artemis/pkg/eval"
	"artemis/pkg/shared/models"
)

// A db step's args keep their types -- a placeholder is compared against a
// typed column -- where a terminal step's are text. An object is its JSON.
func TestADBStepLowersWithTypedArgs(t *testing.T) {
	src := `scenario "s" {
  secret var url = "sqlite:app.db"
  var n = 7

  step "q" {
    db url {
      sql  = "SELECT * FROM t WHERE a = ? AND b = ? AND c = ? AND d = ? AND e = ? AND f = ?"
      args = [n, 2.5, "x", true, null, {"k": [1]}]
    }
    expect affected == 1
  }
}
`
	m := dbModelOf(t, src, map[string]any{"url": "sqlite:app.db", "n": 7.0})
	want := []any{int64(7), 2.5, "x", true, nil, `{"k":[1]}`}
	if !reflect.DeepEqual(m.DB.Args, want) {
		t.Errorf("args = %#v, want %#v", m.DB.Args, want)
	}
	if m.Type != "db" || m.DB.URL != "sqlite:app.db" || !strings.HasPrefix(m.DB.SQL, "SELECT") {
		t.Errorf("model = %+v", m)
	}
	if !m.Secrets.URL || m.Secrets.Args || m.Secrets.SQL {
		t.Errorf("secrets = %+v, want only the URL marked", m.Secrets)
	}
}

func TestADBStepsSQLFileIsAPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("seed.sql", []byte("CREATE TABLE t (x);"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := dbModelOf(t, `scenario "s" {
  step "seed" {
    db "sqlite:app.db" { sql = file("seed.sql") }
  }
}
`, nil)
	if filepath.Base(m.DB.SQLFile) != "seed.sql" || !filepath.IsAbs(m.DB.SQLFile) || m.DB.SQL != "" {
		t.Errorf("model = %+v, want the file's path and no text", m.DB)
	}
}

func dbModelOf(t *testing.T, src string, vars map[string]any) models.Step {
	t.Helper()
	files := expand.MapLoader{"main.art": src, "seed.sql": "CREATE TABLE t (x);"}
	u := front.CompileWith("main.art", src, files)
	if u.Bag.HasErrors() {
		t.Fatal(u.Bag.All())
	}
	scs, err := File(u.Expanded, u.Info)
	if err != nil {
		t.Fatal(err)
	}
	if vars == nil {
		vars = map[string]any{}
	}
	m, err := scs[0].Steps[0].Model(&eval.Env{Vars: vars})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A command's objects keep the order they were written in -- a Mongo
// command's name is its first key, a sort's keys are its order -- all the way
// down.
func TestADBCommandKeepsItsKeyOrder(t *testing.T) {
	m := dbModelOf(t, `scenario "s" {
  var status = "paid"

  step "find" {
    db "mongodb://localhost:27017/shop" {
      command = {"find": "orders", "filter": {"status": status}, "sort": {"z": 1, "a": -1}}
    }
  }
}
`, map[string]any{"status": "paid"})
	want := models.Doc{
		{Key: "find", Value: "orders"},
		{Key: "filter", Value: models.Doc{{Key: "status", Value: "paid"}}},
		{Key: "sort", Value: models.Doc{{Key: "z", Value: 1.0}, {Key: "a", Value: -1.0}}},
	}
	if !reflect.DeepEqual(m.DB.Command, want) {
		t.Errorf("command = %#v\nwant %#v", m.DB.Command, want)
	}
	argv := dbModelOf(t, `scenario "s" {
  var n = 2

  step "get" {
    db "redis://localhost:6379" { command = ["GET", "session:${n}"] }
  }
}
`, map[string]any{"n": 2.0}).DB.Command
	if !reflect.DeepEqual(argv, []any{"GET", "session:2"}) {
		t.Errorf("argv = %#v", argv)
	}
}
