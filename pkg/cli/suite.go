package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strings"

	"artemis/pkg/dsl/front"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/steps/httpstep"
)

// suiteState is one suite.art in a run: its block, what its setup left, and
// whether the scenarios under it can run.
type suiteState struct {
	file   string
	parent *suiteState
	block  *lower.Scenario // the suite's one block, lowered; nil when it would not compile

	// values are what `suite` holds for the scenarios under it: the suites
	// above's, then this one's own vars and setup captures.
	values map[string]any
	// scope and jar are what the setup ended with, which its teardown resumes
	// from and the scenarios under it start a copy of.
	scope executor.Scope
	jar   *httpstep.Jar

	// failed is why no scenario under this suite runs -- its setup, or one
	// above it, did not pass -- and empty when they can.
	failed string
}

// view is a copy of the suite's values, so a scenario that captured over one
// cannot change what the next scenario reads.
func (s *suiteState) view() map[string]any {
	return maps.Clone(s.values)
}

// suiteFor is the innermost suite a scenario file is under, or nil.
func (rt *runtimeEnv) suiteFor(file string) *suiteState {
	if rt == nil {
		return nil
	}
	return rt.suiteOfFile[file]
}

// planSuites finds the suite.art files the run's files are under and compiles
// each once. A suite that would not compile leaves every scenario under it
// with nothing to run, so they are skipped naming it; its diagnostics go to
// diagOut once, and it is returned for the caller to record as the errored
// file it is.
//
// The compiled suites come back too, for the environment check: an env() a
// suite's setup reads is as much the run's need as a scenario's.
func (rt *runtimeEnv) planSuites(files []string, diagOut io.Writer) (loaded []compiled, broken []fileAttempt) {
	rt.suites = map[string]*suiteState{}
	rt.suiteOfFile = map[string]*suiteState{}
	for _, file := range files {
		var inner *suiteState
		for _, path := range front.SuiteChain(file) {
			if _, err := os.Stat(path); err != nil {
				continue
			}
			st, seen := rt.suites[path]
			if !seen {
				st = &suiteState{file: path, parent: inner}
				rt.suites[path] = st
				diag := &bytes.Buffer{}
				c, err := rt.load(path, diag)
				diagOut.Write(diag.Bytes()) //nolint:errcheck // a console that cannot be written to has bigger problems
				switch {
				case err != nil:
					st.failed = fmt.Sprintf("suite %s does not compile", path)
					broken = append(broken, fileAttempt{compiled: compiled{file: path}, err: err, diag: &bytes.Buffer{}})
				case len(c.scenarios) != 1:
					st.failed = fmt.Sprintf("suite %s holds no suite block", path)
				default:
					st.block = c.scenarios[0]
					c.file = path
					loaded = append(loaded, c)
				}
			}
			inner = st
		}
		if inner != nil {
			rt.suiteOfFile[file] = inner
		}
	}
	return loaded, broken
}

// ordered is the run's suites outermost first: the order their setups run in,
// and the reverse of their teardowns'.
func (rt *runtimeEnv) ordered() []*suiteState {
	out := make([]*suiteState, 0, len(rt.suites))
	for _, st := range rt.suites {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := strings.Count(out[i].file, string(os.PathSeparator)), strings.Count(out[j].file, string(os.PathSeparator))
		if di != dj {
			return di < dj
		}
		return out[i].file < out[j].file
	})
	return out
}

// setUpSuites runs every suite's setup once, outermost first, before any
// scenario: an inner suite starts from its parent's values and cookies, as a
// scenario does. A suite whose parent failed is not set up, and fails with it.
func (rt *runtimeEnv) setUpSuites(ctx context.Context, run *result.RunResult, rep *report.Console) {
	for _, st := range rt.ordered() {
		if st.failed != "" {
			continue
		}
		if st.parent != nil && st.parent.failed != "" {
			st.failed = st.parent.failed
			continue
		}
		sc := &lower.Scenario{
			Name:    "suite setup",
			Line:    st.block.Line,
			Vars:    st.block.Vars,
			Browser: st.block.Browser,
			HTTP:    st.block.HTTP,
			Setup:   st.block.Setup,
		}
		in := scenarioIn{kind: "suite"}
		values := map[string]any{}
		if st.parent != nil {
			in.preset = map[string]any{"suite": st.parent.view()}
			in.jar, in.cloneJar = st.parent.jar, true
			values = st.parent.view()
		}
		out := execute(ctx, rt, sc, st.file, run, rep, in)
		st.scope, st.jar = out.scope, out.jar
		if !out.passed {
			st.failed = fmt.Sprintf("suite setup %s did not pass", st.file)
		}
		if out.scope != nil {
			for _, name := range exported(st.block) {
				if v, ok := out.scope.Get(name); ok {
					values[name] = v
				}
			}
		}
		st.values = values
	}
}

// tearDownSuites runs every suite's teardown after every scenario, innermost
// first, resuming the scope and cookies its setup ended with. A suite whose
// setup never ran -- a parent's failed -- has nothing to tear down.
func (rt *runtimeEnv) tearDownSuites(ctx context.Context, run *result.RunResult, rep *report.Console) {
	order := rt.ordered()
	for i := len(order) - 1; i >= 0; i-- {
		st := order[i]
		if st.block == nil || st.scope == nil || len(st.block.Teardown) == 0 {
			continue
		}
		sc := &lower.Scenario{
			Name:     "suite teardown",
			Line:     st.block.Line,
			Browser:  st.block.Browser,
			HTTP:     st.block.HTTP,
			Teardown: st.block.Teardown,
		}
		execute(ctx, rt, sc, st.file, run, rep, scenarioIn{kind: "suite", bound: st.scope, jar: st.jar})
	}
}

// exported is the names a suite gives the scenarios under it: its vars and its
// setup's captures -- what pkg/dsl/front's prelude told the checker they may
// read.
func exported(block *lower.Scenario) []string {
	var out []string
	for _, v := range block.Vars {
		out = append(out, v.Name)
	}
	for _, st := range block.Setup {
		for _, c := range st.Captures {
			out = append(out, c.Name)
		}
	}
	return out
}
