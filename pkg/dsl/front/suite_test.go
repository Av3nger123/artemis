package front

import (
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
)

// The suites a file may be under are suite.art in its own folder and every
// folder above it to the working directory, outermost first; a suite file's
// own are its parents'.
func TestSuiteChain(t *testing.T) {
	cases := map[string][]string{
		"suite/orders/a.art":     {"suite.art", "suite/suite.art", "suite/orders/suite.art"},
		"a.art":                  {"suite.art"},
		"suite/orders/suite.art": {"suite.art", "suite/suite.art"},
		"suite.art":              nil,
	}
	for file, want := range cases {
		got := SuiteChain(filepath.FromSlash(file))
		for i := range got {
			got[i] = filepath.ToSlash(got[i])
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("SuiteChain(%s) = %v, want %v", file, got, want)
		}
	}
}

func codes(unit *Unit) map[diag.Code]int {
	out := map[diag.Code]int{}
	for _, d := range unit.Bag.All() {
		out[d.Code]++
	}
	return out
}

// A scenario under a suite reads its names -- outer and inner merged -- through
// suite, and a name no suite gives is caught.
func TestASuitesNamesAreInScope(t *testing.T) {
	files := expand.MapLoader{
		"suite/suite.art": `suite {
  var url = "https://example.test"
  setup { step "log in" { post "${url}/login"
    capture token = body.token } }
}`,
		"suite/orders/suite.art": `suite {
  var orders = "${suite.url}/orders"
}`,
	}
	src := `scenario "s" {
  step "s" {
    get "${suite.orders}/1" { header "Authorization" = "Bearer ${suite.token}" }
    expect status == 200
  }
}
`
	if unit := CompileWith("suite/orders/a.art", src, files); unit.Bag.HasErrors() {
		t.Errorf("a scenario reading its suites' names: %v", unit.Bag.All())
	}
	bad := strings.Replace(src, "suite.token", "suite.tokn", 1)
	if got := codes(CompileWith("suite/orders/a.art", bad, files)); got[diag.UnknownField] != 1 {
		t.Errorf("suite.tokn: %v", got)
	}
	if unit := CompileWith("suite/orders/suite.art", files["suite/orders/suite.art"], files); unit.Bag.HasErrors() {
		t.Errorf("an inner suite reading the outer's url: %v", unit.Bag.All())
	}
}

// What a suite file may hold, and what only a suite file may.
func TestSuiteFileRules(t *testing.T) {
	outer := expand.MapLoader{"suite.art": `suite { var url = "x" }`}
	cases := []struct {
		file, src string
		code      diag.Code
	}{
		{"a.art", `suite { var x = 1 }`, diag.SuiteFileName},
		{"suite/suite.art", `suite { step "s" { run "true" } }`, diag.SuiteHasStep},
		{"suite/suite.art", `scenario "s" { step "s" { run "true" } }`, diag.SuiteHasScenario},
		{"suite/suite.art", "suite { var x = 1 }\nscenario \"s\" { step \"s\" { run \"true\" } }", diag.SuiteHasScenario},
		{"suite/suite.art", `suite { var url = "y" }`, diag.DuplicateSuiteName},
	}
	for _, c := range cases {
		if got := codes(CompileWith(c.file, c.src+"\n", outer)); got[c.code] == 0 {
			t.Errorf("%s: %s: want %s, got %v", c.file, c.src, c.code, got)
		}
	}
}
