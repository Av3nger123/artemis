package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The mirror of helpers_test.go. Every check there is a check here, because the
// failure modes are the language's and not Python's: a helper recorded under a
// name it does not define is a ReferenceError in a generated file, and a
// dependency that is declared and not called is a definition nothing uses.

// Every helper the table lists has to be reachable by its name, or a use() call
// would record a helper and emit nothing.
func TestEveryJSHelperIsInTheLookup(t *testing.T) {
	for _, h := range jsHelpers {
		if jsByName[h.name].name != h.name {
			t.Errorf("helper %q is in the table and not in the lookup", h.name)
		}
	}
	if len(jsByName) != len(jsHelpers) {
		t.Errorf("the table has %d helpers and the lookup %d; two share a name",
			len(jsHelpers), len(jsByName))
	}
}

// A helper's JavaScript has to define the name it is recorded under, or the
// generated file calls something it does not define.
func TestEveryJSHelperDefinesItsOwnName(t *testing.T) {
	for _, h := range jsHelpers {
		if !strings.Contains(h.src, "function "+h.name+"(") {
			t.Errorf("helper %q does not define %s", h.name, h.name)
		}
	}
}

// Every binding a helper imports is one a module can actually import, and every
// importable binding's module has a place in the groups -- otherwise a helper
// pulls in a name the import block would never write.
func TestEveryJSHelperImportIsKnown(t *testing.T) {
	grouped := map[string]bool{}
	for _, group := range jsImportGroups {
		for _, m := range group {
			if grouped[m] {
				t.Errorf("%q is in two import groups", m)
			}
			grouped[m] = true
		}
	}
	for member := range jsImportable {
		module, name, ok := strings.Cut(member, ".")
		if !ok || name == "" {
			t.Errorf("%q is not a module.member pair", member)
			continue
		}
		if !grouped[module] {
			t.Errorf("%q names the module %q, which has no import group, so it would never be written",
				member, module)
		}
	}
	for _, h := range jsHelpers {
		for _, m := range h.imports {
			if !jsImportable[m] {
				t.Errorf("helper %q imports %q, which is not importable", h.name, m)
			}
		}
	}
}

// use() pulls a helper's dependencies, so a file that calls art_contains also
// defines art_render and art_eq.
func TestJSUsePullsDependencies(t *testing.T) {
	f := &jsFile{used: map[string]bool{}, imports: map[string]bool{}}
	f.use(jsHelperContains)
	for _, want := range []string{jsHelperRender, jsHelperEq} {
		if !f.used[want] {
			t.Errorf("art_contains was used without %s", want)
		}
	}

	// And the browser launcher pulls both of the bindings it needs: a file that
	// opens a page needs chromium to launch it and onTestFinished to close it,
	// and one without the other leaks a browser or does not compile.
	g := &jsFile{used: map[string]bool{}, imports: map[string]bool{}}
	g.use(jsHelperBrowser)
	for _, want := range []string{"@playwright/test.chromium", "vitest.onTestFinished"} {
		if !g.imports[want] {
			t.Errorf("art_browser was used without importing %s", want)
		}
	}
}

// A helper that names a dependency has to actually call it, and one that calls a
// helper has to name it -- otherwise a file emits a ReferenceError or carries a
// definition nothing uses.
func TestJSHelperDependenciesMatchTheirCalls(t *testing.T) {
	for _, h := range jsHelpers {
		for _, other := range jsHelpers {
			if other.name == h.name {
				continue
			}
			calls := strings.Contains(stripComments(h.src), other.name+"(")
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

// stripComments drops the `//` comments, so a helper named in a comment is not
// read as a call. The helpers' own prose mentions the Python spellings and each
// other by name, which is the whole reason this is needed.
func stripComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// Every helper on its own is valid JavaScript. The whole module is checked by
// the golden test; this is the finer-grained failure, so a broken helper names
// itself rather than failing seven goldens at once.
func TestEveryJSHelperIsValidJavaScript(t *testing.T) {
	node := nodeBinary(t)
	dir := t.TempDir()
	for _, h := range jsHelpers {
		src := h.src
		if h.name == jsHelperBrowser {
			// Its two imported bindings come from the module's import block,
			// which a helper on its own does not have.
			src = "import { chromium } from \"@playwright/test\";\n" +
				"import { onTestFinished } from \"vitest\";\n" + src
		}
		if err := parseJavaScript(node, dir, h.name, src); err != nil {
			t.Errorf("helper %q is not valid javascript: %v", h.name, err)
		}
	}
}

// Every helper's JavaScript name has the prefix, so jsLocal() renames around all
// of them without listing them.
func TestEveryJSHelperNameIsCoveredByThePrefixRule(t *testing.T) {
	for _, h := range jsHelpers {
		if !strings.HasPrefix(h.name, helperPrefix) {
			t.Errorf("helper %q does not start with %q", h.name, helperPrefix)
		}
		if jsLocal(h.name) == h.name {
			t.Errorf("jsLocal(%q) = %q; a capture with that name would shadow the helper", h.name, h.name)
		}
	}
}

// nodeBinary is the interpreter to check with, or a skip.
//
// Skipping rather than failing is how `make lint` treats a missing linter and how
// python3(t) treats a missing python3: a toolchain nobody asked for must not stop
// anyone building the binary, and CI installs node.
func nodeBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping the javascript syntax check")
	}
	return path
}

// parseJavaScript asks node whether src parses as a module, which is the only
// authority on the question.
//
// Through a .mjs file rather than stdin, because `node --check` decides a file is
// ESM from its extension and reads stdin as a script -- so an `import` statement
// would be reported as an error in a file that is correct.
func parseJavaScript(node, dir, name, src string) error {
	path := filepath.Join(dir, name+".mjs")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		return err
	}
	out, err := exec.Command(node, "--check", path).CombinedOutput() //nolint:gosec // node came from exec.LookPath
	if err != nil {
		return &nodeError{out: string(out), err: err}
	}
	return nil
}

type nodeError struct {
	out string
	err error
}

func (e *nodeError) Error() string { return e.err.Error() + "\n" + e.out }
