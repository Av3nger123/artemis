package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/result"
	"artemis/pkg/steps/browserstep"
)

// These tests drive the settle loop on a virtual clock against a fake page, so
// they take microseconds and need no browser.
//
// The fake satisfies pkg/steps/browserstep's own driver interface structurally
// -- it is unexported there, and Go interfaces do not care which package a
// value comes from -- so browserstep.Open hands back real Bindings over it.
// That is what lets this file test the loop against the same type the runner
// uses in production rather than against a stand-in for it.

// clock is a virtual now/sleep pair. Both package variables are swapped for the
// duration of a test: faking only one of them either waits out a real budget or
// spins against a clock that never moves.
type clock struct {
	mu sync.Mutex
	t  time.Time

	// slept is every duration asked for, which is what makes "how many times
	// did it try" and "did it overshoot the deadline" assertable.
	slept []time.Duration
}

func fakeClock(t *testing.T) *clock {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}
	oldNow, oldSleep := now, sleep
	now = c.now
	sleep = c.sleep
	t.Cleanup(func() { now, sleep = oldNow, oldSleep })
	return c
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// sleep advances the clock instead of waiting, which is what makes a 5s budget
// cost nothing to exhaust.
func (c *clock) sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	c.t = c.t.Add(d)
}

func (c *clock) tries() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.slept)
}

func (c *clock) total() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sum time.Duration
	for _, d := range c.slept {
		sum += d
	}
	return sum
}

// settlingPage is a page whose one element changes after n reads. It is the
// browserstep driver interface, satisfied structurally.
type settlingPage struct {
	mu sync.Mutex

	url, title string

	// text is what text(sel) answers, changing to after once reads passes at.
	text, after string
	at          int
	reads       int

	// urlAfter, when set, is what page.url becomes once reads passes at, so a
	// root -- not just an element -- can be the thing that settles.
	urlAfter string

	// rootErr makes URL fail, which is a page that has gone away mid-wait.
	rootErr error

	shots []string
}

func (p *settlingPage) read() {
	p.mu.Lock()
	p.reads++
	p.mu.Unlock()
}

func (p *settlingPage) settled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.at > 0 && p.reads > p.at
}

func (p *settlingPage) Goto(string) error           { return nil }
func (p *settlingPage) Click(string) error          { return nil }
func (p *settlingPage) Hover(string) error          { return nil }
func (p *settlingPage) Fill(string, string) error   { return nil }
func (p *settlingPage) Select(string, string) error { return nil }
func (p *settlingPage) Upload(string, string) error { return nil }
func (p *settlingPage) Press(string) error          { return nil }
func (p *settlingPage) Wait(time.Duration)          {}
func (p *settlingPage) SetTimeout(time.Duration)    {}

func (p *settlingPage) URL() (string, error) {
	if p.rootErr != nil {
		return "", p.rootErr
	}
	p.read()
	if p.settled() && p.urlAfter != "" {
		return p.urlAfter, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.url, nil
}

func (p *settlingPage) Title() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.title, nil
}

func (p *settlingPage) Text(string) (any, error) {
	p.read()
	if p.settled() {
		return p.after, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.text, nil
}

func (p *settlingPage) Value(string) (any, error)        { return nil, nil }
func (p *settlingPage) Attr(string, string) (any, error) { return nil, nil }
func (p *settlingPage) Count(string) (any, error)        { return float64(0), nil }

func (p *settlingPage) Visible(string) (any, error) {
	p.read()
	return p.settled(), nil
}

func (p *settlingPage) Screenshot(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shots = append(p.shots, path)
	return nil
}

// fakeSessions is the registry a scenario would have, holding the fake page.
type fakeSessions struct{ page any }

func (s fakeSessions) Session(context.Context) (any, error) { return s.page, nil }

// pageOn returns a context carrying page as the scenario's session, and the
// Bindings the runner would build from it.
func pageOn(t *testing.T, page any) (context.Context, *browserstep.Bindings) {
	t.Helper()
	ctx := executor.WithSessions(context.Background(), fakeSessions{page: page})
	b, err := browserstep.Open(ctx)
	if err != nil {
		t.Fatalf("browserstep.Open() = %v; the fake does not satisfy the driver", err)
	}
	return ctx, b
}

// oneStep lowers a single-step scenario, which is how a test gets a real
// lower.Step -- with its expects, their budgets and the per-type default the
// lowerer computed -- rather than one assembled by hand.
func oneStep(t *testing.T, src string) *lower.Step {
	t.Helper()
	_, st := oneScenario(t, src)
	return st
}

// oneScenario is oneStep with the scenario kept, for the tests that need its
// vars bound -- a `within` budget that came out of a var is read from the same
// scope the runner would have put it in.
func oneScenario(t *testing.T, src string) (*lower.Scenario, *lower.Step) {
	t.Helper()
	tree, bag := parser.Parse("t.art", src)
	info, checked := check.Check(tree)
	bag.Merge(checked)
	if bag.HasErrors() {
		t.Fatalf("the source does not check: %s", bag.All()[0].Message)
	}
	scenarios, err := lower.File(tree, info)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 1 || len(scenarios[0].Steps) != 1 {
		t.Fatalf("want one scenario of one step, got %d scenarios", len(scenarios))
	}
	return scenarios[0], scenarios[0].Steps[0]
}

// envFor is the env a browser step's assertions are evaluated against: the
// page's roots, and its element functions.
func envFor(t *testing.T, b *browserstep.Bindings) *eval.Env {
	t.Helper()
	roots, err := b.Roots()
	if err != nil {
		t.Fatal(err)
	}
	return &eval.Env{Roots: roots, Elements: b.Elements()}
}

const browserSrc = `scenario "s" {
  step "check" {
    browser { goto "http://127.0.0.1:1/" }
    %s
  }
}`

// The headline: an assertion that is false now and true later passes, having
// waited. It is the one behaviour `within` exists for, and the one `retry`
// cannot provide -- a retry would have re-run the `goto`.
func TestAnAssertionThatSettlesPasses(t *testing.T) {
	c := fakeClock(t)
	page := &settlingPage{url: "http://127.0.0.1:1/", title: "Welcome",
		text: "Working", after: "Pro", at: 3}
	ctx, b := pageOn(t, page)

	st := oneStep(t, fmt.Sprintf(browserSrc, `expect text("[role=status]") contains "Pro" within "10s"`))
	got := settle(ctx, st, envFor(t, b), b)

	if len(got) != 1 {
		t.Fatalf("settle() made %d assertions, want 1 -- one expect is one assertion", len(got))
	}
	if !got[0].Passed() {
		t.Fatalf("the assertion did not settle: %s (actual %v)", got[0].Status, got[0].Actual)
	}
	// It waited, and it stopped waiting. Both halves matter: an assertion that
	// never waited would pass by luck on a fast machine, and one that burned
	// its whole budget would make a passing step as slow as a failing one.
	if c.tries() == 0 {
		t.Error("the assertion passed without ever re-asking, so nothing was proved")
	}
	if c.total() >= 10*time.Second {
		t.Errorf("the assertion spent %v of its 10s budget, want it to stop as soon as it held", c.total())
	}
}

// The budget is honoured when nothing settles: the assertion fails, having
// spent about what it was given and not more.
func TestAnAssertionThatNeverSettlesFailsOnItsBudget(t *testing.T) {
	c := fakeClock(t)
	page := &settlingPage{url: "http://127.0.0.1:1/", title: "Never", text: "Working"}
	ctx, b := pageOn(t, page)

	st := oneStep(t, fmt.Sprintf(browserSrc, `expect text("#s") contains "Pro" within "500ms"`))
	got := settle(ctx, st, envFor(t, b), b)

	if got[0].Passed() {
		t.Fatal("the assertion passed, want a failure: nothing on that page ever settles")
	}
	if c.total() != 500*time.Millisecond {
		t.Errorf("the assertion spent %v, want exactly its 500ms budget", c.total())
	}
	// Five 100ms polls, with the last one clamped to whatever was left. An
	// overshoot would report an assertion as having taken longer than it was
	// given, which is the sort of arithmetic that makes a timing test not add
	// up.
	if c.tries() != 5 {
		t.Errorf("it tried %d times over 500ms at a %v poll, want 5", c.tries(), settlePoll)
	}
	// And it reports what it actually read, not the absence of a reading.
	if got[0].Actual != "Working" {
		t.Errorf("actual = %#v, want the value it kept reading", got[0].Actual)
	}
}

// A browser assertion with no `within` of its own gets the 5s default, which is
// lower.BrowserWithin -- the lowerer's number, not a second copy of it here.
func TestTheBrowserDefaultAppliesWithNoWithinWritten(t *testing.T) {
	c := fakeClock(t)
	page := &settlingPage{url: "http://127.0.0.1:1/", title: "Never", text: "Working"}
	ctx, b := pageOn(t, page)

	st := oneStep(t, fmt.Sprintf(browserSrc, `expect text("#s") contains "Pro"`))
	if got := settle(ctx, st, envFor(t, b), b); got[0].Passed() {
		t.Fatal("the assertion passed, want a failure")
	}
	if c.total() != lower.BrowserWithin {
		t.Errorf("it spent %v, want the %v browser default", c.total(), lower.BrowserWithin)
	}
}

// An assertion that is already true costs nothing. The 5s default is a ceiling
// on a failure, not a delay on a pass -- without this, every browser step in a
// passing suite would take five seconds per assertion.
func TestAnAssertionThatIsAlreadyTrueDoesNotWait(t *testing.T) {
	c := fakeClock(t)
	page := &settlingPage{url: "http://127.0.0.1:1/done", title: "Welcome", text: "Pro"}
	ctx, b := pageOn(t, page)

	st := oneStep(t, fmt.Sprintf(browserSrc, `expect page.url contains "/done"`))
	if got := settle(ctx, st, envFor(t, b), b); !got[0].Passed() {
		t.Fatalf("the assertion failed: %v", got[0])
	}
	if c.tries() != 0 {
		t.Errorf("it slept %d times for an assertion that was already true", c.tries())
	}
}

// A root, not just an element function, is re-read while waiting. `page.url` is
// a value in the roots map -- a snapshot -- so without the refresh an
// `expect page.url ... within` could never come true, which is the whole reason
// Bindings.Roots exists beside Observe.
func TestARootIsReReadWhileWaiting(t *testing.T) {
	fakeClock(t)
	page := &settlingPage{
		url: "http://127.0.0.1:1/pending", urlAfter: "http://127.0.0.1:1/done",
		title: "Welcome", at: 2,
	}
	ctx, b := pageOn(t, page)

	st := oneStep(t, fmt.Sprintf(browserSrc, `expect page.url contains "/done" within "5s"`))
	if got := settle(ctx, st, envFor(t, b), b); !got[0].Passed() {
		t.Fatalf("page.url never settled: actual %v -- the roots are not being re-read", got[0].Actual)
	}
}

// Each assertion gets its own budget, and all of them are evaluated: a step
// with a wrong title and a missing element should take one run to diagnose.
func TestEveryExpectIsEvaluatedAndEachHasItsOwnBudget(t *testing.T) {
	c := fakeClock(t)
	page := &settlingPage{url: "http://127.0.0.1:1/", title: "Welcome", text: "Working"}
	ctx, b := pageOn(t, page)

	st := oneStep(t, fmt.Sprintf(browserSrc, `expect page.title == "Welcome"
    expect text("#a") contains "Pro" within "300ms"
    expect text("#b") contains "Max" within "200ms"`))
	got := settle(ctx, st, envFor(t, b), b)

	if len(got) != 3 {
		t.Fatalf("settle() made %d assertions, want 3", len(got))
	}
	if !got[0].Passed() {
		t.Error("the first assertion failed, and it was true")
	}
	if got[1].Passed() || got[2].Passed() {
		t.Error("the two waiting assertions passed, and nothing settled")
	}
	// 300ms + 200ms: each assertion's budget is its own, which is what
	// "re-evaluates that single assertion" means.
	if want := 500 * time.Millisecond; c.total() != want {
		t.Errorf("the step spent %v, want %v -- one budget each", c.total(), want)
	}
}

// An api or terminal step has no page, so an explicit `within` is accepted and
// the assertion is evaluated once. Nothing it reads can change, so looping
// would make a failure exactly `within` slower and no more likely to pass --
// which is the reason those types have no default.
func TestAStepWithNoPageEvaluatesOnceEvenWithAnExplicitWithin(t *testing.T) {
	c := fakeClock(t)
	st := oneStep(t, `scenario "s" {
  step "check" {
    get "http://127.0.0.1:1/x"
    expect status == 200 within "10s"
  }
}`)
	env := &eval.Env{Roots: map[string]any{"status": float64(404)}}

	got := settle(context.Background(), st, env, nil)
	if got[0].Passed() {
		t.Fatal("the assertion passed, want a failure")
	}
	if c.tries() != 0 {
		t.Errorf("it slept %d times for a step with nothing to wait for", c.tries())
	}
}

// A budget that will not parse is an errored assertion naming the expect's
// line, not a silent one-shot. The checker caught every literal; this is the
// one that came out of a var.
func TestABudgetThatWillNotParseIsAnErroredAssertion(t *testing.T) {
	fakeClock(t)
	page := &settlingPage{url: "http://127.0.0.1:1/", title: "Welcome", text: "Working"}
	ctx, b := pageOn(t, page)

	sc, st := oneScenario(t, `scenario "s" {
  var budget = "soon"

  step "check" {
    browser { goto "http://127.0.0.1:1/" }
    expect page.title == "Welcome" within budget
  }
}`)
	// The scope the runner would have bound, because the budget is a var and a
	// `within` is evaluated against the same names an expect is.
	scope := executor.NewScope()
	if err := sc.Bind(scope); err != nil {
		t.Fatal(err)
	}
	env := envFor(t, b)
	env.Vars = scope.Vars()

	got := settle(ctx, st, env, b)

	if got[0].Status != result.StatusError {
		t.Fatalf("assertion = %v, want errored", got[0].Status)
	}
	if !strings.Contains(got[0].Error, "soon") {
		t.Errorf("error = %q, want it to quote the budget", got[0].Error)
	}
	if got[0].Line == 0 {
		t.Error("the errored assertion has no line, so a reader has nowhere to go")
	}
	if got[0].Kind != lower.KindExpect {
		t.Errorf("kind = %q, want %q", got[0].Kind, lower.KindExpect)
	}
}

// A cancelled run stops waiting. The assertion already made is reported: it is
// a true statement about the page at the moment the run was interrupted.
func TestACancelledRunStopsWaiting(t *testing.T) {
	c := fakeClock(t)
	page := &settlingPage{url: "http://127.0.0.1:1/", title: "Welcome", text: "Working"}
	ctx, b := pageOn(t, page)
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	st := oneStep(t, fmt.Sprintf(browserSrc, `expect text("#s") contains "Pro" within "10s"`))
	got := settle(ctx, st, envFor(t, b), b)

	if got[0].Passed() {
		t.Fatal("the assertion passed on a cancelled run")
	}
	if c.tries() != 0 {
		t.Errorf("it waited %d times on a cancelled run", c.tries())
	}
}

// A page that goes away mid-wait leaves the last assertion standing rather than
// replacing it with a complaint about the page, which would hide what was being
// waited for.
func TestAPageThatGoesAwayMidWaitKeepsTheLastAssertion(t *testing.T) {
	fakeClock(t)
	page := &settlingPage{url: "http://127.0.0.1:1/", title: "Welcome", text: "Working"}
	ctx, b := pageOn(t, page)
	env := envFor(t, b)
	// After the first read, the page is gone.
	page.rootErr = errors.New("the page has been closed")

	st := oneStep(t, fmt.Sprintf(browserSrc, `expect text("#s") contains "Pro" within "10s"`))
	got := settle(ctx, st, env, b)

	if got[0].Passed() {
		t.Fatal("the assertion passed")
	}
	if got[0].Status != result.StatusFail {
		t.Errorf("assertion = %v, want a plain failure about the element", got[0].Status)
	}
	if strings.Contains(fmt.Sprint(got[0].Actual), "closed") {
		t.Errorf("actual = %v, want the value that was read rather than the page's error", got[0].Actual)
	}
}

// A step with no expects makes no assertions and waits for nothing.
func TestAStepWithNoExpectsAssertsNothing(t *testing.T) {
	c := fakeClock(t)
	page := &settlingPage{url: "http://127.0.0.1:1/", title: "Welcome"}
	ctx, b := pageOn(t, page)

	st := oneStep(t, `scenario "s" {
  step "check" {
    browser { goto "http://127.0.0.1:1/" }
  }
}`)
	if got := settle(ctx, st, envFor(t, b), b); got != nil {
		t.Errorf("settle() = %v, want nil for a step with no expects", got)
	}
	if c.tries() != 0 {
		t.Errorf("it slept %d times with nothing to assert", c.tries())
	}
}

// pause never overshoots the deadline, which is what keeps a budget's
// arithmetic honest at the end of a wait.
func TestThePauseIsClampedToWhatIsLeft(t *testing.T) {
	c := fakeClock(t)
	for _, tc := range []struct {
		left, want time.Duration
	}{
		{time.Second, settlePoll},
		{settlePoll, settlePoll},
		{20 * time.Millisecond, 20 * time.Millisecond},
		{0, 0},
	} {
		if got := pause(c.now().Add(tc.left)); got != tc.want {
			t.Errorf("with %v left, pause() = %v, want %v", tc.left, got, tc.want)
		}
	}
}

// livePage is a browser step's page and nothing else's. An api step that
// somehow asked would get a nil, which is how the settle loop reads "there is
// nothing to wait for".
func TestLivePageIsForBrowserStepsOnly(t *testing.T) {
	page := &settlingPage{url: "http://127.0.0.1:1/"}
	ctx := executor.WithSessions(context.Background(), fakeSessions{page: page})

	if got := livePage(ctx, browserstep.StepType); got == nil {
		t.Error("livePage() = nil for a browser step with a session")
	}
	if got := livePage(ctx, "api"); got != nil {
		t.Error("livePage() returned a page for an api step")
	}
	if got := livePage(context.Background(), browserstep.StepType); got != nil {
		t.Error("livePage() returned a page for a run with no session")
	}
}
