package cli

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/token"
)

func need(name string, line int) check.EnvNeed {
	return check.EnvNeed{Name: name, Span: token.Span{Line: line}}
}

func TestEnvGateNamesEveryAbsentVariable(t *testing.T) {
	files := []compiled{
		{file: "suite/01.art", needs: []check.EnvNeed{need("API_URL", 4), need("API_PASSWORD", 5)}},
		{file: "suite/03.art", needs: []check.EnvNeed{need("REPORT_URL", 7)}},
	}
	set := map[string]string{"API_PASSWORD": "secret"}
	faults := envGate(files, func(name string) (string, bool) {
		v, ok := set[name]
		return v, ok
	})

	if len(faults) != 2 {
		t.Fatalf("got %d faults, want 2: %v", len(faults), faults)
	}
	if faults[0].Name != "API_URL" || faults[0].File != "suite/01.art" || faults[0].Line != 4 {
		t.Fatalf("the first fault is wrong: %+v", faults[0])
	}
	if faults[1].Name != "REPORT_URL" {
		t.Fatalf("the second fault is wrong: %+v", faults[1])
	}
}

func TestEnvGateCountsAnEmptyValueAsAbsent(t *testing.T) {
	files := []compiled{{file: "a.art", needs: []check.EnvNeed{need("API_URL", 1)}}}
	faults := envGate(files, func(string) (string, bool) { return "   ", true })
	if len(faults) != 1 {
		t.Fatalf("a blank value passed the gate")
	}
}

func TestEnvGatePassesAFullEnvironment(t *testing.T) {
	files := []compiled{{file: "a.art", needs: []check.EnvNeed{need("API_URL", 1)}}}
	faults := envGate(files, func(string) (string, bool) { return "http://x", true })
	if len(faults) != 0 {
		t.Fatalf("got %v, want no fault", faults)
	}
}

func TestEnvGateReportNamesTheEnvFile(t *testing.T) {
	var b strings.Builder
	envGateReport(&b, []envFault{{Name: "API_URL", File: "a.art", Line: 4}}, ".env")
	out := b.String()
	for _, want := range []string{"API_URL", "a.art:4", ".env", "nothing ran"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the report does not hold %q:\n%s", want, out)
		}
	}
}

// The env-file clause is conditional, because the usual cause of an absent
// variable is the file and "read .env" in a directory that has no .env sends
// the reader to inspect a file that is not there. An empty path is how
// reportRun says nothing loaded.
func TestEnvGateReportOmitsTheEnvFileClauseWhenNothingWasRead(t *testing.T) {
	var b strings.Builder
	envGateReport(&b, []envFault{{Name: "API_URL", File: "a.art", Line: 4}}, "")
	out := b.String()
	if strings.Contains(out, "read ") {
		t.Fatalf("the report claims to have read a file:\n%s", out)
	}
	for _, want := range []string{"API_URL", "a.art:4", "no value for 1 name", "nothing ran"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the report does not hold %q:\n%s", want, out)
		}
	}
}
