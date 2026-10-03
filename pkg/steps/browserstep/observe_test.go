package browserstep

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"artemis/pkg/dsl/check"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// fakeSessions is a scenario's session registry holding a fake page. It is the
// executor.Sessions a runner would put on the context, which is how the whole
// executor -- Observe included -- is tested with no browser.
type fakeSessions struct {
	page *fakePage
	err  error
	asks int
}

func (s *fakeSessions) Session(context.Context) (any, error) {
	s.asks++
	if s.err != nil {
		return nil, s.err
	}
	return s.page, nil
}

// withPage is the context a browser step runs on.
func withPage(f *fakePage) (context.Context, *fakeSessions) {
	s := &fakeSessions{page: f}
	return executor.WithSessions(context.Background(), s), s
}

func browserStep(as ...models.Act) models.Step {
	return models.Step{Name: "upgrade", Type: StepType, Browser: models.Browser{Acts: as}}
}

// The roots this package binds are exactly the roots pkg/dsl/check admits in a
// browser step, and `page`'s members are exactly the closed set the checker
// holds. A root the checker admits and this file never binds is an `expect`
// that errors at run time for no reason a reader could find.
func TestTheRootsAreTheCheckersRoots(t *testing.T) {
	want := check.Roots(check.Browser)
	sort.Strings(want)
	got := []string{RootPage}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("browserstep binds %v, pkg/dsl/check admits %v", got, want)
	}

	members, closed := check.Members(RootPage)
	if !closed {
		t.Fatalf("pkg/dsl/check does not hold a closed member set for %q", RootPage)
	}
	sort.Strings(members)
	mine := []string{MemberTitle, MemberURL}
	if strings.Join(mine, ",") != strings.Join(members, ",") {
		t.Errorf("browserstep binds page.{%s}, pkg/dsl/check admits page.{%s}",
			strings.Join(mine, ","), strings.Join(members, ","))
	}
}

// Observe performs the acts and reports where the page ended up. The roots are
// nested under `page` because `page` is what an expression resolves and `url`
// is a member of it.
func TestObservePerformsTheActsAndBindsThePage(t *testing.T) {
	f := newFake().at("http://127.0.0.1:1/", "Sign in")
	ctx, _ := withPage(f)

	roots, err := Executor{}.Observe(ctx, browserStep(
		models.Act{Name: ActFill, Target: "#user", Value: "alice", Line: 4},
		models.Act{Name: ActClick, Target: "#go", Line: 5},
		models.Act{Name: ActGoto, Target: "http://127.0.0.1:1/done", Line: 6},
	), executor.NewScope())
	if err != nil {
		t.Fatalf("Observe() = %v, want nil", err)
	}

	page, ok := roots[RootPage].(map[string]any)
	if !ok {
		t.Fatalf("roots[%q] is %T, want an object", RootPage, roots[RootPage])
	}
	if page[MemberURL] != "http://127.0.0.1:1/done" {
		t.Errorf("page.url = %v, want the address the last goto reached", page[MemberURL])
	}
	if page[MemberTitle] != "Sign in" {
		t.Errorf("page.title = %v, want what the page reports", page[MemberTitle])
	}
	if len(roots) != 1 {
		t.Errorf("roots are %v, want page and nothing else", roots)
	}
	if got := f.log(); !strings.Contains(got, "fill #user = alice") {
		t.Errorf("the acts did not run:\n%s", got)
	}
}

// The roots are in the evaluator's domain, which is what makes `expect page.url
// contains "/done"` work: a string, resolved through a member lookup on a
// plain object.
func TestTheRootsAreReadableByTheEvaluator(t *testing.T) {
	f := newFake().at("http://127.0.0.1:1/done", "Welcome")
	b := &Bindings{driver: f}
	roots, err := b.Roots()
	if err != nil {
		t.Fatal(err)
	}

	env := &eval.Env{Roots: roots, Elements: b.Elements()}
	for _, tc := range []struct {
		src  string
		want any
	}{
		{"page.url", "http://127.0.0.1:1/done"},
		{"page.title", "Welcome"},
	} {
		got, err := evalIn(t, env, tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %#v, want %#v", tc.src, got, tc.want)
		}
	}
}

// The five element functions resolve through the same Env, which is the whole
// of `expect text("[role=status]") contains "Pro"`. The empty-match answers are
// SPEC.md's decision and are asserted here rather than left to the browser
// test, so a change to them fails in CI.
func TestTheElementFunctionsResolveAndAnswerForNoMatch(t *testing.T) {
	f := newFake().
		with("#who", element{text: "alice"}).
		with("#user", element{value: "alice"}).
		with("#home", element{attrs: map[string]string{"href": "/"}}).
		with(".invoice", element{count: 3}).
		with(".modal", element{hidden: true})
	b := &Bindings{driver: f}
	roots, err := b.Roots()
	if err != nil {
		t.Fatal(err)
	}
	env := &eval.Env{Roots: roots, Elements: b.Elements()}

	for _, tc := range []struct {
		src  string
		want any
	}{
		{`text("#who")`, "alice"},
		{`value("#user")`, "alice"},
		{`attr("#home", "href")`, "/"},
		{`count(".invoice")`, float64(3)},
		{`visible("#who")`, true},
		{`visible(".modal")`, false},

		// Nothing matches: 0, false, null, null, null.
		{`count(".nope")`, float64(0)},
		{`visible(".nope")`, false},
		{`text(".nope")`, nil},
		{`value(".nope")`, nil},
		{`attr(".nope", "href")`, nil},

		// A present element with no such attribute is null as well, because
		// `exists` is the operator for asking.
		{`attr("#who", "href")`, nil},
	} {
		got, err := evalIn(t, env, tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %#v, want %#v", tc.src, got, tc.want)
		}
	}
}

// A context with no session registry is a browser step outside a scenario. The
// error names the one thing that puts a registry there, because a reader who
// hits this is reading the runner rather than their scenario.
func TestABrowserStepWithNoSessionSaysSo(t *testing.T) {
	_, err := Executor{}.Observe(context.Background(), browserStep(
		models.Act{Name: ActClick, Target: "#go", Line: 4},
	), executor.NewScope())
	if err == nil {
		t.Fatal("Observe() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "browser session") {
		t.Errorf("Observe() = %v, want it to say the run has no browser session", err)
	}
}

// A session that will not open is the step's error, not a panic. pkg/session
// caches a failed open, so this is also what every later browser step of the
// scenario gets.
func TestASessionThatWillNotOpenFailsTheStep(t *testing.T) {
	boom := errors.New("launch chromium: no such file")
	ctx := executor.WithSessions(context.Background(), &fakeSessions{err: boom})

	_, err := Executor{}.Observe(ctx, browserStep(
		models.Act{Name: ActClick, Target: "#go", Line: 4},
	), executor.NewScope())
	if !errors.Is(err, boom) {
		t.Errorf("Observe() = %v, want the open's error", err)
	}
}

// A session of a type that is not a page is an artemis bug and reads as one.
func TestASessionOfTheWrongTypeIsRefused(t *testing.T) {
	ctx := executor.WithSessions(context.Background(), &fakeSessions{})
	// fakeSessions with a nil page hands back a typed nil *fakePage, which is
	// a driver; so this asks bindingsOf directly for the case the runner could
	// only reach with a registry from somewhere else.
	_, err := bindingsOf("a string is not a page")
	if err == nil {
		t.Fatal("bindingsOf(string) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "not a browser page") {
		t.Errorf("bindingsOf(string) = %v, want it to say so", err)
	}
	_ = ctx
}

// The step's timeout bounds the attempt, and a timeout that will not parse is
// the scenario's mistake rather than the page's -- caught before anything is
// driven, so it costs no navigation to find out.
func TestABadTimeoutIsCaughtBeforeAnythingIsDriven(t *testing.T) {
	f := newFake()
	ctx, sessions := withPage(f)
	step := browserStep(models.Act{Name: ActClick, Target: "#go", Line: 4})
	step.Timeout = "soon"

	if _, err := (Executor{}).Observe(ctx, step, executor.NewScope()); err == nil {
		t.Fatal("Observe() = nil, want a timeout error")
	}
	if f.log() != "" {
		t.Errorf("a bad timeout still drove the page: %s", f.log())
	}
	if sessions.asks != 0 {
		t.Error("a bad timeout still opened a browser")
	}
}

// A step with no timeout of its own gets the package's default, which bounds
// the whole block rather than one act.
func TestTheDefaultTimeoutBoundsTheWholeBlock(t *testing.T) {
	f := newFake()
	ctx, _ := withPage(f)

	if _, err := (Executor{}).Observe(ctx, browserStep(
		models.Act{Name: ActClick, Target: "#go", Line: 4},
	), executor.NewScope()); err != nil {
		t.Fatal(err)
	}
	if len(f.timeouts) != 1 {
		t.Fatalf("SetTimeout called %d times, want once", len(f.timeouts))
	}
	// Within a second of the default: the deadline was taken a moment before.
	if d := f.timeouts[0]; d > DefaultTimeout || d < DefaultTimeout-time.Second {
		t.Errorf("the act was given %v, want about the %v default", d, DefaultTimeout)
	}
}

// A step that says its own timeout gets that instead.
func TestTheStepsOwnTimeoutWins(t *testing.T) {
	f := newFake()
	ctx, _ := withPage(f)
	step := browserStep(models.Act{Name: ActClick, Target: "#go", Line: 4})
	step.Timeout = "2s"

	if _, err := (Executor{}).Observe(ctx, step, executor.NewScope()); err != nil {
		t.Fatal(err)
	}
	if d := f.timeouts[0]; d > 2*time.Second {
		t.Errorf("the act was given %v, more than the step's 2s", d)
	}
}

// A cancelled run stops rather than driving a page nobody is waiting for.
// Playwright's calls take no context, so this is the one place a browser step
// can notice.
func TestACancelledRunDrivesNothing(t *testing.T) {
	f := newFake()
	ctx, _ := withPage(f)
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := (Executor{}).Observe(ctx, browserStep(
		models.Act{Name: ActClick, Target: "#go", Line: 4},
	), executor.NewScope()); !errors.Is(err, context.Canceled) {
		t.Errorf("Observe() = %v, want context.Canceled", err)
	}
	if f.log() != "" {
		t.Errorf("a cancelled run still drove the page: %s", f.log())
	}
}

// Two steps of one scenario get one page. The registry is what guarantees it --
// this asserts that the executor asks for the session rather than opening
// anything of its own, which is the half of the promise that lives here.
func TestEveryStepBorrowsTheSameSession(t *testing.T) {
	f := newFake().at("http://127.0.0.1:1/", "Sign in")
	ctx, sessions := withPage(f)

	for i := 0; i < 3; i++ {
		if _, err := (Executor{}).Observe(ctx, browserStep(
			models.Act{Name: ActClick, Target: "#go", Line: 4},
		), executor.NewScope()); err != nil {
			t.Fatal(err)
		}
	}
	if sessions.asks != 3 {
		t.Errorf("the registry was asked %d times, want once per step", sessions.asks)
	}
	// And the page carried what the earlier steps did to it: three clicks on
	// one page, not one click on three pages.
	if got := strings.Count(f.log(), "click #go"); got != 3 {
		t.Errorf("the page saw %d clicks, want 3:\n%s", got, f.log())
	}
}

// Observe reports an act that failed as a step that could not run, which is
// pkg/executor's contract: a wrong answer is an observation, and a page that
// would not do what it was told is not.
func TestAnActThatFailedIsAStepThatCouldNotRun(t *testing.T) {
	f := newFake().breaks("Click", errors.New("timeout exceeded"))
	ctx, _ := withPage(f)

	roots, err := Executor{}.Observe(ctx, browserStep(
		models.Act{Name: ActClick, Target: "#go", Line: 9},
	), executor.NewScope())
	if err == nil {
		t.Fatal("Observe() = nil, want the click's error")
	}
	if roots != nil {
		t.Errorf("Observe() returned roots %v beside an error", roots)
	}
	if !strings.Contains(err.Error(), "line 9") {
		t.Errorf("Observe() = %v, want it to name the line", err)
	}
}

// It is registered, and under the type pkg/dsl/lower stamps on a browser step.
// Without this the step type is an unknown one at run time and the error is
// about a registry rather than about anything a reader wrote.
func TestItIsRegisteredAsBrowser(t *testing.T) {
	if StepType != "browser" {
		t.Errorf("StepType = %q, want browser", StepType)
	}
	e, ok := executor.Default().Lookup(StepType)
	if !ok {
		t.Fatalf("nothing is registered for %q", StepType)
	}
	if _, isBrowser := e.(Executor); !isBrowser {
		t.Errorf("the executor for %q is a %T, want browserstep.Executor", StepType, e)
	}
}
