package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	mcpserver "artemis/pkg/mcp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectWithBinary is connect with a real artemis for artemis_run to execute.
// Inside `go test` the running executable is the test binary, so a server that
// defaulted to "this one" would ask the test harness to run a scenario.
func connectWithBinary(t *testing.T, workspace, bin string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	server := mcpserver.New(mcpserver.Options{Workspace: workspace, Binary: bin})
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)

	st, ct := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("connecting the server: %v", err)
	}
	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("connecting the client: %v", err)
	}
	t.Cleanup(func() {
		clientSession.Close() //nolint:errcheck // the test is over
		serverSession.Wait()  //nolint:errcheck // the test is over
	})
	return clientSession
}

// A terminal scenario, so these tests need no server and no network. `run` is
// the action block that makes a step a terminal step.
const passingScenario = `scenario "ok" {
  step "true" {
    run "true"
    expect exit_code == 0
  }
}
`

const failingScenario = `scenario "no" {
  step "false" {
    run "false"
    expect exit_code == 0
  }
}
`

func TestWriteChecksFormatsAndWrites(t *testing.T) {
	root := t.TempDir()
	cs := connect(t, root)

	got := structured(t, call(t, cs, "artemis_write", map[string]any{
		"path":   "checkout.art",
		"source": "scenario    \"checkout\"   {\n  step \"s\" {\n    get \"http://example.test\"\n  }\n}\n",
	}))
	if got["ok"] != true || got["written"] != true {
		t.Fatalf("ok=%v written=%v, want both true; diagnostics: %v", got["ok"], got["written"], got["diagnostics"])
	}
	if got["created"] != true {
		t.Errorf("created = %v, want true for a path that did not exist", got["created"])
	}

	onDisk, err := os.ReadFile(filepath.Join(root, "checkout.art"))
	if err != nil {
		t.Fatalf("reading what was written: %v", err)
	}
	// The file is canonical, not the bytes handed in: the next edit to it is
	// then a one-line diff rather than a whole-file reformat.
	if string(onDisk) != got["source"] {
		t.Errorf("the file is not what the tool reported\n disk: %q\nsaid: %q", onDisk, got["source"])
	}
	if string(onDisk) == "scenario    \"checkout\"   {\n  step \"s\" {\n    get \"http://example.test\"\n  }\n}\n" {
		t.Error("the tool wrote the source verbatim; it must write the canonical form")
	}

	// Writing again over the same path reports it was not created.
	again := structured(t, call(t, cs, "artemis_write", map[string]any{
		"path": "checkout.art", "source": string(onDisk),
	}))
	if again["created"] != false {
		t.Errorf("created = %v, want false for a path that existed", again["created"])
	}
}

// The property that makes this tool safe to give an agent: it cannot leave a
// scenario on disk that the runner would refuse.
func TestWriteRefusesBrokenSourceAndTouchesNothing(t *testing.T) {
	root := t.TempDir()
	cs := connect(t, root)

	got := structured(t, call(t, cs, "artemis_write", map[string]any{
		"path": "broken.art", "source": "scenario {\n",
	}))
	if got["ok"] != false || got["written"] != false {
		t.Fatalf("ok=%v written=%v, want both false", got["ok"], got["written"])
	}
	if got["diagnostics"] == nil {
		t.Error("diagnostics are absent; the caller has nothing to act on")
	}
	if _, err := os.Stat(filepath.Join(root, "broken.art")); !os.IsNotExist(err) {
		t.Errorf("broken.art exists; the tool must write nothing when the source does not compile (stat err: %v)", err)
	}
}

func TestWriteRefusesPathsOutsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	cs := connect(t, root)

	for name, path := range map[string]string{
		"parent":      filepath.Join("..", "escape.art"),
		"not art":     "notes.md",
		"missing dir": filepath.Join("nope", "x.art"),
		"absolute":    filepath.Join(t.TempDir(), "x.art"),
	} {
		t.Run(name, func(t *testing.T) {
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "artemis_write",
				Arguments: map[string]any{"path": path, "source": passingScenario},
			})
			if err != nil {
				return // a transport-level refusal is still a refusal
			}
			if !res.IsError {
				t.Fatalf("artemis_write accepted %q: %+v", path, res.StructuredContent)
			}
		})
	}
}

// A passing run is reported as passing, and the document is the one
// `artemis run --report json` writes.
func TestRunReportsAPassingRun(t *testing.T) {
	bin := buildArtemis(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.art"), []byte(passingScenario), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	cs := connectWithBinary(t, root, bin)
	got := structured(t, call(t, cs, "artemis_run", map[string]any{"path": "ok.art"}))

	if got["passed"] != true {
		t.Fatalf("passed = %v, want true; report: %v", got["passed"], got)
	}
	if got["schema_version"] == nil {
		t.Error("the report has no schema_version; this is not the CLI's document")
	}
	if got["counts"] == nil || got["scenarios"] == nil {
		t.Error("the report is missing counts or scenarios")
	}
}

// The important one: a failing run is a successful call. If it came back as a
// tool error, an agent would set about repairing the runner instead of the
// test.
func TestRunReportsAFailingRunAsASuccessfulCall(t *testing.T) {
	bin := buildArtemis(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "no.art"), []byte(failingScenario), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	cs := connectWithBinary(t, root, bin)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "artemis_run", Arguments: map[string]any{"path": "no.art"},
	})
	if err != nil {
		t.Fatalf("calling artemis_run: %v", err)
	}
	if res.IsError {
		t.Fatalf("a failing run came back as a tool error: %+v", res.Content)
	}

	var got map[string]any
	if uErr := json.Unmarshal(documentBytes(t, res), &got); uErr != nil {
		t.Fatalf("the report is not JSON: %v", uErr)
	}
	if got["passed"] != false {
		t.Errorf("passed = %v, want false", got["passed"])
	}
	if got["failures"] == nil {
		t.Error("the report has no failures block; that is what an agent acts on")
	}
}

// The document the tool returns is the document the CLI prints, because it is
// the CLI that printed it.
func TestRunMatchesTheRunCommand(t *testing.T) {
	bin := buildArtemis(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.art"), []byte(passingScenario), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	cs := connectWithBinary(t, root, bin)
	res := call(t, cs, "artemis_run", map[string]any{"path": "ok.art"})

	// Fields that legitimately differ between two runs of the same scenario.
	volatile := func(doc []byte, t *testing.T) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal(doc, &m); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, doc)
		}
		delete(m, "started_at")
		delete(m, "duration_ms")
		delete(m, "scenarios")
		return m
	}

	want := runCLI(t, bin, root, "run", "ok.art", "--report", "json")
	gotJSON, _ := json.Marshal(volatile(documentBytes(t, res), t))
	wantJSON, _ := json.Marshal(volatile(want, t))
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("the tool and `artemis run --report json` disagree\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestRunRefusesPathsOutsideTheWorkspace(t *testing.T) {
	bin := buildArtemis(t)
	root := t.TempDir()
	cs := connectWithBinary(t, root, bin)

	for name, path := range map[string]string{
		"parent":  filepath.Join("..", "escape.art"),
		"not art": "notes.md",
		"missing": "nope.art",
	} {
		t.Run(name, func(t *testing.T) {
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "artemis_run", Arguments: map[string]any{"path": path},
			})
			if err != nil {
				return
			}
			if !res.IsError {
				t.Fatalf("artemis_run accepted %q: %+v", path, res.StructuredContent)
			}
		})
	}
}

// The loop the whole ticket exists for: write a scenario, then run it.
func TestWriteThenRun(t *testing.T) {
	bin := buildArtemis(t)
	root := t.TempDir()
	cs := connectWithBinary(t, root, bin)

	wrote := structured(t, call(t, cs, "artemis_write", map[string]any{
		"path": "smoke.art", "source": passingScenario,
	}))
	if wrote["ok"] != true {
		t.Fatalf("write failed: %v", wrote)
	}

	ran := structured(t, call(t, cs, "artemis_run", map[string]any{"path": "smoke.art"}))
	if ran["passed"] != true {
		t.Fatalf("the scenario this server just wrote does not pass: %v", ran)
	}
}

// runCLI runs a real artemis in dir and returns its stdout document. The exit
// status is ignored: a failing run still writes the report, which is the thing
// being compared.
func runCLI(t *testing.T, bin, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	if len(out) == 0 {
		t.Fatalf("`artemis %v` in %s wrote nothing", args, dir)
	}
	return out
}
