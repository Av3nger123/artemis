package mcpserver_test

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, src string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

const collSrc = "collection \"c\" {\n  request r() {\n    get \"x\"\n    expect status == 200\n  }\n  flow f() {\n  }\n}\n"

func TestValidateResolvesImportsInsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "c.art", collSrc)
	cs := connect(t, root)
	got := validate(t, cs, map[string]any{
		"file":   "main.art",
		"source": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r\n}\n",
	})
	if got["ok"] != true {
		t.Fatalf("got %+v", got)
	}
}

func TestFormatResolvesImportsAndKeepsTheTree(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "c.art", collSrc)
	cs := connect(t, root)
	src := "import \"c.art\"\n\nscenario \"s\" {\n  use c.r\n}\n"
	_, got := formatSource(t, cs, src)
	if got["ok"] != true || got["source"] != src {
		t.Fatalf("got %+v", got)
	}
}

func TestImportCannotLeaveTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "ws")
	writeFile(t, root, "keep.art", "scenario \"x\" {}\n")
	writeFile(t, parent, "outside.art", collSrc)
	cs := connect(t, root)
	got := validate(t, cs, map[string]any{
		"file":   "main.art",
		"source": "import \"../outside.art\"\n",
	})
	raw := toJSON(t, got)
	if got["ok"] == true || !strings.Contains(raw, "import-not-found") {
		t.Fatalf("got %s", raw)
	}
}

func TestImportCannotFollowASymlinkOut(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "ws")
	writeFile(t, root, "keep.art", "scenario \"x\" {}\n")
	writeFile(t, parent, "outside.art", collSrc)
	if err := os.Symlink(filepath.Join(parent, "outside.art"), filepath.Join(root, "link.art")); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, root)
	got := validate(t, cs, map[string]any{"file": "main.art", "source": "import \"link.art\"\n"})
	if got["ok"] == true || !strings.Contains(toJSON(t, got), "import-not-found") {
		t.Fatalf("got %+v", got)
	}
}

func TestRunRefusesAnImportThatLeavesTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "ws")
	writeFile(t, parent, "outside.art", collSrc)
	writeFile(t, root, "main.art", "import \"../outside.art\"\n\nscenario \"s\" {\n  use c.r\n}\n")
	cs := connectWithBinary(t, root, buildArtemis(t))
	res, err := cs.CallTool(context.Background(), callParams("artemis_run", map[string]any{"path": "main.art"}))
	if err == nil && !res.IsError {
		t.Fatalf("expected a refusal, got %+v", res)
	}
	if err == nil {
		if txt := toJSON(t, res.Content); !strings.Contains(txt, "outside.art") {
			t.Fatalf("the refusal must name the import: %s", txt)
		}
	} else if !strings.Contains(err.Error(), "outside.art") {
		t.Fatalf("the refusal must name the import: %v", err)
	}
}

func TestListShowsCollections(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "c.art", collSrc)
	writeFile(t, root, "plain.art", "scenario \"x\" {}\n")
	cs := connect(t, root)
	out := toJSON(t, structured(t, call(t, cs, "artemis_list", nil)))
	if !strings.Contains(out, `"c.r"`) || !strings.Contains(out, `"c.f"`) {
		t.Fatalf("got %s", out)
	}
	cols, _ := structured(t, call(t, cs, "artemis_list", nil))["collections"].(map[string]any)
	if got := toJSON(t, cols["c.art"]); got != `["c.r","c.f"]` || len(cols) != 1 {
		t.Fatalf("collections shape: %s", toJSON(t, cols))
	}
	if got := listFiles(t, cs, nil); len(got) != 2 {
		t.Fatalf("files must stay a list of paths: %v", got)
	}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func callParams(name string, args map[string]any) *mcp.CallToolParams {
	return &mcp.CallToolParams{Name: name, Arguments: args}
}

func TestListDoesNotReadASymlinkOutOfTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "ws")
	writeFile(t, root, "keep.art", "scenario \"x\" {}\n")
	writeFile(t, parent, "outside.art", "collection \"secret\" {\n  request leak() {\n    get \"x\"\n  }\n}\n")
	if err := os.Symlink(filepath.Join(parent, "outside.art"), filepath.Join(root, "link.art")); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, root)
	if out := toJSON(t, structured(t, call(t, cs, "artemis_list", nil))); strings.Contains(out, "secret") {
		t.Fatalf("read through the symlink: %s", out)
	}
}

func TestRefusedImportsLeakNoHostPathAndDoNotProbe(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "ws")
	writeFile(t, root, "keep.art", "scenario \"x\" {}\n")
	writeFile(t, parent, "exists.art", collSrc)
	cs := connect(t, root)
	msg := func(imp string) string {
		got := validate(t, cs, map[string]any{"file": "main.art", "source": "import \"" + imp + "\"\n"})
		return toJSON(t, got["diagnostics"])
	}
	a, b := msg("../exists.art"), msg("../absent.art")
	if strings.Contains(a, parent) || strings.Contains(b, parent) || strings.Contains(a, "lstat") {
		t.Fatalf("leaks a host path: %s / %s", a, b)
	}
	if strings.ReplaceAll(a, "exists", "X") != strings.ReplaceAll(b, "absent", "X") {
		t.Fatalf("existence is observable:\n%s\n%s", a, b)
	}
}
