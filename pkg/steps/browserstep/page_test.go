//go:build browser

// This file is the half of browserstep that only a real browser can check:
// that pwPage maps each driver method onto the right Playwright call, and that
// Playwright behaves the way page.go assumes -- in particular that a selector
// matching nothing answers 0, false and null rather than waiting and erroring.
//
//	go test -tags browser ./pkg/steps/browserstep
//
// It is tagged off by default because CI has no browser and the first run on a
// cold machine downloads about 683 MB (docs/browser-engine.md). Everything
// above the driver -- the act loop, the roots, the screenshot naming -- is
// covered against fakePage in the default suite, which is what CI runs.
//
// Every page it drives comes from the local fixture server. No test here, or
// anywhere in artemis, reaches a public site.

package browserstep

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/session"
	"artemis/pkg/shared/models"
	"artemis/pkg/steps/browserstep/fixture"
)

// onFixture stands up the fixture server and a real browser, and runs fn with
// the driver and the server. The session is closed on the way out whatever fn
// does, which is session.WithScenario's whole job.
func onFixture(t *testing.T, fn func(d driver, srv *httptest.Server)) {
	t.Helper()
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)

	err := session.WithScenario(context.Background(), session.Config{Headless: true},
		func(ctx context.Context) error {
			page, err := Open(ctx)
			if err != nil {
				return err
			}
			// A generous per-call budget: these tests are about what the calls
			// do, not about timing out.
			page.driver.SetTimeout(15 * time.Second)
			fn(page.driver, srv)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

// All eight actions against a real page, as one flow: fill the form, choose a
// plan, attach a file, hover, press, click to submit.
//
// It asserts the *effect* rather than that each call returned nil. #who on the
// next page holds what `fill` typed, which is the only evidence that the text
// went into the right box and that the form submitted it.
func TestTheEightActionsDriveARealPage(t *testing.T) {
	onFixture(t, func(d driver, srv *httptest.Server) {
		upload := filepath.Join(t.TempDir(), "avatar.png")
		if err := os.WriteFile(upload, []byte("not really a png"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := d.Goto(srv.URL); err != nil {
			t.Fatal(err)
		}
		if err := d.Fill(fixture.SelUser, "alice"); err != nil {
			t.Fatal(err)
		}
		if err := d.Select(fixture.SelPlan, "pro"); err != nil {
			t.Fatal(err)
		}
		if err := d.Upload(fixture.SelAvatar, upload); err != nil {
			t.Fatal(err)
		}
		if err := d.Hover(fixture.SelMenu); err != nil {
			t.Fatal(err)
		}
		// Press goes to whatever has focus, so focus something first: a click
		// into the password box, then a key into it.
		if err := d.Click(fixture.SelPass); err != nil {
			t.Fatal(err)
		}
		if err := d.Press("x"); err != nil {
			t.Fatal(err)
		}
		if got, err := d.Value(fixture.SelPass); err != nil || got != "x" {
			t.Errorf("value(%s) = %v, %v; want \"x\" -- press did not reach the focused field", fixture.SelPass, got, err)
		}
		d.Wait(50 * time.Millisecond)
		if err := d.Click(fixture.SelSubmit); err != nil {
			t.Fatal(err)
		}

		// The form posted what was filled and what was selected.
		if got, err := d.Text(fixture.SelWho); err != nil || got != "alice" {
			t.Errorf("text(%s) = %v, %v; want \"alice\" -- fill or submit did not work", fixture.SelWho, got, err)
		}
		if got, err := d.Text(fixture.SelBadge); err != nil || !strings.Contains(got.(string), "pro") {
			t.Errorf("text(%s) = %v, %v; want it to name the selected plan", fixture.SelBadge, got, err)
		}
		// And the two roots followed the navigation.
		if url, err := d.URL(); err != nil || !strings.HasSuffix(url, fixture.PathDone) {
			t.Errorf("page.url = %v, %v; want it to end in %s", url, err, fixture.PathDone)
		}
		if title, err := d.Title(); err != nil || title != "Welcome" {
			t.Errorf("page.title = %v, %v; want Welcome", title, err)
		}
	})
}

// The five element functions against a real page, and -- the part that only a
// browser can settle -- what Playwright actually answers for a selector that
// matches nothing.
//
// SPEC.md fixes these as a spec decision: count() is 0, visible() is false, and
// text(), value() and attr() are null. They answer rather than erroring because
// `expect count(".invoice") == 0` and `expect text(".error") is null` are
// assertions an author wants to write.
func TestTheElementFunctionsAndTheEmptyMatchAnswers(t *testing.T) {
	onFixture(t, func(d driver, srv *httptest.Server) {
		if err := d.Goto(srv.URL + fixture.PathForm); err != nil {
			t.Fatal(err)
		}
		if err := d.Fill(fixture.SelUser, "bob"); err != nil {
			t.Fatal(err)
		}

		if got, err := d.Value(fixture.SelUser); err != nil || got != "bob" {
			t.Errorf("value(%s) = %v, %v; want bob", fixture.SelUser, got, err)
		}
		if got, err := d.Attr(fixture.SelHome, "href"); err != nil || got != "/" {
			t.Errorf("attr(%s, href) = %v, %v; want /", fixture.SelHome, got, err)
		}
		if got, err := d.Count("input"); err != nil || got.(float64) < 3 {
			t.Errorf("count(input) = %v, %v; want at least the three inputs on the form", got, err)
		}
		if got, err := d.Visible(fixture.SelSubmit); err != nil || got != true {
			t.Errorf("visible(%s) = %v, %v; want true", fixture.SelSubmit, got, err)
		}

		// Nothing matches. Each of these must answer, and answer *quickly*: a
		// read that auto-waited would spend the page's whole timeout here,
		// which is what the elapsed check below catches.
		start := time.Now()
		for _, tc := range []struct {
			what string
			got  func() (any, error)
			want any
		}{
			{"count", func() (any, error) { return d.Count(fixture.SelMissing) }, float64(0)},
			{"visible", func() (any, error) { return d.Visible(fixture.SelMissing) }, false},
			{"text", func() (any, error) { return d.Text(fixture.SelMissing) }, nil},
			{"value", func() (any, error) { return d.Value(fixture.SelMissing) }, nil},
			{"attr", func() (any, error) { return d.Attr(fixture.SelMissing, "href") }, nil},
		} {
			got, err := tc.got()
			if err != nil {
				t.Errorf("%s(%s) errored: %v -- an absent element is an answer, not a failure", tc.what, fixture.SelMissing, err)
				continue
			}
			if got != tc.want {
				t.Errorf("%s(%s) = %#v, want %#v", tc.what, fixture.SelMissing, got, tc.want)
			}
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("the five empty-match reads took %v; an element function must not wait -- `within` is the only waiting", elapsed)
		}

		// An absent attribute on a *present* element is null as well, which is
		// the distinction GetAttribute cannot make and Evaluate can.
		if got, err := d.Attr(fixture.SelHome, "data-nothing"); err != nil || got != nil {
			t.Errorf("attr(%s, data-nothing) = %#v, %v; want null", fixture.SelHome, got, err)
		}
	})
}

// A relative `goto` resolves against where the page already is, which is
// SPEC.md's rule and is not something Playwright does on its own without a base
// URL on the browser context.
func TestARelativeGotoResolvesAgainstThePage(t *testing.T) {
	onFixture(t, func(d driver, srv *httptest.Server) {
		if err := d.Goto(srv.URL + fixture.PathNever); err != nil {
			t.Fatal(err)
		}
		if err := d.Goto(fixture.PathForm); err != nil {
			t.Fatalf("a relative goto failed: %v", err)
		}
		if title, err := d.Title(); err != nil || title != "Sign in" {
			t.Errorf("after a relative goto the page is %v, %v; want the form", title, err)
		}
		url, err := d.URL()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(url, srv.URL) {
			t.Errorf("page.url = %q, want it under %s -- the relative goto did not resolve", url, srv.URL)
		}
	})
}

// A relative first `goto` has nothing to resolve against, and says what to
// write instead rather than failing with the engine's protocol error.
func TestARelativeFirstGotoSaysWhatToWrite(t *testing.T) {
	onFixture(t, func(d driver, _ *httptest.Server) {
		err := d.Goto("/orders")
		if err == nil {
			t.Fatal("a relative goto on a fresh page succeeded")
		}
		for _, want := range []string{"relative", "full URL"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("goto = %v, want it to mention %q", err, want)
			}
		}
	})
}

// A selector that matches nothing is an answer; a selector the engine cannot
// parse is a failure. Reporting null for a typo in the selector syntax would
// hide it forever -- that selector will never match anything.
func TestAMalformedSelectorIsAnError(t *testing.T) {
	onFixture(t, func(d driver, srv *httptest.Server) {
		if err := d.Goto(srv.URL); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Text("div[[["); err == nil {
			t.Error("text(\"div[[[\") = nil error, want the engine's complaint about the selector")
		}
	})
}

// An action against an element that is not there fails within the budget it was
// given, rather than hanging or returning success. This is the auto-wait that
// is inherited rather than reimplemented: it waits, and then it gives up when
// told to.
func TestAnActionHonoursItsBudget(t *testing.T) {
	onFixture(t, func(d driver, srv *httptest.Server) {
		if err := d.Goto(srv.URL); err != nil {
			t.Fatal(err)
		}
		d.SetTimeout(700 * time.Millisecond)

		start := time.Now()
		err := d.Click(fixture.SelMissing)
		elapsed := time.Since(start)

		if err == nil {
			t.Fatal("clicking an element that is not there succeeded")
		}
		if elapsed < 500*time.Millisecond {
			t.Errorf("the click gave up after %v, want it to have waited out its 700ms budget", elapsed)
		}
		if elapsed > 5*time.Second {
			t.Errorf("the click took %v, want it bounded by its 700ms budget", elapsed)
		}
	})
}

// An action does not need a `wait` in front of it: a click on an element that
// appears late waits for it, which is Playwright's behaviour and is the claim
// SPEC.md makes about every action.
func TestAnActionAutoWaitsForALateElement(t *testing.T) {
	onFixture(t, func(d driver, srv *httptest.Server) {
		if err := d.Goto(srv.URL); err != nil {
			t.Fatal(err)
		}
		if err := d.Click(fixture.SelSubmit); err != nil {
			t.Fatal(err)
		}
		// [role=status] is on the page from the start but says "Working" until
		// the settle delay; a click on it is not the auto-wait case. The
		// auto-wait case is an element that is not in the DOM yet at all, so
		// drive the page that grows one: the status element's text changes, and
		// a text= selector for its settled value does not match until then.
		d.SetTimeout(10 * time.Second)
		start := time.Now()
		if err := d.Hover("text=Pro"); err != nil {
			t.Fatalf("hover on a late element failed: %v", err)
		}
		elapsed := time.Since(start)
		if elapsed < fixture.DefaultSettle/2 {
			t.Errorf("the hover returned after %v, before the element could exist (%v)", elapsed, fixture.DefaultSettle)
		}
	})
}

// A screenshot is a real PNG on disk. It is the artifact the JSON report names,
// so a file that is zero bytes or not an image would be worse than no file.
func TestAScreenshotIsARealPNG(t *testing.T) {
	onFixture(t, func(d driver, srv *httptest.Server) {
		if err := d.Goto(srv.URL); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "shot.png")
		if err := d.Screenshot(path); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) < 1024 {
			t.Errorf("the screenshot is %d bytes, want a real image", len(raw))
		}
		if !strings.HasPrefix(string(raw[:8]), "\x89PNG\r\n\x1a\n") {
			t.Errorf("the screenshot does not start with the PNG magic: % x", raw[:8])
		}
	})
}

// The whole executor over a real page: the acts run, and the roots come back.
// Everything between Observe and the driver is covered in CI against fakePage;
// this is the one run of it that proves the two halves fit.
func TestObserveOverARealPage(t *testing.T) {
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)

	err := session.WithScenario(context.Background(), session.Config{Headless: true},
		func(ctx context.Context) error {
			roots, err := Executor{}.Observe(ctx, models.Step{
				Name: "sign in", Type: StepType, Timeout: "20s",
				Browser: models.Browser{Acts: []models.Act{
					{Name: ActGoto, Target: srv.URL, Line: 3},
					{Name: ActFill, Target: fixture.SelUser, Value: "alice", Line: 4},
					{Name: ActClick, Target: fixture.SelSubmit, Line: 5},
				}},
			}, executor.NewScope())
			if err != nil {
				return err
			}
			page := roots[RootPage].(map[string]any)
			if !strings.HasSuffix(page[MemberURL].(string), fixture.PathDone) {
				t.Errorf("page.url = %v, want it to end in %s", page[MemberURL], fixture.PathDone)
			}
			if page[MemberTitle] != "Welcome" {
				t.Errorf("page.title = %v, want Welcome", page[MemberTitle])
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}
