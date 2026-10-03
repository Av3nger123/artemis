package session

import (
	"context"
	"fmt"
	"sync"

	playwright "github.com/mxschmitt/playwright-go"

	"artemis/pkg/executor"
)

// opener opens one scenario's session.
//
// It is an unexported function type with one real implementation and one test
// one. Exporting it would make "who may open a browser" an open question; the
// answer is this package, and a caller that wants a different browser wants a
// different engine, not a different opener.
type opener func(ctx context.Context, cfg Config) (*Session, error)

// Registry is one scenario's browser session, opened on the first ask.
//
// It implements executor.Sessions, which is how a browser step reaches it: the
// registry travels on the context, the step calls executor.SessionsOf and then
// Session, and gets back a *Session it cannot close.
//
// A Registry is for exactly one scenario. Two scenarios get two registries and
// therefore two browsers, which is what keeps one scenario's cookies out of the
// next one's.
type Registry struct {
	cfg  Config
	open opener

	mu      sync.Mutex
	opened  bool
	session *Session
	err     error
}

// NewRegistry returns a registry that will open a browser configured by cfg,
// the first time something asks it for one -- and not before.
//
// Nothing happens here. No driver is installed, no process is started and
// nothing is written to disk, which is what lets the runner build one of these
// for every scenario without knowing whether any of them has a browser step.
func NewRegistry(cfg Config) *Registry {
	return &Registry{cfg: cfg, open: openBrowser}
}

// Session is the scenario's session, opening it if this is the first ask.
//
// Two asks return the same *Session, which is the whole point: step 4 reads
// what step 3 typed. The returned value is `any` because executor.Sessions
// declares it so -- see that interface's doc comment -- and it is always a
// *Session.
//
// A failed open is cached with the same reasoning as the driver's: a browser
// that will not launch will not launch for step 5 either, and retrying per step
// turns one broken scenario into a run nobody waits out.
func (r *Registry) Session(ctx context.Context) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.opened {
		if r.err != nil {
			return nil, r.err
		}
		return r.session, nil
	}
	r.opened = true

	s, err := r.open(ctx, r.cfg)
	if err != nil {
		r.err = err
		return nil, err
	}
	r.session = s
	return r.session, nil
}

// close ends whatever this registry opened.
//
// It is idempotent and it is a no-op on a registry that was never asked, which
// is what lets WithScenario defer it unconditionally. Closing marks the
// registry opened, so a step that somehow asks after the scenario ended gets an
// error rather than a fresh browser nothing will ever close.
func (r *Registry) close() error {
	r.mu.Lock()
	s := r.session
	r.session = nil
	if !r.opened {
		r.opened = true
		r.err = errScenarioOver
	} else if r.err == nil {
		r.err = errScenarioOver
	}
	r.mu.Unlock()
	return s.close()
}

// errScenarioOver is what a step asking for a session after its scenario has
// ended is told. It should be unreachable; it is here so that if the runner
// ever leaks a context past the boundary, the symptom is one failed step rather
// than a browser process with nothing left to close it.
var errScenarioOver = fmt.Errorf("the scenario that owns this browser session has ended")

// WithScenario runs fn with a browser registry for one scenario on its context,
// and closes whatever fn opened on the way out.
//
// This is the scenario boundary and it is a function rather than a convention
// on purpose: "close the session when the scenario ends, pass or fail" is a
// rule the runner would otherwise have to remember at every return, and a
// panicking step is exactly the case a forgotten close loses a browser process
// to. A panic from fn is closed around and then re-panicked, so the runner's
// own recovery sees what it would have seen.
//
// The close error is reported only when fn itself succeeded. A scenario that
// failed has already got the news a reader cares about, and replacing it with
// "could not close the browser" buries it.
func WithScenario(ctx context.Context, cfg Config, fn func(context.Context) error) error {
	return withScenarioOpener(ctx, cfg, openBrowser, fn)
}

// withScenarioOpener is WithScenario with the browser swapped out, which is how
// every lifetime rule above is tested in CI, where there is no browser.
func withScenarioOpener(ctx context.Context, cfg Config, open opener, fn func(context.Context) error) (err error) {
	reg := NewRegistry(cfg)
	reg.open = open
	defer func() {
		closeErr := reg.close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return fn(executor.WithSessions(ctx, reg))
}

// openBrowser is the real opener: driver, browser, context, page.
//
// Each layer is closed if the next one fails, because a half-open session has
// nobody to close it -- Registry.Session stores an error, not a Session, so
// anything already launched here is unreachable the moment this returns.
// ctx is unused: playwright-go's Launch, NewContext and NewPage take no
// context, and their own timeouts are the ones that apply. It is in the
// signature because opener's other implementation -- the test one -- is
// checked against it, and because an engine that did take one would not then
// be a signature change through the whole package.
func openBrowser(_ context.Context, cfg Config) (*Session, error) {
	viewport, err := parseViewport(cfg.Viewport)
	if err != nil {
		return nil, err
	}

	pw, err := start()
	if err != nil {
		return nil, err
	}

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(cfg.Headless),
	})
	if err != nil {
		return nil, fmt.Errorf("launch chromium: %w", err)
	}

	browserCtx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport: viewport,
	})
	if err != nil {
		_ = browser.Close()
		return nil, fmt.Errorf("open a browser context: %w", err)
	}

	page, err := browserCtx.NewPage()
	if err != nil {
		_ = browserCtx.Close()
		_ = browser.Close()
		return nil, fmt.Errorf("open a page: %w", err)
	}

	return &Session{browser: browser, context: browserCtx, page: page}, nil
}
