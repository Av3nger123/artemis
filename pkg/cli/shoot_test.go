package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/steps/browserstep"
)

// A browser step that did not pass leaves a picture, and the step carries its
// path so the report can name it.
func TestAFailedBrowserStepIsPhotographed(t *testing.T) {
	dir := t.TempDir()
	page := &settlingPage{url: "http://127.0.0.1:1/done", title: "Welcome"}
	ctx := executor.WithSessions(context.Background(), fakeSessions{page: page})
	rt := &runtimeEnv{reg: executor.Default(), shots: browserstep.NewShots(dir)}

	st := oneStep(t, `scenario "upgrade to pro" {
  step "check the receipt" {
    browser { goto "http://127.0.0.1:1/" }
    expect visible(".receipt")
  }
}`)
	step := &result.StepResult{Name: st.Name, Status: result.StatusFail}

	shoot(ctx, rt, "upgrade to pro", st, step)

	want := filepath.Join(dir, "upgrade-to-pro-check-the-receipt.png")
	if step.Screenshot != want {
		t.Errorf("step.Screenshot = %q, want %q", step.Screenshot, want)
	}
	if len(page.shots) != 1 || page.shots[0] != want {
		t.Errorf("the page was asked for %v, want one shot at %q", page.shots, want)
	}
}

// Nothing is photographed for a step of another type. An api step has no page,
// and asking for one would be an error rather than a picture.
func TestAnApiStepIsNotPhotographed(t *testing.T) {
	dir := t.TempDir()
	page := &settlingPage{url: "http://127.0.0.1:1/"}
	ctx := executor.WithSessions(context.Background(), fakeSessions{page: page})
	rt := &runtimeEnv{reg: executor.Default(), shots: browserstep.NewShots(dir)}

	st := oneStep(t, `scenario "s" {
  step "ping" {
    get "http://127.0.0.1:1/x"
    expect status == 200
  }
}`)
	step := &result.StepResult{Name: st.Name, Status: result.StatusFail}

	shoot(ctx, rt, "s", st, step)

	if step.Screenshot != "" {
		t.Errorf("an api step got a screenshot at %q", step.Screenshot)
	}
	if len(page.shots) != 0 {
		t.Errorf("the page was asked for %v for an api step", page.shots)
	}
	// And no directory: a suite of api steps must not grow a folder in
	// someone's working directory.
	if entries, err := os.ReadDir(dir); err == nil && len(entries) != 0 {
		t.Errorf("the screenshot directory holds %d entries after an api-only failure", len(entries))
	}
}

// A run with screenshots turned off takes none, and the same call is what a
// caller makes either way.
func TestScreenshotsOffTakesNone(t *testing.T) {
	page := &settlingPage{url: "http://127.0.0.1:1/"}
	ctx := executor.WithSessions(context.Background(), fakeSessions{page: page})

	st := oneStep(t, `scenario "s" {
  step "x" {
    browser { goto "http://127.0.0.1:1/" }
    expect page.title == "nope"
  }
}`)

	for _, rt := range []*runtimeEnv{
		{reg: executor.Default(), shots: browserstep.NewShots("")},
		{reg: executor.Default()}, // nil shots, which is what runFiles builds
	} {
		step := &result.StepResult{Name: st.Name, Status: result.StatusFail}
		shoot(ctx, rt, "s", st, step)
		if step.Screenshot != "" {
			t.Errorf("a screenshot was taken at %q with screenshots off", step.Screenshot)
		}
	}
	if len(page.shots) != 0 {
		t.Errorf("the page was asked for %v with screenshots off", page.shots)
	}
}

// A browser step with no session -- the session never opened, which is itself
// what failed the step -- gets no screenshot and no crash.
func TestAStepWithNoSessionIsNotPhotographed(t *testing.T) {
	rt := &runtimeEnv{reg: executor.Default(), shots: browserstep.NewShots(t.TempDir())}
	st := oneStep(t, `scenario "s" {
  step "x" {
    browser { goto "http://127.0.0.1:1/" }
    expect page.title == "nope"
  }
}`)
	step := &result.StepResult{Name: st.Name, Status: result.StatusFail}

	shoot(context.Background(), rt, "s", st, step)

	if step.Screenshot != "" {
		t.Errorf("step.Screenshot = %q, want none", step.Screenshot)
	}
}

// The path reaches the JSON report at both levels: on the step, and on the
// failures entry -- which has to stand alone, so something deciding what to go
// and fix does not have to walk the tree to find the picture.
func TestTheScreenshotPathReachesTheJSONReport(t *testing.T) {
	run := result.NewRun()
	sc := run.NewScenario("upgrade to pro", "upgrade.art")
	step := sc.NewStep("check the receipt")
	step.Line = 7
	step.Screenshot = filepath.Join("artemis-screenshots", "upgrade-to-pro-check-the-receipt.png")
	step.Assert(result.Assertion{
		Kind: "expect", Path: `visible(".receipt")`, Operator: "==",
		Expected: true, Actual: false, Line: 9,
	}.Fail())
	step.Finish(time.Second)
	sc.Finish(time.Second)
	run.Finish()

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

	if got := doc.Scenarios[0].Steps[0].Screenshot; got != step.Screenshot {
		t.Errorf("the step's screenshot is %q, want %q", got, step.Screenshot)
	}
	if len(doc.Failures) != 1 {
		t.Fatalf("the document holds %d failures, want 1", len(doc.Failures))
	}
	if got := doc.Failures[0].Screenshot; got != step.Screenshot {
		t.Errorf("the failure's screenshot is %q, want %q", got, step.Screenshot)
	}
	// And the key is present-and-empty everywhere else, so a jq expression
	// never has to tell absent from empty.
	if !strings.Contains(buf.String(), `"screenshot"`) {
		t.Error("the document holds no screenshot key at all")
	}
}

// --screenshots is read off the command, so an explicit empty value turns them
// off and is distinguishable from not passing the flag.
func TestTheScreenshotsFlagIsReadOffTheCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, browserstep.DefaultDir},
		{[]string{"--screenshots", "shots"}, "shots"},
		{[]string{"--screenshots", ""}, ""},
		{[]string{"--screenshots=/tmp/artemis"}, "/tmp/artemis"},
	} {
		cmd := &cobraTestCmd{}
		cmd.flags().String(screenshotsFlag, browserstep.DefaultDir, "")
		if err := cmd.flags().Parse(tc.args); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := shotDir(cmd.cmd()); got != tc.want {
			t.Errorf("shotDir(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// A command with no such flag gets the default rather than an error: a missing
// flag is a wiring mistake, and failing a run over it would be the worst
// possible report of one.
func TestADirWithNoFlagFallsBackToTheDefault(t *testing.T) {
	cmd := &cobraTestCmd{}
	if got := shotDir(cmd.cmd()); got != browserstep.DefaultDir {
		t.Errorf("shotDir() = %q, want the %q default", got, browserstep.DefaultDir)
	}
}
