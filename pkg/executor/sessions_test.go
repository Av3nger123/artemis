package executor

import (
	"context"
	"errors"
	"testing"
)

// fakeSessions is a registry that hands back one session and counts the asks,
// so a test can see that two steps share one.
type fakeSessions struct {
	page any
	err  error
	asks int
}

func (f *fakeSessions) Session(context.Context) (any, error) {
	f.asks++
	return f.page, f.err
}

var _ Sessions = (*fakeSessions)(nil)

// The registry goes on and comes back off: this is the whole of the seam
// subsystem B depends on, so it is pinned.
func TestSessionsRoundTrip(t *testing.T) {
	want := &fakeSessions{page: "a page"}
	ctx := WithSessions(context.Background(), want)

	got, ok := SessionsOf(ctx)
	if !ok {
		t.Fatal("SessionsOf() = not found, want the registry that was put on")
	}
	if got != want {
		t.Errorf("SessionsOf() = %#v, want %#v", got, want)
	}

	page, err := got.Session(ctx)
	if err != nil {
		t.Fatalf("Session() = %v, want nil", err)
	}
	if page != "a page" {
		t.Errorf("Session() = %#v, want the registry's page", page)
	}
}

// A run with no browser step in it carries no registry, which is the normal
// case and not an error. The http and terminal executors never ask; a browser
// step that asks is told no and fails the step itself.
func TestNoSessionsOnAPlainContext(t *testing.T) {
	if s, ok := SessionsOf(context.Background()); ok {
		t.Errorf("SessionsOf() = %#v, true; want not found on a bare context", s)
	}
}

// A nil context is what a caller that forgot one hands over. It reports absent
// rather than panicking, because a panic here would take down a run over a
// browser nobody asked for.
func TestNoSessionsOnANilContext(t *testing.T) {
	//nolint:staticcheck // passing nil is the case under test.
	if s, ok := SessionsOf(nil); ok {
		t.Errorf("SessionsOf(nil) = %#v, true; want not found", s)
	}
}

// A nil registry is not stored. "This run has no browser" and "this run has a
// registry that is nil" are the same fact to every caller, and storing the
// second would hand back an interface that panics on its first use.
func TestANilRegistryIsNotStored(t *testing.T) {
	ctx := WithSessions(context.Background(), nil)
	if s, ok := SessionsOf(ctx); ok {
		t.Errorf("SessionsOf() = %#v, true; want not found after WithSessions(nil)", s)
	}
}

// A typed nil is the other way to hand over nothing: a (*fakeSessions)(nil) in
// a Sessions is not == nil, so WithSessions stores it, and the caller that
// reads it back gets an interface whose method is a nil-receiver call. That is
// the registry's business, not this file's -- what is pinned here is that the
// context layer does not lose it.
func TestATypedNilRegistryIsStored(t *testing.T) {
	var reg *fakeSessions
	ctx := WithSessions(context.Background(), reg)
	got, ok := SessionsOf(ctx)
	if !ok {
		t.Fatal("SessionsOf() = not found, want the typed-nil registry back")
	}
	if got != Sessions(reg) {
		t.Errorf("SessionsOf() = %#v, want the registry that was put on", got)
	}
}

// Two asks are two asks: whether they share a session is the registry's
// promise, and this is the shape that lets it keep one.
func TestTwoStepsAskTheSameRegistry(t *testing.T) {
	reg := &fakeSessions{page: "one page"}
	ctx := WithSessions(context.Background(), reg)

	for i := 0; i < 2; i++ {
		s, _ := SessionsOf(ctx)
		if _, err := s.Session(ctx); err != nil {
			t.Fatalf("Session() %d = %v, want nil", i, err)
		}
	}
	if reg.asks != 2 {
		t.Errorf("registry was asked %d times, want 2", reg.asks)
	}
}

// A session that will not open is the registry's error, handed straight to the
// executor that asked, which turns it into a step that could not run.
func TestASessionThatWillNotOpenReportsWhy(t *testing.T) {
	want := errors.New("no browser on this machine")
	ctx := WithSessions(context.Background(), &fakeSessions{err: want})

	s, ok := SessionsOf(ctx)
	if !ok {
		t.Fatal("SessionsOf() = not found")
	}
	if _, err := s.Session(ctx); !errors.Is(err, want) {
		t.Errorf("Session() = %v, want %v", err, want)
	}
}
