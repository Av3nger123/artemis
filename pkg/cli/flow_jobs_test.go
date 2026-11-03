package cli

import (
	"bytes"
	"strings"
	"testing"

	"artemis/pkg/executor"
	"artemis/pkg/report"
)

// Review Focus 1 under --jobs 2: two scenario files that name one broken flow
// each report its fault once, and nothing races (go test -race).
func TestFlowErrorReportedPerScenarioFileUnderJobs(t *testing.T) {
	t.Skip("integration: needs Task 6")
	initLog(t)
	root := writeTree(t, t.TempDir(), map[string]string{
		"flows/checkout.art": "flow checkout {\n  step \"pay\" {\n    get \"/${nope}\"\n  }\n}\n",
		"a.art":              "flow \"flows/checkout.art\"\nscenario \"a1\" { }\n",
		"b.art":              "flow \"flows/checkout.art\"\nscenario \"b1\" { }\n",
	})
	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v", err)
	}
	var diags bytes.Buffer
	runFilesWith(&runtimeEnv{reg: executor.Default(), jobs: 2}, files, report.Discard(), &diags)
	if n := strings.Count(diags.String(), "unknown-identifier"); n != 2 {
		t.Fatalf("want the flow's fault once for each scenario file, got %d:\n%s", n, diags.String())
	}
}
