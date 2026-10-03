package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"artemis/pkg/executor"
)

// fakeOpener stands in for a browser. Every lifetime rule in this package is
// about *when* a session is opened and closed rather than what it can do, so
// all of them are testable without a browser -- which is what lets them run in
// CI, where there is none.
type fakeOpener struct {
	mu     sync.Mutex
	opens  int
	closes int
	err    error
}

func (f *fakeOpener) open(context.Context, Config) (*Session, error) {
	f.mu.Lock()
	f.opens++
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &Session{closeFn: func() error {
		f.mu.Lock()
		f.closes++
		f.mu.Unlock()
		return nil
	}}, nil
}

func (f *fakeOpener) counts() (opens, closes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens, f.closes
}

// registryWith is NewRegistry with the browser swapped out.
func registryWith(f *fakeOpener) *Registry {
	r := NewRegistry(Config{Headless: true})
	r.open = f.open
	return r
}

// The whole reason a scenario owns a session rather than a step: step 4 has to
// see what step 3 did to the DOM, which is only true if it is the same page.
func TestOneSessionPerScenario(t *testing.T) {
	f := &fakeOpener{}
	r := registryWith(f)

	first, err := r.Session(context.Background())
	if err != nil {
		t.Fatalf("first ask: %v", err)
	}
	second, err := r.Session(context.Background())
	if err != nil {
		t.Fatalf("second ask: %v", err)
	}
	if first != second {
		t.Fatal("two asks in one scenario got two different sessions")
	}
	if opens, _ := f.counts(); opens != 1 {
		t.Fatalf("opened %d browsers, want 1", opens)
	}
	if _, ok := first.(*Session); !ok {
		t.Fatalf("Session returned %T, want *session.Session", first)
	}
}

// Two scenarios must not share cookies, which here means they must not share a
// browser.
func TestTwoScenariosGetTwoSessions(t *testing.T) {
	f := &fakeOpener{}
	a, b := registryWith(f), registryWith(f)

	first, err := a.Session(context.Background())
	if err != nil {
		t.Fatalf("scenario a: %v", err)
	}
	second, err := b.Session(context.Background())
	if err != nil {
		t.Fatalf("scenario b: %v", err)
	}
	if first == second {
		t.Fatal("two scenarios got the same session")
	}
	if opens, _ := f.counts(); opens != 2 {
		t.Fatalf("opened %d browsers, want 2", opens)
	}
}

// The Done-When, without a browser: a step panics and the session is closed
// anyway, exactly once, before the panic reaches the runner.
func TestSessionClosedWhenStepPanics(t *testing.T) {
	f := &fakeOpener{}
	var closedAtPanicTime int

	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				t.Error("the panic did not reach the caller; WithScenario swallowed it")
			}
			if got, want := recovered, "step 3 blew up"; got != want {
				t.Errorf("recovered %v, want %v", got, want)
			}
			_, closedAtPanicTime = f.counts()
		}()

		_ = withScenarioOpener(context.Background(), Config{Headless: true}, f.open,
			func(ctx context.Context) error {
				sessions, ok := executor.SessionsOf(ctx)
				if !ok {
					t.Fatal("no registry on the scenario's context")
				}
				if _, err := sessions.Session(ctx); err != nil {
					t.Fatalf("opening a session: %v", err)
				}
				panic("step 3 blew up")
			})
	}()

	opens, closes := f.counts()
	if opens != 1 {
		t.Fatalf("opened %d browsers, want 1", opens)
	}
	if closes != 1 {
		t.Fatalf("closed %d browsers, want 1 -- a panicking step leaked one", closes)
	}
	if closedAtPanicTime != 1 {
		t.Fatalf("the session was closed %d times by the time the panic surfaced, want 1", closedAtPanicTime)
	}
}

// Pass or fail, same rule.
func TestSessionClosedWhenScenarioEnds(t *testing.T) {
	failed := errors.New("an assertion failed")
	for name, outcome := range map[string]error{"passes": nil, "fails": failed} {
		t.Run(name, func(t *testing.T) {
			f := &fakeOpener{}
			err := withScenarioOpener(context.Background(), Config{Headless: true}, f.open,
				func(ctx context.Context) error {
					sessions, _ := executor.SessionsOf(ctx)
					if _, err := sessions.Session(ctx); err != nil {
						t.Fatalf("opening a session: %v", err)
					}
					return outcome
				})
			if !errors.Is(err, outcome) {
				t.Fatalf("got %v, want %v", err, outcome)
			}
			if opens, closes := f.counts(); opens != 1 || closes != 1 {
				t.Fatalf("opened %d and closed %d, want 1 and 1", opens, closes)
			}
		})
	}
}

// A scenario with no browser step must not open one, and closing it must not
// be an error.
func TestScenarioThatNeverAsksOpensNothing(t *testing.T) {
	f := &fakeOpener{}
	err := withScenarioOpener(context.Background(), Config{Headless: true}, f.open,
		func(ctx context.Context) error {
			if _, ok := executor.SessionsOf(ctx); !ok {
				t.Fatal("no registry on the scenario's context")
			}
			return nil
		})
	if err != nil {
		t.Fatalf("WithScenario: %v", err)
	}
	if opens, closes := f.counts(); opens != 0 || closes != 0 {
		t.Fatalf("opened %d and closed %d, want 0 and 0", opens, closes)
	}
}

// A browser that will not launch takes tens of seconds to say so. Asking again
// per step would turn one broken scenario into a run nobody waits out.
func TestFailedOpenIsCachedNotRetried(t *testing.T) {
	boom := errors.New("chromium would not start")
	f := &fakeOpener{err: boom}
	r := registryWith(f)

	for i := 0; i < 3; i++ {
		if _, err := r.Session(context.Background()); !errors.Is(err, boom) {
			t.Fatalf("ask %d got %v, want %v", i+1, err, boom)
		}
	}
	if opens, _ := f.counts(); opens != 1 {
		t.Fatalf("tried to open %d times, want 1", opens)
	}
}

// A registry whose open failed has nothing to close.
func TestCloseAfterFailedOpen(t *testing.T) {
	f := &fakeOpener{err: errors.New("chromium would not start")}
	r := registryWith(f)
	if _, err := r.Session(context.Background()); err == nil {
		t.Fatal("expected the open to fail")
	}
	if err := r.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// Closing twice -- which a runner that both defers and explicitly closes would
// do -- must not close the browser twice.
func TestRegistryCloseIsIdempotent(t *testing.T) {
	f := &fakeOpener{}
	r := registryWith(f)
	if _, err := r.Session(context.Background()); err != nil {
		t.Fatalf("Session: %v", err)
	}
	if err := r.close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := r.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, closes := f.counts(); closes != 1 {
		t.Fatalf("closed %d times, want 1", closes)
	}
}

// A context that outlived its scenario must not be able to open a browser
// nothing is left to close.
func TestSessionAfterCloseIsRefused(t *testing.T) {
	f := &fakeOpener{}
	r := registryWith(f)
	if err := r.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, err := r.Session(context.Background())
	if err == nil {
		t.Fatal("a closed registry opened a browser")
	}
	if !strings.Contains(err.Error(), "has ended") {
		t.Fatalf("error does not say why: %v", err)
	}
	if opens, _ := f.counts(); opens != 0 {
		t.Fatalf("opened %d browsers after close, want 0", opens)
	}
}

// The registry reaches a step the way ART-37's seam says it does and no other
// way.
func TestRegistryIsOnTheContextAsExecutorSessions(t *testing.T) {
	var _ executor.Sessions = (*Registry)(nil)

	f := &fakeOpener{}
	err := withScenarioOpener(context.Background(), Config{Headless: true}, f.open,
		func(ctx context.Context) error {
			sessions, ok := executor.SessionsOf(ctx)
			if !ok {
				t.Fatal("executor.SessionsOf found no registry")
			}
			if _, isRegistry := sessions.(*Registry); !isRegistry {
				t.Fatalf("the context carries a %T, want *session.Registry", sessions)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("WithScenario: %v", err)
	}
}

// A close that fails is reported only when the scenario itself did not, so a
// failing assertion is not buried under "could not close the browser".
func TestCloseErrorDoesNotBuryTheScenarioError(t *testing.T) {
	failed := errors.New("an assertion failed")
	closeErr := errors.New("the browser would not exit")
	open := func(context.Context, Config) (*Session, error) {
		return &Session{closeFn: func() error { return closeErr }}, nil
	}

	err := withScenarioOpener(context.Background(), Config{}, open, func(ctx context.Context) error {
		sessions, _ := executor.SessionsOf(ctx)
		if _, err := sessions.Session(ctx); err != nil {
			t.Fatalf("opening a session: %v", err)
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("got %v, want the scenario's own error %v", err, failed)
	}

	err = withScenarioOpener(context.Background(), Config{}, open, func(ctx context.Context) error {
		sessions, _ := executor.SessionsOf(ctx)
		_, err := sessions.Session(ctx)
		return err
	})
	if !errors.Is(err, closeErr) {
		t.Fatalf("got %v, want the close error %v", err, closeErr)
	}
}
