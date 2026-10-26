package front

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/parser"
)

// SuiteFile is the name of a folder's suite file: the setup, teardown and vars
// every scenario in the folder and below it shares.
const SuiteFile = "suite.art"

// IsSuiteFile reports whether path is a suite file, by its name.
func IsSuiteFile(path string) bool { return filepath.Base(path) == SuiteFile }

// SuiteChain is the suite files that may apply to file, outermost first: a
// suite.art in each directory from the working directory down to file's own --
// or, for a suite file, down to its parent's. Which of them exist is the
// loader's to say; this is only where to look.
//
// A relative path is walked up to the directory it is relative to, which is the
// working directory. An absolute one is walked up to the working directory when
// it is under it -- a suite is the project's, and the project is where artemis
// was run -- and to the root of the filesystem when it is not, so a suite named
// by an absolute path from anywhere still applies to the folders under it.
func SuiteChain(file string) []string {
	dir := filepath.Dir(file)
	if IsSuiteFile(file) {
		if dir == "." || dir == string(filepath.Separator) {
			return nil
		}
		dir = filepath.Dir(dir)
	}
	stop := "."
	if filepath.IsAbs(dir) {
		stop = string(filepath.Separator)
		if wd, err := os.Getwd(); err == nil && within(dir, wd) {
			stop = wd
		}
	}
	var chain []string
	for {
		chain = append(chain, filepath.Join(dir, SuiteFile))
		if dir == stop || dir == "." || dir == string(filepath.Separator) {
			break
		}
		dir = filepath.Dir(dir)
	}
	// Collected innermost first; a suite runs outermost first.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

func within(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// exported is what one suite gives the scenarios under it: the names of its
// vars and its setup's captures, and which of them are secret.
type exported struct {
	file    string
	names   []string
	secrets map[string]bool
	tokens  map[string]ast.Node // where each name is declared, for a clash
}

// exports reads a suite file's names off its parse tree. It does not check the
// file: a suite with a mistake in it still gives its scenarios the names it
// declares, so they are not reported for the suite's fault -- which the suite
// file's own compile reports, once.
func exports(file, src string) exported {
	tree, _ := parser.Parse(file, src)
	for _, d := range tree.Scenarios {
		if s, ok := d.(*ast.Scenario); ok && s.Keyword.Value == "suite" {
			out := exportsOf(s)
			out.file = file
			return out
		}
	}
	return exported{file: file}
}

// prelude is what the suites above file give it, through l: every name each
// exports, merged outermost first. A suite file is given its parents', not its
// own. With no loader -- a compile with no disk -- there is none.
func prelude(file string, l expand.Loader) (check.Prelude, []exported) {
	if l == nil {
		return check.Prelude{}, nil
	}
	var suites []exported
	for _, path := range SuiteChain(file) {
		_, src, err := l.Load(path, filepath.Base(path))
		if err != nil {
			continue
		}
		suites = append(suites, exports(path, src))
	}
	if len(suites) == 0 {
		return check.Prelude{}, nil
	}
	seen := map[string]bool{}
	p := check.Prelude{Suite: []string{}}
	for _, s := range suites {
		for _, n := range s.names {
			if seen[n] {
				continue
			}
			seen[n] = true
			p.Suite = append(p.Suite, n)
			p.SuiteSecret = p.SuiteSecret || s.secrets[n]
		}
	}
	sort.Strings(p.Suite)
	return p, suites
}

// checkSuiteFile holds a file to the rules a suite is under: a suite block is in
// a file named suite.art, a suite.art holds the suite and no scenario, the suite
// holds vars, config, setup and teardown and not a step of its own, and it
// exports no name a suite above it already does.
func checkSuiteFile(file string, tree *ast.File, outer []exported, bag *diag.Bag) {
	var suites, scenarios []*ast.Scenario
	for _, d := range tree.Scenarios {
		if s, ok := d.(*ast.Scenario); ok {
			if s.Keyword.Value == "suite" {
				suites = append(suites, s)
			} else {
				scenarios = append(scenarios, s)
			}
		}
	}
	isSuite := IsSuiteFile(file)
	for _, s := range suites {
		if !isSuite {
			bag.Error(s.Keyword.Span, diag.SuiteFileName,
				"a suite block belongs in a file named %s, which applies it to its folder", SuiteFile).
				Hintf("move it to %s", filepath.Join(filepath.Dir(file), SuiteFile))
		}
		for _, b := range s.Body {
			switch b := b.(type) {
			case *ast.StepDecl:
				bag.Error(b.Keyword.Span, diag.SuiteHasStep,
					"a suite runs setup and teardown, not steps of its own").
					Hintf("put the step in setup { ... } or teardown { ... }")
			case *ast.UseDecl:
				bag.Error(b.Keyword.Span, diag.SuiteHasStep,
					"a suite runs setup and teardown, not steps of its own").
					Hintf("put the use in setup { ... } or teardown { ... }")
			}
		}
	}
	if len(suites) > 1 {
		bag.Error(suites[1].Keyword.Span, diag.SuiteHasScenario, "a suite file holds one suite block")
	}
	if isSuite {
		if len(suites) == 0 && len(scenarios) > 0 {
			bag.Error(scenarios[0].Keyword.Span, diag.SuiteHasScenario,
				"%s holds a folder's suite block, and its scenarios would never run", SuiteFile).
				Hintf("move the scenario to a file of its own")
		}
		for _, s := range scenarios {
			if len(suites) > 0 {
				bag.Error(s.Keyword.Span, diag.SuiteHasScenario,
					"%s holds the folder's suite, not a scenario", SuiteFile).
					Hintf("move the scenario to a file of its own")
			}
		}
	}
	if !isSuite || len(suites) == 0 {
		return
	}
	// A name this suite exports that one above it already does would give
	// the scenarios below two values under one name.
	taken := map[string]string{}
	for _, s := range outer {
		for _, n := range s.names {
			if _, ok := taken[n]; !ok {
				taken[n] = s.file
			}
		}
	}
	mine := exportsOf(suites[0])
	for _, name := range mine.names {
		if from, clash := taken[name]; clash {
			bag.Error(mine.tokens[name].Span(), diag.DuplicateSuiteName,
				"%q is already a name %s gives every scenario below it", name, from).
				Hintf("give this one another name: every scenario reads both as suite.<name>")
		}
	}
}

// exportsOf is the names a suite block exports: its vars and its setup's
// captures, in the order written, with which are secret and where each is.
func exportsOf(s *ast.Scenario) exported {
	out := exported{secrets: map[string]bool{}, tokens: map[string]ast.Node{}}
	add := func(name string, secret bool, at ast.Node) {
		if _, dup := out.tokens[name]; dup {
			return
		}
		out.names = append(out.names, name)
		out.tokens[name] = at
		if secret {
			out.secrets[name] = true
		}
	}
	for _, b := range s.Body {
		switch b := b.(type) {
		case *ast.VarDecl:
			add(b.Name.Value, b.Secret.Text != "", b)
		case *ast.PhaseDecl:
			if b.Keyword.Value != ast.PhaseSetup {
				continue
			}
			// Every capture of setup, a block's included, in the order
			// written.
			ast.Inspect(b, func(n ast.Node) {
				if c, ok := n.(*ast.Capture); ok {
					add(c.Name.Value, c.Secret.Text != "", c)
				}
			})
		}
	}
	return out
}
