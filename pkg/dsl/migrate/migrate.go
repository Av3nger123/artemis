// Package migrate rewrites .art files written in the old forms -- a scenario
// that holds its steps, capture, use, a collection's params, secret, suites
// and for rows -- as the collections, flows and scenario files that replace
// them.
//
// It works on the parsed tree, never on text: each old file is read with the
// parser, the new nodes are built from its nodes (cloned, so the comments and
// the spelling of every expression come along), and the result is printed
// with print.Canonical, the printer of `artemis fmt`.
package migrate

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/front"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"
)

// Files migrates in, a set of files by slash path relative to the input root,
// and returns the new files by the same kind of path, and a note for each
// thing the reader should know changed in meaning: each secret removed, and
// each suite whose setup now runs once per scenario.
//
// It is pure and reads no disk. in may hold files that are not .art -- the
// data a for clause reads its rows from -- and files above the root, by a
// path that starts "../": the suites above it, and the collections it
// imports. Neither is written back; out holds a file for each .art in in that
// is not a suite, and the flows and scenario files the old scenarios become.
//
// A file already in the new forms is printed as it is. A file that does not
// parse is an error, and nothing is migrated.
func Files(in map[string]string) (out map[string]string, notes []string, err error) {
	files, notes, err := build(in)
	if err != nil {
		return nil, nil, err
	}
	out = make(map[string]string, len(files))
	for k, f := range files {
		out[k] = print.Canonical(f)
	}
	return out, notes, nil
}

// migration is one run of Files.
type migration struct {
	in    map[string]string
	trees map[string]*ast.File // every .art in in, parsed, never changed
	keys  []string             // the .art paths in in, sorted
	out   map[string]*ast.File
	notes []string
	noted map[string]bool
}

// build is Files before printing: the new trees by path, which the tests read
// node by node.
func build(in map[string]string) (map[string]*ast.File, []string, error) {
	m := &migration{in: in, trees: map[string]*ast.File{}, out: map[string]*ast.File{}, noted: map[string]bool{}}
	for k := range in {
		if front.IsArtFile(k) {
			m.keys = append(m.keys, k)
		}
	}
	sort.Strings(m.keys)
	for _, k := range m.keys {
		tree, bag := parser.Parse(k, in[k])
		for _, d := range bag.All() {
			if d.Severity == diag.Error {
				return nil, nil, fmt.Errorf("%s:%d:%d: %s", k, d.Span.Line, d.Span.Col, d.Message)
			}
		}
		m.trees[k] = tree
	}
	for _, k := range m.keys {
		if strings.HasPrefix(k, "../") {
			continue // above the root: read for what it gives the files in it
		}
		if err := m.file(k, m.trees[k]); err != nil {
			return nil, nil, err
		}
	}
	return m.out, m.notes, nil
}

// file migrates one input file into m.out.
func (m *migration) file(key string, tree *ast.File) error {
	if suiteOf(tree) != nil {
		m.note("%s: its setup now runs once per scenario, not once per run", key)
		return nil
	}
	var scenarios []*ast.Scenario
	collection := false
	for _, d := range tree.Scenarios {
		switch v := d.(type) {
		case *ast.Flow, *ast.FlowRef:
			return m.put(key, tree) // already the new form
		case *ast.Collection:
			collection = true
		case *ast.Scenario:
			if v.Keyword.Value == "scenario" {
				scenarios = append(scenarios, v)
			}
		}
	}
	switch {
	case collection:
		return m.put(key, m.collectionFile(key, tree))
	case len(scenarios) == 0:
		return m.put(key, tree)
	}
	return m.scenarioFile(key, tree, scenarios)
}

// put adds f to the output at key. Two files meeting at one path is an
// error, because one would be lost: a scenario file b.art beside a folder
// flows/ that already holds a b.art, say.
func (m *migration) put(key string, f *ast.File) error {
	if _, dup := m.out[key]; dup {
		return fmt.Errorf("%s: two files would be written here; rename one of the files it comes from", key)
	}
	m.out[key] = f
	return nil
}

// note records a note once: suite material copied into several flows would
// otherwise say the same thing once per flow.
func (m *migration) note(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	if !m.noted[s] {
		m.noted[s] = true
		m.notes = append(m.notes, s)
	}
}

// at is "file:line" for a note about t.
func at(file string, t token.Token) string {
	return fmt.Sprintf("%s:%d", file, t.Span.Line)
}

// suiteOf is the suite block a file holds, or nil.
func suiteOf(tree *ast.File) *ast.Scenario {
	for _, d := range tree.Scenarios {
		if s, ok := d.(*ast.Scenario); ok && s.Keyword.Value == "suite" {
			return s
		}
	}
	return nil
}

// stem is a file's name without its extension.
func stem(key string) string {
	return strings.TrimSuffix(path.Base(key), path.Ext(key))
}
