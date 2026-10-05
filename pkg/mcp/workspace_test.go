package mcpserver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The boundary as a table. Every row is a way out of the workspace that
// somebody will eventually try.
func TestResolveRefusesEveryWayOut(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "orders"), 0o755); err != nil {
		t.Fatalf("making the fixture directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "orders", "checkout.art"), []byte("scenario \"x\" {}\n"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	// A symlink inside the workspace that points out of it. The check has to
	// happen after resolution, or this is the way through.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "escape.art"), []byte("scenario \"x\" {}\n"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	linked := true
	if err := os.Symlink(filepath.Join(outside, "escape.art"), filepath.Join(root, "link.art")); err != nil {
		if runtime.GOOS == "windows" {
			linked = false
		} else {
			t.Fatalf("making the symlink: %v", err)
		}
	}

	w, err := newWorkspace(root)
	if err != nil {
		t.Fatalf("newWorkspace: %v", err)
	}

	t.Run("accepts a path inside the workspace", func(t *testing.T) {
		if _, err := w.resolveArt(filepath.Join("orders", "checkout.art")); err != nil {
			t.Fatalf("resolveArt refused a path inside the workspace: %v", err)
		}
	})

	refused := map[string]string{
		"parent":   filepath.Join("..", "outside.art"),
		"absolute": filepath.Join(outside, "escape.art"),
		"not art":  "notes.md",
	}
	if linked {
		refused["symlink out"] = "link.art"
	}
	for name, rel := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			if got, err := w.resolveArt(rel); err == nil {
				t.Fatalf("resolveArt(%q) = %q, want an error", rel, got)
			}
		})
	}
}

// resolve is resolveArt without the extension rule: artemis_list is given a
// directory, which is not a .art file and must still be accepted.
func TestResolveAcceptsADirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "orders"), 0o755); err != nil {
		t.Fatalf("making the fixture directory: %v", err)
	}
	w, err := newWorkspace(root)
	if err != nil {
		t.Fatalf("newWorkspace: %v", err)
	}
	if _, err := w.resolve("orders"); err != nil {
		t.Fatalf("resolve refused a directory inside the workspace: %v", err)
	}
	if _, err := w.resolve(".."); err == nil {
		t.Fatal("resolve accepted the parent directory")
	}
}

// A workspace that never resolved refuses everything. New leaves it zero rather
// than failing to start, so this is the behaviour that keeps a bad --workspace
// from reading anything.
func TestZeroWorkspaceRefusesEverything(t *testing.T) {
	var w workspace
	for _, rel := range []string{".", "x.art", filepath.Join("orders", "x.art")} {
		if got, err := w.resolve(rel); err == nil {
			t.Errorf("zero workspace resolved %q to %q, want an error", rel, got)
		}
	}
}
