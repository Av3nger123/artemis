package front

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
)

// A literal path to data is read when the file compiles: one that names nothing
// and one whose file does not parse are each a diagnostic on the path, and one
// that is fine says nothing. A path is relative to the file it is written in,
// here the flow's.
func TestDataIsCheckedWhenTheFileCompiles(t *testing.T) {
	files := expand.MapLoader{
		"suite/data/users.json":  `[{"name": "alice"}]`,
		"suite/data/broken.json": "[\n  {\"name\": \"alice\",}\n]",
		"suite/data/rows.csv":    "name,role\nalice,admin\nbob\n",
		"suite/data/good.csv":    "name,role\nalice,admin\n",
		"suite/fixtures/a.png":   "\x89PNG",
		"suite/data_flow.art": `flow data {
  var users  = json("data/users.json")
  var broken = json("data/broken.json")
  var rows   = csv("data/rows.csv")
  var good   = csv("data/good.csv")
  var logo   = file("fixtures/a.png")
  var gone   = file("fixtures/missing.png")
  var later  = json(env("DATA"))

  step s { run "true" }
}
`,
	}
	src := "flow \"data_flow.art\"\n\nscenario data { }\n"
	unit := CompileWith("suite/data.art", src, files)
	got := map[diag.Code][]string{}
	for _, d := range unit.Bag.All() {
		got[d.Code] = append(got[d.Code], d.Message)
	}
	want := map[diag.Code]string{
		diag.DataNotJSON:     "json(): data/broken.json is not JSON: line 2, column 20",
		diag.DataNotCSV:      "csv(): data/rows.csv is not CSV",
		diag.DataFileMissing: "file(): there is no file fixtures/missing.png",
	}
	for code, prefix := range want {
		if len(got[code]) != 1 || !strings.HasPrefix(got[code][0], prefix) {
			t.Errorf("%s: got %v, want one starting %q", code, got[code], prefix)
		}
	}
	if n := len(unit.Bag.All()); n != len(want) {
		t.Errorf("%d diagnostics, want %d: %v", n, len(want), unit.Bag.All())
	}
}

// With no loader -- what fmt and a parse with no disk use -- nothing is read
// and a data path is not a fault.
func TestNoLoaderReadsNoData(t *testing.T) {
	_, _, bag := Compile("a.art", `flow s {
  var gone = file("missing.png")
  step s { run "true" }
}
`)
	if bag.Len() != 0 {
		t.Errorf("Compile read data with no loader: %v", bag.All())
	}
}

// A mistake in a flow is the same mistake in every scenario that runs it, and
// is reported once rather than once per scenario.
func TestAFlowsMistakeIsReportedOnce(t *testing.T) {
	files := expand.MapLoader{"f.art": `flow f {
  step s {
    run "echo"
    expect statu == 0
  }
}
`}
	src := "flow \"f.art\"\n\nscenario a { }\nscenario b { }\nscenario c { }\n"
	unit := CompileWith("s.art", src, files)
	n := 0
	for _, d := range unit.Bag.All() {
		if d.Code == diag.UnknownField || d.Code == diag.UnknownIdentifier || d.Code == diag.NotInScope {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the mistake was reported %d times, want once: %v", n, unit.Bag.All())
	}
}
