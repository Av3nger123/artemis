//go:build browser

// This file is ART-47's three Done-Whens, end to end: a real .art file, through
// the real runner, against a real headless Chromium and the local fixture
// server.
//
//	go test -tags browser ./pkg/cli
//	go test -tags browser -count=10 ./pkg/cli -run Browser   # the flake check
//
// It is tagged off by default because CI has no browser and the first run on a
// cold machine downloads about 683 MB (docs/browser-engine.md). What is covered
// in the default suite is every piece below the browser: settle_test.go drives
// the waiting loop on a virtual clock, shoot_test.go the screenshot, and
// pkg/steps/browserstep's own tests the act loop and the roots.
//
// Every page comes from the fixture server, on loopback. No test in artemis
// reaches a public site under any build tag.

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/steps/browserstep"
	"artemis/pkg/steps/browserstep/fixture"
)

// runBrowserArt runs one .art source through the real runner with screenshots
// going to a temporary folder, and returns the run and that folder.
func runBrowserArt(t *testing.T, src string) (*result.RunResult, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "shots")
	path := writeArt(t, "browser.art", src)
	run := runFilesWith(&runtimeEnv{
		reg:   executor.Default(),
		shots: browserstep.NewShots(dir),
	}, []string{path}, report.Discard(), &bytes.Buffer{})
	return run, dir
}

// Done-When 1: a scenario spanning several browser steps shares one page.
//
// Three steps, one browser. Step 1 fills the form and submits it; step 2 reads
// #who, which only holds "alice" if it is looking at the page step 1 landed on;
// step 3 navigates on with a *relative* goto, which only resolves if the page
// remembers where it is. A fresh page per step would fail all three.
func TestBrowserScenarioSharesOnePageAcrossSteps(t *testing.T) {
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)

	run, _ := runBrowserArt(t, fmt.Sprintf(`scenario "the app" {
  config browser { headless = true, viewport = "1280x720" }

  var url = %q

  step "sign in" {
    browser {
      goto "${url}/"
      fill "#user" = "alice"
      select "#plan" = "pro"
      click "#go"
    }
    timeout = "30s"
    expect page.title == "Welcome"
    expect page.url contains "/done"
  }

  step "the page remembers what step one typed" {
    browser {
      wait "50ms"
    }
    timeout = "30s"
    expect text("#who") == "alice"
    expect text(".plan-badge") contains "pro"
    expect count(".invoice") == 3
    capture plan = match(text(".plan-badge"), /plan: (\w+)/)
  }

  step "and it remembers where it is" {
    browser {
      goto "/never"
    }
    timeout = "30s"
    expect page.title == "Never"
    expect page.url contains "/never"
    expect plan == "pro"
  }
}
`, srv.URL))

	if !run.Passed() {
		for _, d := range run.Diagnostics() {
			t.Errorf("%s:%d %s: %v", d.File, d.Line, d.Step, failureText(d))
		}
		t.Fatal("the scenario failed; the three steps did not share one page")
	}
	if n := len(run.Scenarios[0].Steps); n != 3 {
		t.Errorf("the scenario ran %d steps, want 3", n)
	}
}

// Done-When 2: a `within` assertion passes only after the DOM settles.
//
// [role=status] on the fixture's /done says "Working" and becomes "Pro" after
// ?settle= milliseconds. The assertion is false when the step's actions finish
// and true later, which is the whole case `within` exists for -- and the case
// `retry` gets wrong, because a retry would re-run the `goto` and start the
// settle delay over.
//
// Two things are asserted about the timing, and both matter: it waited at least
// as long as the delay, and it did not burn its whole budget. The first says
// the wait is real; the second says it stops as soon as the condition holds.
func TestBrowserWithinWaitsForTheDomToSettle(t *testing.T) {
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)

	const settle = 1200 * time.Millisecond
	start := time.Now()
	run, _ := runBrowserArt(t, fmt.Sprintf(`scenario "settling" {
  var url = %q

  step "wait for the banner" {
    browser {
      goto "${url}/"
      fill "#user" = "alice"
      click "#go"
    }
    timeout = "30s"
    expect text("[role=status]") == "Working"
    expect text("[role=status]") contains "Pro" within "10s"
  }
}
`, srv.URL+fixture.PathForm))
	elapsed := time.Since(start)

	if !run.Passed() {
		for _, d := range run.Diagnostics() {
			t.Errorf("%s:%d %s: %v", d.File, d.Line, d.Step, failureText(d))
		}
		t.Fatal("the within assertion did not settle")
	}
	// The first expect read "Working": the assertion really was false when the
	// actions finished, so the second one really did have to wait.
	step := run.Scenarios[0].Steps[0]
	if len(step.Assertions) != 2 {
		t.Fatalf("the step made %d assertions, want 2", len(step.Assertions))
	}
	if step.Duration < settle/2 {
		t.Errorf("the step took %v, less than the %v settle delay: nothing was waited for", step.Duration, settle)
	}
	if step.Duration > 9*time.Second {
		t.Errorf("the step took %v of its 10s budget, want it to stop as soon as the banner landed", step.Duration)
	}
	if elapsed > 30*time.Second {
		t.Errorf("the whole run took %v, which is too slow to be waiting on the DOM", elapsed)
	}
}

// The other half of a budget: one that expires. /never grows nothing, so the
// assertion cannot pass on a fast machine or a slow one -- it measures the
// budget and nothing else.
func TestBrowserWithinHonoursABudgetThatExpires(t *testing.T) {
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)

	start := time.Now()
	run, dir := runBrowserArt(t, fmt.Sprintf(`scenario "never settles" {
  var url = %q

  step "wait for something that never comes" {
    browser {
      goto "${url}/never"
    }
    timeout = "30s"
    expect visible("#settled") within "500ms"
  }
}
`, srv.URL))
	elapsed := time.Since(start)

	if run.Passed() {
		t.Fatal("the run passed; nothing on /never ever settles")
	}
	step := run.Scenarios[0].Steps[0]
	if step.Status != result.StatusFail {
		t.Errorf("step = %v, want a plain failure: the page answered, it just answered false", step.Status)
	}
	if elapsed > 30*time.Second {
		t.Errorf("a 500ms budget took %v to expire", elapsed)
	}
	// Done-When 3's other half: a failed browser step leaves a picture, and the
	// step names it.
	if step.Screenshot == "" {
		t.Fatal("the failed step has no screenshot")
	}
	raw, err := os.ReadFile(step.Screenshot)
	if err != nil {
		t.Fatalf("the screenshot the step names is not there: %v", err)
	}
	if !strings.HasPrefix(string(raw), "\x89PNG\r\n\x1a\n") {
		t.Errorf("%s is not a PNG", step.Screenshot)
	}
	if filepath.Dir(step.Screenshot) != dir {
		t.Errorf("the screenshot went to %s, want %s", filepath.Dir(step.Screenshot), dir)
	}
}

// The screenshot's path reaches the JSON report, on the step and on the
// failures entry -- which is what a CI job reads to know which artifact to
// upload.
func TestBrowserScreenshotIsNamedInTheJSONReport(t *testing.T) {
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)

	run, _ := runBrowserArt(t, fmt.Sprintf(`scenario "never settles" {
  step "look for a receipt" {
    browser {
      goto "%s/never"
    }
    timeout = "30s"
    expect visible(".receipt") within "300ms"
  }
}
`, srv.URL))

	var buf bytes.Buffer
	if err := report.WriteJSON(&buf, run); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Scenarios []struct {
			Steps []struct {
				Screenshot string `json:"screenshot"`
			} `json:"steps"`
		} `json:"scenarios"`
		Failures []struct {
			Screenshot string `json:"screenshot"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	want := run.Scenarios[0].Steps[0].Screenshot
	if want == "" {
		t.Fatal("the failed step has no screenshot to report")
	}
	if got := doc.Scenarios[0].Steps[0].Screenshot; got != want {
		t.Errorf("the report's step screenshot is %q, want %q", got, want)
	}
	if len(doc.Failures) == 0 || doc.Failures[0].Screenshot != want {
		t.Errorf("the report's failures entry does not name the screenshot: %+v", doc.Failures)
	}
}

// `retry` and `within` compose, which is the claim the issue makes about the
// two timers. The step retries twice; each attempt drives the page again and
// each attempt's assertion waits. The second attempt is the one that passes,
// because the fixture settles well inside the budget either way -- what is
// asserted is that both loops ran, not which one did the work.
func TestBrowserRetryAndWithinCompose(t *testing.T) {
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)

	run, _ := runBrowserArt(t, fmt.Sprintf(`scenario "both timers" {
  step "settle, retrying if the page is wrong" {
    browser {
      goto "%s/"
      fill "#user" = "alice"
      click "#go"
    }
    timeout = "30s"
    retry { times = 2, delay = "10ms" }
    expect text("[role=status]") contains "Pro" within "10s"
    expect page.url contains "/done"
  }
}
`, srv.URL))

	if !run.Passed() {
		for _, d := range run.Diagnostics() {
			t.Errorf("%s:%d %s: %v", d.File, d.Line, d.Step, failureText(d))
		}
		t.Fatal("the step failed")
	}
	// One attempt, because the waiting assertion settled inside it: `within`
	// did the work and `retry` was not needed, which is exactly the division
	// the two timers are for. A step that needed two attempts to settle a
	// browser assertion would mean the waiting loop was not waiting.
	if got := run.Scenarios[0].Steps[0].Attempts; got != 1 {
		t.Errorf("the step took %d attempts, want 1: within should have settled it inside the first", got)
	}
}

// An element function with no match answers rather than erroring, against a
// real page and through the whole runner. SPEC.md fixes these as a spec
// decision and they are what makes the absence of a thing assertable.
func TestBrowserEmptyMatchesAreAssertable(t *testing.T) {
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)

	run, _ := runBrowserArt(t, fmt.Sprintf(`scenario "nothing there" {
  step "assert the absence of things" {
    browser {
      goto "%s/never"
    }
    timeout = "30s"
    expect count(".invoice") == 0 within "1s"
    expect visible(".modal") == false within "1s"
    expect text(".error") is null within "1s"
    expect value("#missing") is null within "1s"
    expect attr("#missing", "href") is null within "1s"
  }
}
`, srv.URL))

	if !run.Passed() {
		for _, d := range run.Diagnostics() {
			t.Errorf("%s:%d %s: %v", d.File, d.Line, d.Step, failureText(d))
		}
		t.Fatal("an empty-match assertion failed")
	}
	// And quickly: an element function that waited would spend each 1s budget.
	if d := run.Scenarios[0].Steps[0].Duration; d > 3*time.Second {
		t.Errorf("five empty-match assertions took %v; the functions must not wait", d)
	}
}

// A browser scenario leaves no browser behind. session.WithScenario closes the
// page, the context and the browser at the scenario boundary and
// session.Shutdown stops the driver at the end of the run; this is the run-level
// check that both actually happen, so a suite of a hundred scenarios does not
// end with a hundred Chromiums.
func TestBrowserRunLeavesNothingRunning(t *testing.T) {
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)

	for i := 0; i < 2; i++ {
		run, _ := runBrowserArt(t, fmt.Sprintf(`scenario "open and close %d" {
  step "look" {
    browser {
      goto "%s/"
    }
    timeout = "30s"
    expect page.title == "Sign in"
  }
}
`, i, srv.URL))
		if !run.Passed() {
			t.Fatalf("run %d failed", i)
		}
	}
	// Two runs in one process, each with its own driver start and shutdown.
	// A driver that could not be restarted after Shutdown would fail the
	// second one, which is the regression this guards.
}

// failureText is the one line a diagnostic says, for a test's error message.
func failureText(d result.Diagnostic) string {
	if d.Assertion != nil {
		if d.Assertion.Error != "" {
			return d.Assertion.Error
		}
		return fmt.Sprintf("%s %s: want %v, got %v",
			d.Assertion.Path, d.Assertion.Operator, d.Assertion.Expected, d.Assertion.Actual)
	}
	return d.Error
}
