package cli

import (
	"context"
	"time"

	"artemis/pkg/dsl/lower"
	"artemis/pkg/eval"
	"artemis/pkg/result"
	"artemis/pkg/steps/browserstep"
)

// This file is `within`: an assertion given time to come true.
//
// It is the third of artemis's three clocks and the one most easily confused
// with the first. They are different scopes of the same idea:
//
//	retry { times, delay }   re-runs the whole step, action included
//	timeout = "5s"           bounds one attempt of the step
//	expect ... within "10s"  re-evaluates one assertion
//
// `retry` is right for an API that is not ready yet: nothing has happened, so
// ask again. It is wrong for a browser, where the page is already loading and
// re-running the step would re-click the button -- the action was fine, and one
// condition has not settled. So waiting is per-assertion, and the two compose:
// the loop below is inside attemptArtStep, which is inside the retry loop, so a
// step may retry and each attempt's assertions may each wait.
//
// The budget and its per-type default are pkg/dsl/lower's -- Expect.Within,
// defaulting to lower.BrowserWithin in a browser step and to zero elsewhere --
// because only the lowerer knows the step's type. What is here is the loop that
// assert.go's comment said belongs with the step that needs one.

// settlePoll is how often a waiting assertion re-asks.
//
// Flat rather than backing off. Over a 5s budget it costs at most fifty extra
// DOM reads, each of them a single non-waiting selector query, and in exchange
// the timing of a `within` test is something a reader can predict: an assertion
// that settles at 1.2s is reported at about 1.2s, not at the next doubling
// after it. Playwright's own assertions back off, and matching them would make
// our numbers harder to explain for no gain at these budgets.
const settlePoll = 100 * time.Millisecond

// settle evaluates every expect in the step, in source order, giving each one
// its own `within` budget to come true in.
//
// Source order is the order they are printed in, which is the order they were
// written in. Nothing stops at the first failure: a step with a wrong title and
// a missing element should take one run to diagnose. Each assertion's budget is
// its own, so two waiting assertions in one step may each take the full
// budget -- which is what "re-evaluates that single assertion" means, and is
// why a step that wants one shared budget writes one `expect`.
//
// This replaces lower.Step.Assert on the run path. That method is the one-shot
// form and is still what pkg/dsl/lower's own tests use; the difference is
// exactly the loop, and it is here because only the runner has the page to
// re-read and the clock to do it against.
func settle(ctx context.Context, st *lower.Step, env *eval.Env, page *browserstep.Bindings) []result.AssertionResult {
	if len(st.Expects) == 0 {
		return nil
	}
	out := make([]result.AssertionResult, 0, len(st.Expects))
	for _, e := range st.Expects {
		out = append(out, settleOne(ctx, st.Name, e, env, page))
	}
	return out
}

// settleOne evaluates one expect, re-evaluating it until it holds or its budget
// expires.
//
// The clock starts before the first evaluation, so the budget is what the
// author asked for and not that plus however long one read of the page takes. A
// zero budget then needs no special case: the deadline is already now, the loop
// does not run, and the assertion was evaluated exactly once.
//
// An assertion that passes stops the loop at once, which is the behaviour the
// default exists for: a browser assertion that is already true costs nothing,
// and only one that is not yet true spends any of its 5s.
func settleOne(ctx context.Context, step string, e *lower.Expect, env *eval.Env, page *browserstep.Bindings) result.AssertionResult {
	budget, err := e.Within(env)
	if err != nil {
		// `within "soon"` out of an expression. The checker caught every
		// literal; this is the one that came from a var, and it is the
		// scenario's mistake rather than the page's -- so it is an errored
		// assertion naming the expect's line, not a silent one-shot.
		return result.Assertion{Step: step, Kind: lower.KindExpect, Line: e.Line, File: e.File}.Errored(err)
	}

	deadline := now().Add(budget)
	a := e.Assert(step, env)

	// Nothing to wait for. An api or terminal observation is complete when the
	// step returned and cannot change, so re-asking would make a failure
	// exactly `within` slower and no more likely to pass -- which is the
	// reason those types have no default. An explicit budget on one of them is
	// accepted, and evaluated once.
	if page == nil {
		return a
	}

	for !a.Passed() && now().Before(deadline) {
		// A cancelled run stops waiting. The assertion already made is what is
		// reported: it is a true statement about the page at the moment the run
		// was interrupted.
		if ctx.Err() != nil {
			break
		}
		sleep(pause(deadline))
		next, err := refreshed(env, page)
		if err != nil {
			// The page has gone -- closed, crashed -- so there is nothing left
			// to re-read. The last assertion stands rather than being replaced
			// by a complaint about the page, which would hide what was being
			// waited for.
			break
		}
		env = next
		a = e.Assert(step, env)
	}
	return a
}

// pause is how long to sleep before the next try: the poll interval, or
// whatever is left of the budget when that is less.
//
// Clamping matters at the end of a budget. Without it an assertion with 20ms
// left would sleep 100ms and be reported as having taken 80ms longer than it
// was given, which is the sort of overshoot that makes a `within` test's
// timings not quite add up.
func pause(deadline time.Time) time.Duration {
	if left := deadline.Sub(now()); left < settlePoll {
		return left
	}
	return settlePoll
}

// refreshed is env with the page's roots read again.
//
// A copy rather than a write into env, because eval.Env documents itself as
// read-only -- evaluating the same expression twice against the same Env gives
// the same answer -- and the waiting loop is the caller that documentation was
// written for. The element functions are not refreshed and need no refreshing:
// each call already reads the DOM as it is now, which is what makes
// `expect visible(".modal") within "5s"` settle at all.
func refreshed(env *eval.Env, page *browserstep.Bindings) (*eval.Env, error) {
	roots, err := page.Roots()
	if err != nil {
		return nil, err
	}
	next := *env
	next.Roots = roots
	return &next, nil
}
