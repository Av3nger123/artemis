// Package session owns the browser a scenario runs in: when it is opened, what
// it is configured with, and -- the part that matters -- when it is closed.
//
// # What a session is
//
// A browser step is not stateless. Step 4 reads what step 3 typed, so the page
// has to outlive the step that opened it. A scenario therefore owns exactly one
// session: opened lazily on the first ask, handed unchanged to every later ask,
// and closed when the scenario ends -- passing, failing, or panicking halfway
// through a step.
//
//	err := session.WithScenario(ctx, cfg, func(ctx context.Context) error {
//	    return runSteps(ctx)   // steps reach the session through ctx
//	})
//
// WithScenario is the whole of the runner's side of this. It builds a Registry,
// puts it on the context with executor.WithSessions, and closes it on the way
// out whatever happens inside. A step reaches the session the way ART-37's seam
// says:
//
//	sessions, ok := executor.SessionsOf(ctx)
//	s, err := sessions.Session(ctx)
//	page := s.(*session.Session).Page()
//
// # Who closes it, and why it is not the executor
//
// The registry owns lifetime. Nothing a step can reach has an exported Close:
// *Session has Page and nothing else. That is deliberate and it is the whole
// reason this package exists rather than a browser step that launches its own
// browser -- a step that panics mid-run cannot leak a browser process if it was
// never able to own one.
//
// Session returns `any` because that is what executor.Sessions declares, and
// executor.Sessions declares it so that pkg/executor -- which the http and
// terminal steps also go through -- does not acquire a dependency on Playwright.
// The cast above is the price, paid in one place, by the one step type that
// needs a page.
//
// # Nobody else pays for the browser
//
// Nothing here downloads, launches or starts anything until something actually
// calls Session. A run of api and terminal steps creates a Registry, never asks
// it for anything, and closes it: no driver directory, no node, no browser. See
// TestAPIOnlyRunTouchesNoDriver, which asserts exactly that and exists to catch
// a future refactor that moves the install somewhere eager.
//
// # The engine
//
// playwright-go, confirmed in ART-45 against a real page. The write-up --
// "The browser engine: playwright-go, confirmed" -- is in the project docs
// beside the DSL design, not in this repo, which keeps /docs ignored on
// purpose. It has the spike, the measured install cost, and the reason the
// import path is github.com/mxschmitt/playwright-go rather than the
// playwright-community one: the latter's newest release cannot download its
// own driver.
//
// The browser-facing test in this package is behind a build tag, because CI has
// no browser and a 683 MB download is not a unit test:
//
//	go test -tags browser ./pkg/session
//
// # What this package does not do
//
// It opens a page and takes no view on what is done with it. The `browser { ...
// }` grammar and the browser expression scope are ART-46; the step that drives
// the page, and `within`, are ART-47.
package session
