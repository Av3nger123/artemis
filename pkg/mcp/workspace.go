package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
)

// workspace is the directory tool paths resolve against, and the boundary no
// tool reads outside of.
//
// The boundary is a security property and not hygiene. artemis_run -- not in
// this version -- executes a `terminal` step's shell command, so the set of
// files a caller can name is the set of commands it can cause to run. This type
// is where that set is decided, which is why it is one type with one check
// rather than a filepath.Join at each call site.
type workspace struct {
	// root is absolute and symlink-resolved, so that comparing a resolved
	// candidate against it compares two paths of the same kind.
	root string
}

// newWorkspace resolves root once, when the server starts.
//
// Resolving here rather than per call means a workspace that is itself reached
// through a symlink -- /tmp on macOS, every time -- does not refuse every path
// inside it for failing to have the prefix it was handed.
func newWorkspace(root string) (workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return workspace{}, fmt.Errorf("resolving the workspace %s: %w", root, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return workspace{}, fmt.Errorf("resolving the workspace %s: %w", abs, err)
	}
	return workspace{root: real}, nil
}

// resolve turns a workspace-relative path into an absolute one inside the
// workspace, or returns an error naming the rule that was broken.
//
// An absolute path is refused outright rather than accepted when it happens to
// point inside. A caller that sends one is not using the contract, and the
// narrow rule is the one that stays true when the workspace moves.
//
// Symlinks are resolved before the prefix check, which is the whole point: a
// link inside the workspace pointing out of it passes a string comparison and
// fails this.
func (w workspace) resolve(rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s: the path must be relative to the workspace", rel)
	}

	joined := filepath.Join(w.root, rel)
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		// The path does not exist, or a component of it does not. Reported as
		// the caller's path rather than the resolved one, which they have
		// never seen and cannot act on.
		return "", fmt.Errorf("%s: %w", rel, err)
	}
	if real != w.root && !strings.HasPrefix(real, w.root+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: the path leaves the workspace", rel)
	}
	return real, nil
}

// resolveArt is resolve with the extension rule, for a tool that is given a
// scenario.
//
// The extension is checked on the path the caller wrote, before anything is
// opened: `notes.md` is refused for being the wrong kind of file, which is a
// more useful answer than a page of diagnostics about a file that was never a
// scenario.
func (w workspace) resolveArt(rel string) (string, error) {
	if !front.IsArtFile(rel) {
		return "", fmt.Errorf("%s: not a scenario file (artemis reads %s files)", rel, front.Ext)
	}
	return w.resolve(rel)
}

// resolveNew is resolve for a path that does not exist yet, which is what a
// write is given.
//
// resolve cannot serve it: it resolves symlinks, and a path with no file at the
// end of it has nothing to resolve. So the parent is checked instead -- the
// parent must exist and must be inside the workspace -- and the name is joined
// onto the resolved parent. A caller cannot escape through a directory that is
// not there, and cannot create the directory either.
func (w workspace) resolveNew(rel string) (string, error) {
	if !front.IsArtFile(rel) {
		return "", fmt.Errorf("%s: not a scenario file (artemis writes %s files)", rel, front.Ext)
	}
	dir, name := filepath.Split(rel)
	parent, err := w.resolve(filepath.Clean(dir))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, name), nil
}

// resolveRunTarget is resolve for the path a run is given, which is a scenario
// file or a folder of them.
//
// `artemis run` takes either, so this does too. The extension rule applies only
// to a regular file: a folder is walked for *.art and is not itself one.
func (w workspace) resolveRunTarget(rel string) (string, error) {
	abs, err := w.resolve(rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", rel, err)
	}
	if !info.IsDir() && !front.IsArtFile(rel) {
		return "", fmt.Errorf("%s: not a scenario file (artemis runs %s files)", rel, front.Ext)
	}
	return abs, nil
}

// loader resolves imports through the same boundary every tool uses, so an
// import is never a way to read outside the workspace.
func (w workspace) loader() expand.Loader { return wsLoader{w} }

type wsLoader struct{ w workspace }

// Load reads p, written in the file named from. from is the label the tool
// compiled under, which is workspace-relative when the caller named a real
// file and a placeholder (or a path outside the workspace) when it did not;
// either way the joined path goes through resolveArt, so it is refused unless
// it is a scenario file inside the workspace.
func (l wsLoader) Load(from, p string) (string, string, error) {
	rel := filepath.Join(filepath.Dir(from), p)
	abs, err := l.w.resolveArt(rel)
	if err != nil {
		return "", "", err
	}
	b, err := os.ReadFile(abs) //nolint:gosec // resolveArt kept it inside the workspace
	if err != nil {
		return "", "", err
	}
	return rel, string(b), nil
}
