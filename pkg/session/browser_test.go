//go:build browser

// This file is ART-45's spike, kept rather than thrown away. It is what
// confirmed playwright-go against a real page, and it is the only test in
// artemis that needs a browser:
//
//	go test -tags browser ./pkg/session
//
// It is tagged off by default because CI has no browser and the first run on a
// cold machine downloads about 683 MB. The lifetime rules it checks against a
// real Chromium are checked against a fake opener in registry_test.go, which
// does run in CI; what only this file can prove is that the engine behaves the
// way the design said it would when it chose Playwright.

package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"

	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// The fixture: a login form, and a welcome page that grows an element 1.2s
// after it loads. The late element is the whole auto-waiting question -- a
// runner with no wait model reads the DOM before it is there and reports a
// failure that is really a race.
const (
	lateElementDelay = 1200 * time.Millisecond

	loginPage = `<!doctype html><title>Login</title>
<form method="POST" action="/done">
<input name="user" id="user">
<button type="submit" id="go">Sign in</button>
</form>`

	welcomePage = `<!doctype html><title>Welcome</title>
<div id="greeting">hello <span id="who">%s</span></div>
<script>setTimeout(function(){var d=document.createElement("div");d.id="late";d.textContent="settled";document.body.appendChild(d);},%d);</script>`
)

func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/done" {
			_ = r.ParseForm()
			fmt.Fprintf(w, welcomePage, r.FormValue("user"), lateElementDelay.Milliseconds())
			return
		}
		fmt.Fprint(w, loginPage)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The flow the design named: goto, fill, click, assert. Every call here is the
// Go spelling of a call the Python and JavaScript targets will emit, which is
// the reason playwright-go was chosen over a pure-Go engine.
func TestBrowserGotoFillClickAssert(t *testing.T) {
	srv := fixtureServer(t)

	err := WithScenario(context.Background(), Config{Headless: true, Viewport: "1280x720"},
		func(ctx context.Context) error {
			page := pageFrom(t, ctx)

			if _, err := page.Goto(srv.URL); err != nil {
				return fmt.Errorf("goto: %w", err)
			}
			if err := page.Fill("#user", "alice"); err != nil {
				return fmt.Errorf("fill: %w", err)
			}
			if err := page.Click("#go"); err != nil {
				return fmt.Errorf("click: %w", err)
			}

			// page.title, and the element function text(sel), from the design's
			// browser scope.
			title, err := page.Title()
			if err != nil {
				return fmt.Errorf("title: %w", err)
			}
			if title != "Welcome" {
				return fmt.Errorf("page.title is %q, want %q", title, "Welcome")
			}
			who, err := page.TextContent("#who")
			if err != nil {
				return fmt.Errorf("text(#who): %w", err)
			}
			if who != "alice" {
				return fmt.Errorf("text(#who) is %q, want %q -- the form did not submit what was filled", who, "alice")
			}
			// page.url, likewise.
			if !strings.HasSuffix(page.URL(), "/done") {
				return fmt.Errorf("page.url is %q, want it to end in /done", page.URL())
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

// Auto-waiting, the second thing that chose Playwright. Nothing below sleeps or
// polls: a bare read of an element that does not exist yet has to block until
// it does.
func TestBrowserAutoWaitsForALateElement(t *testing.T) {
	srv := fixtureServer(t)

	err := WithScenario(context.Background(), Config{Headless: true},
		func(ctx context.Context) error {
			page := pageFrom(t, ctx)
			if _, err := page.Goto(srv.URL + "/done"); err != nil {
				return fmt.Errorf("goto: %w", err)
			}

			start := time.Now()
			late, err := page.TextContent("#late")
			waited := time.Since(start)
			if err != nil {
				return fmt.Errorf("text(#late) did not wait for the element: %w", err)
			}
			if late != "settled" {
				return fmt.Errorf("text(#late) is %q, want %q", late, "settled")
			}
			// It must actually have waited -- an element that was already there
			// would prove nothing about the wait model.
			if waited < lateElementDelay/2 {
				return fmt.Errorf("returned after %s, sooner than the element appears (%s): the fixture is wrong", waited, lateElementDelay)
			}
			t.Logf("text(#late) waited %s for an element that appears after %s", waited.Round(10*time.Millisecond), lateElementDelay)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

// `expect visible(".modal") within "5s"` lowers to this call and no other. Both
// halves matter: it succeeds when the element arrives inside the budget, and it
// gives up roughly when the budget says rather than hanging or returning early.
func TestBrowserWithinLowersToATimeout(t *testing.T) {
	srv := fixtureServer(t)

	err := WithScenario(context.Background(), Config{Headless: true},
		func(ctx context.Context) error {
			page := pageFrom(t, ctx)
			if _, err := page.Goto(srv.URL + "/done"); err != nil {
				return fmt.Errorf("goto: %w", err)
			}
			expect := playwright.NewPlaywrightAssertions()

			if err := expect.Locator(page.Locator("#late")).ToBeVisible(
				playwright.LocatorAssertionsToBeVisibleOptions{Timeout: playwright.Float(5000)},
			); err != nil {
				return fmt.Errorf("within \"5s\" on an element that arrives in 1.2s: %w", err)
			}

			const budget = time.Second
			start := time.Now()
			err := expect.Locator(page.Locator("#never")).ToBeVisible(
				playwright.LocatorAssertionsToBeVisibleOptions{Timeout: playwright.Float(float64(budget.Milliseconds()))},
			)
			took := time.Since(start)
			if err == nil {
				return errors.New("an element that does not exist was reported visible")
			}
			if took < budget/2 || took > budget*4 {
				return fmt.Errorf("gave up after %s, want about %s: the budget is not being honoured", took, budget)
			}
			if !strings.Contains(err.Error(), "#never") {
				return fmt.Errorf("the failure does not name the selector: %v", err)
			}
			t.Logf("within %s failed after %s: %v", budget, took.Round(10*time.Millisecond), firstLine(err.Error()))
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

// The page persists across steps, which is the reason a scenario owns a session
// rather than a step doing. Written as two steps through executor.Run, because
// that is the shape the runner will use.
func TestBrowserPagePersistsAcrossSteps(t *testing.T) {
	srv := fixtureServer(t)
	reg := executor.NewRegistry()
	reg.Register("browser", executor.Func(func(ctx context.Context, step models.Step, scope executor.Scope) (map[string]any, error) {
		sessions, ok := executor.SessionsOf(ctx)
		if !ok {
			return nil, errors.New("no session registry on the context")
		}
		s, err := sessions.Session(ctx)
		if err != nil {
			return nil, err
		}
		page := s.(*Session).Page()

		switch step.Name {
		case "sign in":
			if _, err := page.Goto(srv.URL); err != nil {
				return nil, err
			}
			if err := page.Fill("#user", "alice"); err != nil {
				return nil, err
			}
			return &result.StepResult{}, page.Click("#go")
		case "read it back":
			// A different step, no navigation: it must see the DOM the first
			// step left behind.
			who, err := page.TextContent("#who")
			if err != nil {
				return nil, err
			}
			if who != "alice" {
				return nil, fmt.Errorf("the second step sees %q, want %q -- the page did not persist", who, "alice")
			}
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected step %q", step.Name)
	}))

	err := WithScenario(context.Background(), Config{Headless: true}, func(ctx context.Context) error {
		for _, name := range []string{"sign in", "read it back"} {
			if _, err := executor.Observe(ctx, reg, models.Step{Name: name, Type: "browser"}, executor.NewScope()); err != nil {
				return fmt.Errorf("step %q: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The Done-When, against a real browser: a step panics and the process does not
// survive it.
func TestBrowserProcessDoesNotSurviveAPanickingStep(t *testing.T) {
	srv := fixtureServer(t)

	// The machine's own Chrome must not be mistaken for a leak, so the
	// assertion is on the processes this test *added*: anything matching after
	// the scenario that was not matching before it.
	before := headlessShellPIDs(t)

	var browser playwright.Browser

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not reach the caller")
			}
		}()
		_ = WithScenario(context.Background(), Config{Headless: true}, func(ctx context.Context) error {
			sessions, ok := executor.SessionsOf(ctx)
			if !ok {
				t.Fatal("no session registry on the context")
			}
			s, err := sessions.Session(ctx)
			if err != nil {
				t.Fatalf("opening a session: %v", err)
			}
			sess := s.(*Session)
			browser = sess.browser
			if _, err := sess.Page().Goto(srv.URL); err != nil {
				t.Fatalf("goto: %v", err)
			}
			if !browser.IsConnected() {
				t.Fatal("the browser is not connected before the panic; this test proves nothing")
			}
			panic("the browser step blew up")
		})
	}()

	if browser == nil {
		t.Fatal("no browser was opened")
	}
	if browser.IsConnected() {
		t.Fatal("the browser is still connected after a panicking step: the registry leaked a browser process")
	}
	// IsConnected is Playwright's view. This is the operating system's.
	for pid := range headlessShellPIDs(t) {
		if !before[pid] {
			t.Fatalf("a headless-shell process (pid %s) outlived the panicking step", pid)
		}
	}
}

// headlessShellPIDs is the set of chromium headless-shell processes running
// now. An empty set is a fine answer -- the test only ever compares two of
// them, so a machine with no pgrep simply compares nothing and leans on
// IsConnected above.
func headlessShellPIDs(t *testing.T) map[string]bool {
	t.Helper()
	pids := map[string]bool{}
	if _, err := exec.LookPath("pgrep"); err != nil {
		return pids
	}
	out, err := exec.Command("pgrep", "-f", "headless_shell").Output()
	if err != nil {
		return pids // exit 1 means no match, which is a legitimate answer
	}
	for _, pid := range strings.Fields(string(out)) {
		pids[pid] = true
	}
	return pids
}

// pageFrom is how a browser step reaches its page: off the context, through the
// seam, with the one cast this package's `any` return costs.
func pageFrom(t *testing.T, ctx context.Context) playwright.Page {
	t.Helper()
	sessions, ok := executor.SessionsOf(ctx)
	if !ok {
		t.Fatal("no session registry on the scenario's context")
	}
	s, err := sessions.Session(ctx)
	if err != nil {
		t.Fatalf("opening a session: %v", err)
	}
	sess, ok := s.(*Session)
	if !ok {
		t.Fatalf("the registry returned a %T, want *session.Session", s)
	}
	return sess.Page()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// The driver is per process and the tests above share it. Stopping it here
// rather than leaving it to process exit is the same discipline the runner
// follows.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := Shutdown(); err != nil {
		fmt.Println("shutting the driver down:", err)
	}
	os.Exit(code)
}
