package executor

import "context"

// This file is where a browser session comes from, and it exists before
// anything needs one on purpose.
//
// A browser step is not stateless: step 4 depends on what step 3 did to the
// DOM, so a scenario owns one lazily created session, shared by every browser
// step in it and closed when the scenario ends, pass or fail. That makes the
// session something an executor *borrows*, which is a change to the Executor
// seam rather than a detail of the browser step -- so it lands here, with the
// seam, instead of reopening the seam when pkg/session arrives (ART-45).
//
// Two things are fixed here and both would be expensive to change later.
//
// The registry on the context is the *scenario's*. A browser step asks for "the
// session" and gets its scenario's, with no key: a models.Step carries no
// scenario name, so a keyed lookup would mean threading a name onto either the
// step or the context, and the runner already has a per-scenario boundary to
// derive a context at. How a registry keys sessions internally is its own
// business.
//
// A session is `any`. What a session can *do* -- navigate, click, read an
// element -- belongs to pkg/session (ART-45) and to the browser step that
// drives it (ART-46), and naming it here would make this package depend on a
// subsystem that does not exist. What must not be renegotiated later is where a
// session comes from and who closes it, and that is what these three names say.

// Sessions hands a step the browser session its scenario owns.
//
// Session opens one on the first ask and returns the same one after, so two
// browser steps in a scenario see one page. The error is a session that could
// not be opened -- no browser, a driver that would not start -- which the
// calling executor turns into a step that could not run.
//
// Nothing closes a session through this interface. The registry owns lifetime
// and closes what it opened when the scenario ends, which is why a step that
// failed, errored or panicked cannot leak a browser process.
type Sessions interface {
	Session(ctx context.Context) (any, error)
}

// sessionsKey is the context key the registry travels under. It is an
// unexported zero-size type, so no other package can collide with it and none
// can read the registry off a context without going through SessionsOf.
type sessionsKey struct{}

// WithSessions returns a copy of ctx carrying s.
//
// A nil registry is not stored: "this run has no browser" and "this run has a
// registry that is nil" are the same fact to every caller, and storing the
// second would make SessionsOf hand back an interface that panics on use.
func WithSessions(ctx context.Context, s Sessions) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionsKey{}, s)
}

// SessionsOf returns the registry on ctx and whether there was one.
//
// There is no registry on a run with no browser step in it, which is the normal
// case and not an error: the http and terminal executors never ask. A browser
// step that asks and is told no reports a step it could not run, because a
// browser step with nowhere to open a page cannot do anything else.
func SessionsOf(ctx context.Context) (Sessions, bool) {
	if ctx == nil {
		return nil, false
	}
	s, ok := ctx.Value(sessionsKey{}).(Sessions)
	if !ok || s == nil {
		return nil, false
	}
	return s, true
}
