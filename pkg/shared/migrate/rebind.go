package migrate

import (
	"fmt"
	"strconv"
	"strings"

	"artemis/pkg/dsl/ast"
)

// rebinding is how a scenario that captures a name twice is written in a
// language that binds a name once.
//
// A YAML scenario may capture `token` at login and again at refresh, each
// later step reading the latest. A .art file may not: a capture that rebinds
// a var or an earlier capture is duplicate-binding. So the second capture is
// written token_2 (or the first free suffix), every read after it follows the
// new name, and the file's header says what was renamed -- which is the same
// scenario, and one artemis will run.
type rebinding struct {
	captures []map[string]string // per step: YAML capture name -> name written
	reads    []map[string]string // per step: name read -> name it is written under
	notes    []string
}

// rebind plans the renames for config, in step order.
func rebind(config Config) rebinding {
	rb := rebinding{
		captures: make([]map[string]string, len(config.Steps)),
		reads:    make([]map[string]string, len(config.Steps)),
	}
	// taken is every name the file writes, so a new name is never one the
	// scenario already uses -- a later step's own token_2 included.
	taken := map[string]bool{}
	bound := map[string]bool{}
	for _, v := range config.Variables {
		taken[v.Name], bound[v.Name] = true, true
	}
	for _, s := range config.Steps {
		for name := range s.Capture {
			taken[name] = true
		}
	}

	current := map[string]string{} // a YAML name -> the name its latest value is written under
	for i, s := range config.Steps {
		if len(current) > 0 {
			rb.reads[i] = make(map[string]string, len(current))
			for k, v := range current {
				rb.reads[i][k] = v
			}
		}
		for _, name := range s.CaptureKeys() {
			if !bound[name] {
				bound[name] = true
				continue
			}
			n := fresh(name, taken)
			taken[n], bound[n] = true, true
			current[name] = n
			if rb.captures[i] == nil {
				rb.captures[i] = map[string]string{}
			}
			rb.captures[i][name] = n
			rb.notes = append(rb.notes, fmt.Sprintf(
				"# The capture %q of step %d %q is written %s here, because a name is bound once and %s already is.",
				name, i+1, s.Name, n, name))
		}
	}
	return rb
}

// fresh is name_2, name_3, ... -- the first that taken does not hold.
func fresh(name string, taken map[string]bool) string {
	for i := 2; ; i++ {
		if n := name + "_" + strconv.Itoa(i); !taken[n] {
			return n
		}
	}
}

// note is the header lines naming every rename, "" when there is none.
func (rb rebinding) note() string { return strings.Join(rb.notes, "\n") }

// renameReads rewrites every read in the i-th step of tree's scenario to the
// name reads[i] gives it. A call's callee is a builtin, never a read.
func renameReads(tree *ast.File, reads []map[string]string) {
	for _, d := range tree.Scenarios {
		sc, ok := d.(*ast.Scenario)
		if !ok {
			continue
		}
		i := 0
		for _, b := range sc.Body {
			s, ok := b.(*ast.StepDecl)
			if !ok {
				continue
			}
			if i < len(reads) && len(reads[i]) > 0 {
				rename := reads[i]
				callee := map[*ast.Ident]bool{}
				ast.Inspect(s, func(n ast.Node) {
					switch v := n.(type) {
					case *ast.Call:
						callee[v.Callee] = true
					case *ast.Ident:
						if to, ok := rename[v.Tok.Value]; ok && !callee[v] {
							v.Tok.Text, v.Tok.Value = to, to
						}
					}
				})
			}
			i++
		}
	}
}
