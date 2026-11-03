package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
)

// The fixtures under testdata/flows are a collection, one flow that calls it,
// and two scenario files that name the flow:
//
//	collections/auth.art   login and orders
//	flows/checkout.art     flow checkout
//	scenarios/checkout.art "full price", "smoke" (tagged smoke), "explicit code"
//	scenarios/admin.art    "admin smoke" (tagged smoke), "admin full"
const flowsDir = "testdata/flows"

// integration is the reason a test of the whole path is skipped until the
// parser reads flow and scenario files and expansion turns a scenario file into
// plain scenarios.
const integration = "integration: needs Tasks 6,9"

type flowReport struct {
	Scenarios []struct {
		Name   string   `json:"name"`
		File   string   `json:"file"`
		Flow   string   `json:"flow"`
		Status string   `json:"status"`
		Tags   []string `json:"tags"`
	} `json:"scenarios"`
}

func decodeFlowReport(t *testing.T, stdout, stderr string) flowReport {
	t.Helper()
	var doc flowReport
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("the report is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	return doc
}

// The flow's name reaches the report from the lowered scenario: the runner
// copies it onto the result, and the JSON report writes it. Expansion is what
// sets it in a real run; here it is set on the lowered scenario directly.
func TestTheRunCarriesTheFlowName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	path := writeArt(t, "checkout.art", "scenario \"full price\" {\n  step \"ping\" {\n    get \""+srv.URL+"/ping\"\n    expect status == 200\n  }\n}\n")

	c, err := loadArtFile(path, io.Discard)
	if err != nil {
		t.Fatalf("loadArtFile() = %v", err)
	}
	c.scenarios[0].FlowName = "checkout"
	run := result.NewRun()
	runCompiled(context.Background(), &runtimeEnv{reg: executor.Default()}, c, run, report.NewConsole(io.Discard))
	run.Finish()

	if got := run.Scenarios[0].Flow; got != "checkout" {
		t.Fatalf("ScenarioResult.Flow = %q, want checkout", got)
	}
	var buf bytes.Buffer
	if err := report.WriteJSON(&buf, run); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"flow": "checkout"`) {
		t.Errorf("the report does not name the flow:\n%s", buf.String())
	}
}

// A scenario the selection passes over is still in the report, flow and all.
func TestASkippedScenarioCarriesTheFlowName(t *testing.T) {
	path := writeArt(t, "checkout.art", "scenario \"full price\" {\n  step \"ping\" {\n    get \"http://127.0.0.1:1/ping\"\n  }\n}\n")
	c, err := loadArtFile(path, io.Discard)
	if err != nil {
		t.Fatalf("loadArtFile() = %v", err)
	}
	c.scenarios[0].FlowName = "checkout"
	c.selected = []bool{false}
	sel, err := newSelector("smoke", "")
	if err != nil {
		t.Fatal(err)
	}
	run := result.NewRun()
	runCompiled(context.Background(), &runtimeEnv{reg: executor.Default(), sel: sel}, c, run, report.NewConsole(io.Discard))
	if got := run.Scenarios[0]; got.Status != result.StatusSkip || got.Flow != "checkout" {
		t.Fatalf("scenario = %+v, want skipped with flow checkout", got)
	}
}

// A flow file named on the command line is a compile error that names the
// scenario files that use it, and nothing runs. It is read before the parser
// is asked, so this holds before the parser knows the flow form.
func TestRunAFlowFileIsAnError(t *testing.T) {
	t.Chdir(flowsDir)
	stdout, stderr, err := runCLI(t, "run", "flows/checkout.art", "--report", "json")
	if err == nil {
		t.Fatalf("want a non-zero exit\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	for _, want := range []string{
		"flows/checkout.art is a flow; run a scenario file that names it",
		"scenario files that name it: scenarios/admin.art, scenarios/checkout.art",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not hold %q:\n%s", want, stderr)
		}
	}
	doc := decodeFlowReport(t, stdout, stderr)
	if len(doc.Scenarios) != 1 || doc.Scenarios[0].Status != "error" || doc.Scenarios[0].File != "flows/checkout.art" {
		t.Errorf("the report holds %+v, want one errored entry for the flow file", doc.Scenarios)
	}
	if !strings.Contains(err.Error(), "flows/checkout.art:3:1") {
		t.Errorf("error = %v, want it at the flow keyword", err)
	}
}

// The diagnostic carries its code, at the flow keyword; any other file has
// none.
func TestFlowFileDiagIsFlowNotRunnable(t *testing.T) {
	d, ok := flowFileDiag("flows/checkout.art", "import \"a.art\"\n\nflow checkout {\n}\n", t.TempDir())
	if !ok || d.Code != diag.FlowNotRunnable || d.Severity != diag.Error || d.Span.Line != 3 {
		t.Errorf("flowFileDiag() = %+v, %v, want flow-not-runnable at line 3", d, ok)
	}
	if _, ok := flowFileDiag("s.art", "scenario \"s\" {}\n", t.TempDir()); ok {
		t.Error("a scenario has a flow-not-runnable diagnostic")
	}
}

// With no scenario file naming the flow, the hint says how to write one.
func TestRunAFlowFileNoOneUsesSaysHowToWriteOne(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTree(t, dir, map[string]string{
		"flows/lonely.art": "flow lonely {\n  step \"a\" {\n    get \"/\"\n  }\n}\n",
		"other.art":        "flow \"flows/elsewhere.art\"\n\nscenario \"s\" {}\n",
	})

	_, stderr, err := runCLI(t, "run", "flows/lonely.art")
	if err == nil {
		t.Fatal("want a non-zero exit")
	}
	if !strings.Contains(stderr, `write one: flow "flows/lonely.art"`) {
		t.Errorf("stderr does not say how to write a scenario file:\n%s", stderr)
	}
}

// The hint lists at most three scenario files.
func TestFlowUsersStopsAtTheLimit(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"flows/f.art": "flow f {\n}\n"}
	for _, name := range []string{"a", "b", "c", "d"} {
		files[name+".art"] = "flow \"flows/f.art\"\n\nscenario \"s\" {}\n"
	}
	writeTree(t, dir, files)
	got := flowUsers(filepath.Join(dir, "flows", "f.art"), dir, maxUsers)
	if strings.Join(got, ",") != "a.art,b.art,c.art" {
		t.Errorf("flowUsers() = %v, want the first three", got)
	}
}

// A folder run never names a flow file, so a flow file in it is not the error
// a named one is.
func TestAFlowFileInAFolderIsNotNamed(t *testing.T) {
	_, stderr, _ := runCLI(t, "run", flowsDir)
	if strings.Contains(stderr, "flow-not-runnable") {
		t.Errorf("a folder run reported a flow file it found:\n%s", stderr)
	}
}

func TestRunAScenarioFile(t *testing.T) {
	t.Skip(integration)
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	t.Chdir(flowsDir)
	stdout, stderr, err := runCLI(t, "run", "scenarios/checkout.art", "--report", "json")
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	doc := decodeFlowReport(t, stdout, stderr)
	want := []string{"full price", "smoke", "explicit code"}
	if len(doc.Scenarios) != len(want) {
		t.Fatalf("the report holds %d scenarios, want %d: %+v", len(doc.Scenarios), len(want), doc.Scenarios)
	}
	for i, sc := range doc.Scenarios {
		if sc.Name != want[i] || sc.Flow != "checkout" || sc.File != "scenarios/checkout.art" || sc.Status != "pass" {
			t.Errorf("scenario %d = %+v, want %q of flow checkout in scenarios/checkout.art, passed", i, sc, want[i])
		}
	}
}

func TestRunAFolderSkipsFlowAndCollectionFiles(t *testing.T) {
	t.Skip(integration)
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	stdout, stderr, err := runCLI(t, "run", flowsDir, "--report", "json")
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	doc := decodeFlowReport(t, stdout, stderr)
	var names []string
	for _, sc := range doc.Scenarios {
		names = append(names, sc.Name)
		if !strings.HasPrefix(sc.File, flowsDir+"/scenarios/") {
			t.Errorf("%q is from %s, want only the scenario files' scenarios", sc.Name, sc.File)
		}
	}
	if got := strings.Join(names, ", "); got != "admin smoke, admin full, full price, smoke, explicit code" {
		t.Errorf("scenarios = %s", got)
	}
}

func TestTagsSelectAcrossScenarioFiles(t *testing.T) {
	t.Skip(integration)
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	stdout, stderr, err := runCLI(t, "run", flowsDir, "--tags", "smoke", "--report", "json")
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	ran := map[string]string{}
	for _, sc := range decodeFlowReport(t, stdout, stderr).Scenarios {
		if sc.Status != "skip" {
			ran[sc.Name] = sc.File
		}
	}
	want := map[string]string{
		"admin smoke": flowsDir + "/scenarios/admin.art",
		"smoke":       flowsDir + "/scenarios/checkout.art",
	}
	if len(ran) != len(want) {
		t.Fatalf("ran %v, want %v", ran, want)
	}
	for name, file := range want {
		if ran[name] != file {
			t.Errorf("%q ran from %q, want %q", name, ran[name], file)
		}
	}
}
