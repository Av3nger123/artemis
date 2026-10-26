package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/dsl/parser"
)

// evalIn evaluates src as if it were written in dir/scenario.art, which is what
// a data path is resolved against.
func evalIn(t *testing.T, dir, src string) (any, error) {
	t.Helper()
	x, bag := parser.ParseExpr(filepath.Join(dir, "scenario.art"), src)
	if bag.HasErrors() {
		t.Fatalf("ParseExpr(%q): %v", src, bag.All())
	}
	return Eval(x, &Env{})
}

func dataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"data/users.json": `[{"name": "alice", "age": 30}, {"name": "bob", "age": 4}]`,
		"data/users.csv":  "name,role\nalice,admin\nbob,viewer\n",
		"data/empty.csv":  "",
		"data/ragged.csv": "a,b\n1\n",
		"data/twice.csv":  "a,a\n1,2\n",
		"data/bad.json":   "{",
		"seed.sql":        "select 1;\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// json() and csv() are the file's data, found next to the file that names
// them: JSON as any value, CSV as an array of objects of strings.
func TestDataBuiltinsReadTheirFiles(t *testing.T) {
	dir := dataDir(t)
	cases := []struct {
		src  string
		want any
	}{
		{`json("data/users.json")[1].name`, "bob"},
		{`json("data/users.json")[0].age`, float64(30)},
		{`len(json("data/users.json"))`, float64(2)},
		{`csv("data/users.csv")[0].role`, "admin"},
		{`len(csv("data/users.csv"))`, float64(2)},
		{`len(csv("data/empty.csv"))`, float64(0)},
		{`file("seed.sql") is file`, true},
		{`file("seed.sql") == file("./seed.sql")`, true},
		{`base64(file("seed.sql"))`, "c2VsZWN0IDE7Cg=="},
		{`sha256(file("seed.sql")) == sha256("select 1;\n")`, true},
	}
	for _, c := range cases {
		got, err := evalIn(t, dir, c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if !equal(got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.src, got, c.want)
		}
	}
}

// A file in text is its absolute path: what `args` hands a command.
func TestAFileRendersAsItsPath(t *testing.T) {
	dir := dataDir(t)
	got, err := evalIn(t, dir, `"${file("seed.sql")}"`)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "seed.sql") {
		t.Errorf("a file interpolated as %q, want its absolute path", got)
	}
}

// Each way a data builtin cannot answer is an errored assertion that says why.
func TestDataBuiltinErrors(t *testing.T) {
	dir := dataDir(t)
	cases := []struct{ src, want string }{
		{`json("data/missing.json")`, "json(): there is no file data/missing.json"},
		{`json("data/bad.json")`, "json(): data/bad.json is not JSON"},
		{`csv("data/ragged.csv")`, "csv(): data/ragged.csv is not CSV"},
		{`csv("data/twice.csv")`, `the column "a" is named twice`},
		{`file("data")`, "file(): data is a directory, not a file"},
		{`to_json({"f": file("seed.sql")})`, "a file cannot be written as JSON"},
	}
	for _, c := range cases {
		_, err := evalIn(t, dir, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", c.src, err, c.want)
		}
	}
}
