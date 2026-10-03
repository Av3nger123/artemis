package codegen

import (
	"os/exec"
	"strings"
	"testing"
)

// Every helper the table lists has to be reachable by its name, or a use() call
// would record a helper and emit nothing.
func TestEveryHelperIsInTheLookup(t *testing.T) {
	for _, h := range helpers {
		if byName[h.name].name != h.name {
			t.Errorf("helper %q is in the table and not in the lookup", h.name)
		}
	}
	if len(byName) != len(helpers) {
		t.Errorf("the table has %d helpers and the lookup %d; two share a name",
			len(helpers), len(byName))
	}
}

// A helper's Python has to define the name it is recorded under, or the generated
// file calls something it does not define.
func TestEveryHelperDefinesItsOwnName(t *testing.T) {
	for _, h := range helpers {
		if !strings.Contains(h.src, "def "+h.name+"(") && !strings.Contains(h.src, "class "+h.name+":") {
			t.Errorf("helper %q does not define %s", h.name, h.name)
		}
	}
}

// Every module a helper names has an import line and a place in the groups, so a
// helper cannot pull in a module the file never imports.
func TestEveryHelperImportIsKnown(t *testing.T) {
	grouped := map[string]bool{}
	for _, group := range importGroups {
		for _, m := range group {
			if grouped[m] {
				t.Errorf("%q is in two import groups", m)
			}
			grouped[m] = true
		}
	}
	for m := range importLines {
		if !grouped[m] {
			t.Errorf("import %q has a line and no group, so it would never be written", m)
		}
	}
	for _, h := range helpers {
		for _, m := range h.imports {
			if importLines[m] == "" {
				t.Errorf("helper %q imports %q, which has no import line", h.name, m)
			}
		}
	}
}

// use() pulls a helper's dependencies, so a file that calls art_contains also
// defines art_render.
func TestUsePullsDependencies(t *testing.T) {
	f := &pyFile{used: map[string]bool{}, mods: map[string]bool{}}
	f.use(helperContains)
	if !f.used[helperRender] {
		t.Error("art_contains was used without art_render")
	}
	if !f.mods["json"] {
		t.Error("art_render was used without its json import")
	}
}

// A helper that names a dependency has to actually call it, and one that calls a
// helper has to name it -- otherwise a file emits a NameError or carries a
// definition nothing uses.
func TestHelperDependenciesMatchTheirCalls(t *testing.T) {
	for _, h := range helpers {
		for _, other := range helpers {
			if other.name == h.name {
				continue
			}
			calls := strings.Contains(h.src, other.name+"(")
			declared := false
			for _, need := range h.needs {
				if need == other.name {
					declared = true
				}
			}
			if calls && !declared {
				t.Errorf("helper %q calls %s and does not declare it as a dependency", h.name, other.name)
			}
			if declared && !calls {
				t.Errorf("helper %q declares %s as a dependency and never calls it", h.name, other.name)
			}
		}
	}
}

// Every helper on its own is valid Python. The whole module is checked by the
// golden test; this is the finer-grained failure, so a broken helper names
// itself rather than failing eight goldens at once.
func TestEveryHelperIsValidPython(t *testing.T) {
	python := python3(t)
	for _, h := range helpers {
		src := h.src
		if h.name == helperBrowser {
			// The fixture's decorator and its two playwright names come from the
			// module's imports, which a helper on its own does not have.
			src = "import pytest\nfrom playwright.sync_api import sync_playwright\n" + src
		}
		if err := parsePython(python, src); err != nil {
			t.Errorf("helper %q is not valid python: %v", h.name, err)
		}
	}
}

// python3 is the interpreter to check with, or a skip.
//
// Skipping rather than failing is how `make lint` treats a missing linter: a tool
// that is not installed must not stop anyone building the binary, and CI has
// python3.
func python3(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; skipping the python syntax check")
	}
	return path
}

// parsePython asks Python whether src parses, which is the only authority on the
// question.
func parsePython(python, src string) error {
	cmd := exec.Command(python, "-c", "import ast,sys; ast.parse(sys.stdin.read())") //nolint:gosec // python is from LookPath
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &pythonError{out: string(out), err: err}
	}
	return nil
}

type pythonError struct {
	out string
	err error
}

func (e *pythonError) Error() string { return e.err.Error() + "\n" + e.out }
