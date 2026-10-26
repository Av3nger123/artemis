package cli

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/shared/logger"
)

// flowRun runs a scenario's steps and the blocks that hold them, and records
// each step where the report expects it.
//
// A step's result is recorded into scenario and printed to rep. Most of a
// scenario does both live, a step at a time. A parallel branch and a repeat's
// round run into a flowRun of their own -- a scratch result and a buffered
// console -- whose steps are handed to the parent once the branch or the loop
// is done, so the report and the console stay in source order however the
// branches interleaved.
type flowRun struct {
	ctx      context.Context
	rt       *runtimeEnv
	sc       *lower.Scenario
	file     string
	scenario *result.ScenarioResult
	rep      *report.Console
	scope    executor.Scope
	gen      *eval.Gen

	// live is whether a step's events go out as it runs. A buffered run's go
	// out when the parent takes its steps.
	live bool
	// index is the next --events step index, shared by every run of the
	// scenario so the indexes are the order the steps were reported in.
	index *int

	// round is the repeat round being run, 0 outside one.
	round int
}

// child is a run whose steps are held until the parent takes them: a
// parallel branch's, over its own copy of the scope, or a repeat round's,
// over the parent's.
func (f *flowRun) child(scope executor.Scope, gen *eval.Gen) (*flowRun, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return &flowRun{
		ctx: f.ctx, rt: f.rt, sc: f.sc, file: f.file,
		scenario: &result.ScenarioResult{Name: f.scenario.Name, File: f.scenario.File},
		rep:      report.NewConsole(buf),
		scope:    scope, gen: gen, index: f.index, round: f.round,
	}, buf
}

// adopt takes a held run's steps into this one, in order, with their console
// lines and their events.
func (f *flowRun) adopt(c *flowRun, out *bytes.Buffer) {
	f.rep.Write(out.Bytes()) //nolint:errcheck // a console that will not take bytes is already reporting nothing
	for _, r := range c.scenario.Steps {
		f.scenario.Steps = append(f.scenario.Steps, r)
		// A run that is itself held sends nothing yet: its steps' events go
		// out when its own parent takes them.
		if f.live {
			f.rt.events.StepStart(f.scenario, *f.index, r.Name)
			f.rt.events.StepEnd(f.scenario, *f.index, r)
			*f.index++
		}
	}
}

// step runs one step -- or records why it did not run -- and reports it.
// cleanup is teardown's and a finally's rule: a step that reads a capture no
// step made has nothing to clean up, and is skipped.
func (f *flowRun) step(phase string, st *lower.Step, skip string, cleanup bool) *result.StepResult {
	if f.live {
		f.rt.events.StepStart(f.scenario, *f.index, st.Name)
	}
	r := f.scenario.NewStep(st.Name)
	// The line the step was written on, so a step that could not run at all
	// -- and an assertion with no line of its own -- still points somewhere a
	// reader can go (ART-12). And the file that line is in, when a use brought
	// the step in from a collection.
	r.Line, r.File, r.Phase, r.Round = st.Line, st.File, phase, f.round
	switch missing := missingNeeds(st, f.scope); {
	case skip != "":
		r.Skip(skip)
	case cleanup && len(missing) > 0:
		r.Skip(fmt.Sprintf("needs %s, which no step before it captured", strings.Join(missing, ", ")))
	default:
		runArtStep(f.ctx, f.rt, f.sc.Name, st, f.scope, f.gen, r)
	}
	logger.Logger.Info(fmt.Sprintf("Step completed: %s, Duration: %v", st.Name, r.Duration))
	// Every step that was reached gets a line, including one artemis could not
	// execute and one it skipped.
	f.rep.Step(r)
	if f.live {
		f.rt.events.StepEnd(f.scenario, *f.index, r)
		*f.index++
	}
	return r
}

// errored records a step that stands for a block whose own expression could
// not be evaluated -- an if's condition, a repeat's bound -- or whose loop ran
// out, so the fault is in the report where the block is.
func (f *flowRun) errored(phase, name string, line int, err error) {
	if f.live {
		f.rt.events.StepStart(f.scenario, *f.index, name)
	}
	r := f.scenario.NewStep(name)
	r.Line, r.Phase, r.Round = line, phase, f.round
	r.Fail(0, err)
	f.rep.Step(r)
	if f.live {
		f.rt.events.StepEnd(f.scenario, *f.index, r)
		*f.index++
	}
}

// outcome is how a run of nodes ended: whether every step passed, and if not
// the first that did not, for the reason a skip names.
type outcome struct {
	ok     bool
	failed string
}

func (o *outcome) add(other outcome) {
	if !other.ok && o.ok {
		o.ok, o.failed = false, other.failed
	}
}

// nodes runs a sequence. stop is the reason the steps after a failure are
// skipped with -- "" for a scenario's own steps, where a failure does not
// stop the next, as it never has.
func (f *flowRun) nodes(nodes []lower.Node, phase, skip string, cleanup bool, stop func(failed string) string) outcome {
	out := outcome{ok: true}
	for _, n := range nodes {
		var got outcome
		switch n := n.(type) {
		case *lower.Step:
			r := f.step(phase, n, skip, cleanup)
			got = outcome{ok: r.Status != result.StatusFail && r.Status != result.StatusError, failed: n.Name}
		case *lower.Flow:
			got = f.flow(n, phase, skip, cleanup)
		}
		if got.ok {
			continue
		}
		out.add(got)
		if stop != nil && skip == "" {
			skip = stop(got.failed)
		}
	}
	return out
}

// skipAll records every step of nodes as skipped, with reason.
func (f *flowRun) skipAll(nodes []lower.Node, phase, reason string) {
	for _, st := range lower.Steps(nodes) {
		f.step(phase, st, reason, false)
	}
}

// flow runs one block.
func (f *flowRun) flow(fl *lower.Flow, phase, skip string, cleanup bool) outcome {
	if skip != "" {
		for _, b := range fl.Branches {
			f.skipAll(b.Body, phase, skip)
		}
		return outcome{ok: true}
	}
	switch fl.Kind {
	case ast.FlowParallel:
		return f.parallel(fl, phase, cleanup)
	case ast.FlowIf, ast.FlowSwitch:
		return f.choose(fl, phase, cleanup)
	case ast.FlowTry:
		return f.try(fl, phase, cleanup)
	case ast.FlowRepeat:
		return f.repeat(fl, phase, cleanup)
	}
	return outcome{ok: true}
}

// parallel runs every branch at once, each over its own copy of the scope and
// its own random generator, and waits for all of them. The checker has made
// sure no two branches capture one name and no branch reads a sibling's
// capture, so the copies merge back without a conflict.
func (f *flowRun) parallel(fl *lower.Flow, phase string, cleanup bool) outcome {
	type branchRun struct {
		run *flowRun
		buf *bytes.Buffer
		got outcome
	}
	runs := make([]*branchRun, len(fl.Branches))
	var wg sync.WaitGroup
	for i, b := range fl.Branches {
		gen := eval.NewGen(f.rt.seed, f.rt.seedKey(f.file), fmt.Sprintf("%s\x00parallel %d branch %d", f.sc.Name, fl.Line, i))
		c, buf := f.child(maps.Clone(f.scope), gen)
		runs[i] = &branchRun{run: c, buf: buf}
		wg.Add(1)
		go func(br *branchRun, body []lower.Node) {
			defer wg.Done()
			br.got = br.run.nodes(body, phase, "", cleanup, nil)
		}(runs[i], b.Body)
	}
	wg.Wait()

	out := outcome{ok: true}
	for _, br := range runs {
		f.adopt(br.run, br.buf)
		for k, v := range br.run.scope {
			f.scope.Set(k, v)
		}
		if br.run.gen.Used() {
			f.rt.drew.Store(true)
		}
		out.add(br.got)
	}
	return out
}

// choose runs the one branch of an if or a switch that applies and records
// the others as skipped, naming the branch that ran.
func (f *flowRun) choose(fl *lower.Flow, phase string, cleanup bool) outcome {
	env := &eval.Env{Vars: f.scope.Vars(), Gen: f.gen}
	what := "if"
	var subject any
	if fl.Kind == ast.FlowSwitch {
		what = "switch"
		v, err := eval.Eval(fl.Subject, env)
		if err != nil {
			return f.unchosen(fl, phase, what, err)
		}
		subject = v
	}

	taken := -1
	for i, b := range fl.Branches {
		if b.Cond == nil { // else, default
			taken = i
			break
		}
		v, err := eval.Eval(b.Cond, env)
		if err != nil {
			return f.unchosen(fl, phase, what, fmt.Errorf("%s %s: %w", b.Keyword, b.CondText, err))
		}
		if fl.Kind == ast.FlowSwitch {
			if eval.Equal(subject, v) {
				taken = i
				break
			}
			continue
		}
		held, isBool := v.(bool)
		if !isBool {
			return f.unchosen(fl, phase, what, fmt.Errorf("%s %s is %s, want true or false", b.Keyword, b.CondText, eval.Render(v)))
		}
		if held {
			taken = i
			break
		}
	}

	reason := fmt.Sprintf("not taken: no branch of the %s at line %d applied", what, fl.Line)
	if taken >= 0 {
		reason = fmt.Sprintf("not taken: the %s at line %d ran %s", what, fl.Line, describeBranch(fl.Branches[taken]))
	}
	out := outcome{ok: true}
	for i, b := range fl.Branches {
		if i == taken {
			out.add(f.nodes(b.Body, phase, "", cleanup, nil))
			continue
		}
		f.skipAll(b.Body, phase, reason)
	}
	return out
}

// unchosen records an if or a switch whose condition could not be evaluated:
// one errored entry where the block is, and every branch skipped.
func (f *flowRun) unchosen(fl *lower.Flow, phase, what string, err error) outcome {
	name := fmt.Sprintf("%s at line %d", what, fl.Line)
	f.errored(phase, name, fl.Line, err)
	for _, b := range fl.Branches {
		f.skipAll(b.Body, phase, fmt.Sprintf("the %s could not be evaluated", name))
	}
	return outcome{ok: false, failed: name}
}

// describeBranch is how a skip names the branch that ran instead.
func describeBranch(b *lower.Branch) string {
	if b.Cond == nil {
		return b.Keyword
	}
	return fmt.Sprintf("%s %s", b.Keyword, b.CondText)
}

// try runs the try's steps until one does not pass, then the catch when one
// did not, then the finally whatever happened. A catch is compensation, not a
// cure: the step that failed still fails the scenario.
func (f *flowRun) try(fl *lower.Flow, phase string, cleanup bool) outcome {
	out := outcome{ok: true}
	var catch, finally *lower.Branch
	for _, b := range fl.Branches[1:] {
		switch b.Keyword {
		case "catch":
			catch = b
		case "finally":
			finally = b
		}
	}
	tried := f.nodes(fl.Branches[0].Body, phase, "", cleanup, func(failed string) string {
		return fmt.Sprintf("%q did not pass, so the rest of the try block was skipped", failed)
	})
	out.add(tried)
	if catch != nil {
		if tried.ok {
			f.skipAll(catch.Body, phase, "not taken: the try block passed")
		} else {
			out.add(f.nodes(catch.Body, phase, "", cleanup, nil))
		}
	}
	if finally != nil {
		out.add(f.nodes(finally.Body, phase, "", true, nil))
	}
	return out
}

// repeat runs a repeat block's body a round at a time until its condition
// holds, a round fails, or a bound runs out. Only the last round's steps are
// reported, each saying which round it is, as retry reports only the last
// attempt: thirty identical polls would bury the one that matters.
func (f *flowRun) repeat(fl *lower.Flow, phase string, cleanup bool) outcome {
	name := fmt.Sprintf("repeat at line %d", fl.Line)
	env := &eval.Env{Vars: f.scope.Vars(), Gen: f.gen}
	every, err := optionDuration(fl.Every, env)
	if err == nil {
		var within time.Duration
		within, err = optionDuration(fl.Within, env)
		if err == nil {
			var max int
			max, err = optionCount(fl.Max, env)
			if err == nil {
				return f.rounds(fl, phase, cleanup, every, within, max)
			}
		}
	}
	f.errored(phase, name, fl.Line, err)
	f.skipAll(fl.Branches[0].Body, phase, fmt.Sprintf("the %s could not start", name))
	return outcome{ok: false, failed: name}
}

func (f *flowRun) rounds(fl *lower.Flow, phase string, cleanup bool, every, within time.Duration, max int) outcome {
	start := time.Now()
	body := fl.Branches[0].Body
	var last *flowRun
	var lastBuf *bytes.Buffer
	var got outcome
	for round := 1; ; round++ {
		f.round = round
		last, lastBuf = f.child(f.scope, f.gen)
		got = last.nodes(body, phase, "", cleanup, func(failed string) string {
			return fmt.Sprintf("%q did not pass, so the rest of round %d was skipped", failed, round)
		})
		f.round = 0
		if !got.ok {
			break // a round that failed ends the loop: its step says why
		}
		held, err := untilHolds(fl, f.scope, f.gen)
		if err != nil {
			f.adopt(last, lastBuf)
			f.errored(phase, "until "+fl.UntilText, fl.Line, err)
			return outcome{ok: false, failed: "until " + fl.UntilText}
		}
		if held {
			break
		}
		out := (max > 0 && round >= max) || (within > 0 && time.Since(start)+every > within)
		if out {
			f.adopt(last, lastBuf)
			f.errored(phase, "until "+fl.UntilText, fl.Line, exhausted(fl, f.scope, round, within, max))
			return outcome{ok: false, failed: "until " + fl.UntilText}
		}
		if every > 0 {
			select {
			case <-time.After(every):
			case <-f.ctx.Done():
				f.adopt(last, lastBuf)
				f.errored(phase, "until "+fl.UntilText, fl.Line, f.ctx.Err())
				return outcome{ok: false, failed: "until " + fl.UntilText}
			}
		}
	}
	f.adopt(last, lastBuf)
	return got
}

// untilHolds evaluates a repeat's condition against what the round captured.
func untilHolds(fl *lower.Flow, scope executor.Scope, gen *eval.Gen) (bool, error) {
	v, err := eval.Eval(fl.Until, &eval.Env{Vars: scope.Vars(), Gen: gen})
	if err != nil {
		return false, err
	}
	held, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("until %s is %s, want true or false", fl.UntilText, eval.Render(v))
	}
	return held, nil
}

// exhausted is the error for a repeat whose condition never held: the bound
// that ran out, and what the names the condition reads held last.
func exhausted(fl *lower.Flow, scope executor.Scope, rounds int, within time.Duration, max int) error {
	bound := fmt.Sprintf("%d rounds", rounds)
	if within > 0 {
		bound = fmt.Sprintf("%s (%d rounds)", within, rounds)
	}
	var last []string
	seen := map[string]bool{}
	ast.Inspect(fl.Until, func(n ast.Node) {
		id, ok := n.(*ast.Ident)
		if !ok || seen[id.Name()] {
			return
		}
		seen[id.Name()] = true
		if v, ok := scope.Get(id.Name()); ok {
			last = append(last, fmt.Sprintf("%s was %s", id.Name(), quoted(v)))
		}
	})
	sort.Strings(last)
	msg := fmt.Sprintf("until %s never held within %s", fl.UntilText, bound)
	if len(last) > 0 {
		msg += "; " + strings.Join(last, ", ")
	}
	return fmt.Errorf("%s", msg)
}

// quoted renders a value for a message, a string in quotes.
func quoted(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return eval.Render(v)
}

// optionDuration evaluates a repeat's every or within; 0 when it was not
// written.
func optionDuration(x ast.Expr, env *eval.Env) (time.Duration, error) {
	if x == nil {
		return 0, nil
	}
	v, err := eval.Eval(x, env)
	if err != nil {
		return 0, err
	}
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("%s is not a duration (want something like \"2s\")", eval.Render(v))
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%q is not a duration (want something like \"2s\")", s)
	}
	return d, nil
}

// optionCount evaluates a repeat's max; 0 when it was not written.
func optionCount(x ast.Expr, env *eval.Env) (int, error) {
	if x == nil {
		return 0, nil
	}
	v, err := eval.Eval(x, env)
	if err != nil {
		return 0, err
	}
	n, ok := v.(float64)
	if !ok || n < 1 || n != float64(int(n)) {
		return 0, fmt.Errorf("max is %s, want a whole number above 0", eval.Render(v))
	}
	return int(n), nil
}
