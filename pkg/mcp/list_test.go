package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listFiles is the files a list call returned.
func listFiles(t *testing.T, cs *mcp.ClientSession, args map[string]any) []string {
	t.Helper()
	got := structured(t, call(t, cs, "artemis_list", args))
	raw, err := json.Marshal(got["files"])
	if err != nil {
		t.Fatalf("re-encoding files: %v", err)
	}
	var files []string
	if err := json.Unmarshal(raw, &files); err != nil {
		t.Fatalf("files is not a list of strings: %v", err)
	}
	return files
}

// workspaceWithFiles builds a tree with .art files at two depths, a file that
// is not a scenario, and a dot-directory that must not be walked.
func workspaceWithFiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{
		"checkout.art",
		filepath.Join("orders", "list.art"),
		filepath.Join("orders", "admin", "create.art"),
		"notes.md",
		filepath.Join(".git", "hooks.art"),
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("making %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte("flow x {}\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", full, err)
		}
	}
	return root
}

func TestListFindsArtFilesInPathOrder(t *testing.T) {
	cs := connect(t, workspaceWithFiles(t))
	files := listFiles(t, cs, map[string]any{})

	want := []string{
		"checkout.art",
		filepath.Join("orders", "admin", "create.art"),
		filepath.Join("orders", "list.art"),
	}
	if len(files) != len(want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("files[%d] = %q, want %q (full list %v)", i, files[i], want[i], files)
		}
	}
}

// A dir narrows the walk, and the paths stay workspace-relative so they can be
// handed to another tool unchanged.
func TestListNarrowsToADirAndStaysWorkspaceRelative(t *testing.T) {
	cs := connect(t, workspaceWithFiles(t))
	files := listFiles(t, cs, map[string]any{"dir": "orders"})

	want := []string{
		filepath.Join("orders", "admin", "create.art"),
		filepath.Join("orders", "list.art"),
	}
	if len(files) != len(want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("files[%d] = %q, want %q", i, files[i], want[i])
		}
	}
}

// The boundary applies to this tool too. A dir that leaves the workspace is a
// tool error, because the caller broke a rule and no document describes that.
func TestListRefusesADirOutsideTheWorkspace(t *testing.T) {
	cs := connect(t, workspaceWithFiles(t))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "artemis_list", Arguments: map[string]any{"dir": ".."},
	})
	if err != nil {
		// A transport-level error is also a refusal; what must not happen is a
		// successful listing of a directory outside the workspace.
		return
	}
	if !res.IsError {
		t.Fatalf("artemis_list accepted a dir outside the workspace: %+v", res.StructuredContent)
	}
}

// A scenario file is listed with the flow its header names and the titles of
// its scenarios; a flow file and a plain scenario are not scenario files.
func TestListNamesEachScenarioFilesFlow(t *testing.T) {
	root := t.TempDir()
	for rel, src := range map[string]string{
		filepath.Join("flows", "checkout.art"):     "import \"../collections/auth.art\"\n\nflow checkout {\n}\n",
		filepath.Join("scenarios", "checkout.art"): "flow \"../flows/checkout.art\"\n\nscenario \"full price\" {}\n\nscenario \"coupon\" {}\n",
		"plain.art": "scenario \"x\" {}\n",
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := structured(t, call(t, connect(t, root), "artemis_list", nil))["scenario_files"]
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var files map[string]struct {
		FlowFile  string   `json:"flow_file"`
		Flow      string   `json:"flow"`
		Scenarios []string `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &files); err != nil {
		t.Fatalf("scenario_files shape: %s", raw)
	}
	if len(files) != 1 {
		t.Fatalf("scenario_files = %s, want only scenarios/checkout.art", raw)
	}
	sf := files[filepath.Join("scenarios", "checkout.art")]
	if sf.FlowFile != filepath.Join("flows", "checkout.art") || sf.Flow != "checkout" ||
		len(sf.Scenarios) != 2 || sf.Scenarios[0] != "full price" || sf.Scenarios[1] != "coupon" {
		t.Errorf("scenarios/checkout.art = %+v", sf)
	}
}
