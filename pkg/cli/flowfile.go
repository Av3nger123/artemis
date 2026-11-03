package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/front"
)

// A flow runs only through a scenario file that names it, so a flow file named
// on the command line is a compile error rather than a run of nothing. A flow
// file a folder run finds is not named, and compiles to no scenario, as a
// collection file does.

// maxUsers is how many scenario files the hint of flow-not-runnable lists.
const maxUsers = 3

// maxVisited bounds the walk that looks for them: `artemis run flows/x.art`
// from a home directory must not read every file under it to write a hint.
const maxVisited = 20000

// errWalkDone stops the walk once it has what it needs.
var errWalkDone = errors.New("done")

// flowNotRunnable reports file, a path named on the command line, when it is a
// flow file: the diagnostic, rendered to diagOut, and the one-line error that
// fails the run. isFlow is false for any other file, and for one that cannot
// be read, which loading reports.
func flowNotRunnable(file string, diagOut io.Writer) (isFlow bool, err error) {
	src, readErr := os.ReadFile(file) //nolint:gosec // the path is the one the user named
	if readErr != nil {
		return false, nil
	}
	d, isFlow := flowFileDiag(file, string(src), ".")
	if !isFlow {
		return false, nil
	}
	files := diag.NewFiles()
	files.Add(file, string(src))
	if termErr := diag.Terminal(diagOut, files, []diag.Diagnostic{d}); termErr != nil {
		return true, termErr
	}
	return true, firstError([]diag.Diagnostic{d})
}

// flowFileDiag is the flow-not-runnable diagnostic for file, at its flow
// keyword, with the scenario files under root that name it in the hint. ok is
// false when file is not a flow file.
func flowFileDiag(file, src, root string) (d diag.Diagnostic, ok bool) {
	kw, _, isFlow := front.FlowDecl(file, src)
	if !isFlow {
		return diag.Diagnostic{}, false
	}
	return diag.Diagnostic{
		Span:     kw.Span,
		Code:     diag.FlowNotRunnable,
		Severity: diag.Error,
		Message:  fmt.Sprintf("%s is a flow; run a scenario file that names it", file),
		Hint:     flowUsersHint(file, root, maxUsers),
	}, true
}

// flowUsersHint is the hint for a flow file named on the command line: up to
// limit scenario files under root whose header names it, or how to write one
// when there is none.
func flowUsersHint(flowFile, root string, limit int) string {
	users := flowUsers(flowFile, root, limit)
	if len(users) == 0 {
		rel := relSlash(root, flowFile)
		return fmt.Sprintf("write one: flow %q", rel)
	}
	return "scenario files that name it: " + strings.Join(users, ", ")
}

// flowUsers is up to limit scenario files under root whose `flow "..."`
// header resolves to flowFile, in path order and relative to root. The walk
// skips what discover skips, directories whose name starts with a dot.
func flowUsers(flowFile, root string, limit int) []string {
	var users []string
	visited := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner of the tree only shortens the hint
		}
		if visited++; visited > maxVisited {
			return errWalkDone
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if !front.IsArtFile(p) {
			return nil
		}
		src, err := os.ReadFile(p) //nolint:gosec // a file under the working directory, read to name it
		if err != nil {
			return nil //nolint:nilerr // as above
		}
		header, ok := front.FlowHeader(p, string(src))
		if !ok || !front.HeaderNames(p, header, flowFile) {
			return nil
		}
		users = append(users, relSlash(root, p))
		if len(users) == limit {
			return errWalkDone
		}
		return nil
	})
	return users
}

// relSlash is path relative to root with forward slashes, or path itself when
// it cannot be made relative.
func relSlash(root, path string) string {
	absRoot, err1 := filepath.Abs(root)
	absPath, err2 := filepath.Abs(path)
	if err1 != nil || err2 != nil {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
