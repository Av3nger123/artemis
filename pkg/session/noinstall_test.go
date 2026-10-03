package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

// An api or terminal user pays nothing for a browser they never asked for. The
// design makes that a promise -- "the Playwright driver is fetched only when a
// scenario actually contains a browser step" -- and this is the test that keeps
// it, because the way to break it is a refactor that moves Install somewhere
// eager and nothing else notices.
//
// Unlike the browser-tagged test in this package, this one runs everywhere: its
// whole assertion is that *nothing happened*, so it needs no browser and no
// network.
func TestAPIOnlyRunTouchesNoDriver(t *testing.T) {
	driverDir, browsersDir := isolatePlaywright(t)

	reg := executor.NewRegistry()
	ran := false
	reg.Register("api", executor.Func(func(ctx context.Context, step models.Step, scope executor.Scope) (*result.StepResult, error) {
		ran = true
		// What an api step does with the context: reads the deadline, ignores
		// the registry. It never calls executor.SessionsOf.
		if _, hasDeadline := ctx.Deadline(); hasDeadline {
			t.Log("the step saw a deadline, as an http step would")
		}
		return &result.StepResult{}, nil
	}))

	err := WithScenario(context.Background(), Config{Headless: true, Viewport: "1280x720"},
		func(ctx context.Context) error {
			_, err := executor.Run(ctx, reg, models.Step{Name: "login", Type: "api"}, executor.NewScope())
			return err
		})
	if err != nil {
		t.Fatalf("the api-only scenario: %v", err)
	}
	if !ran {
		t.Fatal("the step did not run, so this proves nothing")
	}

	assertEmpty(t, driverDir, "the playwright driver directory")
	assertEmpty(t, browsersDir, "the playwright browsers directory")
	assertNoProcessUnder(t, browsersDir)
}

// Shutdown is deferred by the runner for every run, including the ones with no
// browser in them. On those it must do nothing at all.
func TestShutdownWithoutASessionDoesNothing(t *testing.T) {
	driverDir, browsersDir := isolatePlaywright(t)

	if err := Shutdown(); err != nil {
		t.Fatalf("Shutdown with no driver started: %v", err)
	}
	assertEmpty(t, driverDir, "the playwright driver directory")
	assertEmpty(t, browsersDir, "the playwright browsers directory")
}

// isolatePlaywright points playwright-go's driver and browser caches at empty
// temporary directories, so "nothing was downloaded" is a statement about this
// test rather than about whatever the machine happened to have already.
//
// These are playwright's own variables rather than artemis ones on purpose:
// they are what a CI image author already reaches for, and a second name for
// the same fact is a second thing to keep in step.
func isolatePlaywright(t *testing.T) (driverDir, browsersDir string) {
	t.Helper()
	driverDir = filepath.Join(t.TempDir(), "driver")
	browsersDir = filepath.Join(t.TempDir(), "browsers")
	if err := os.MkdirAll(driverDir, 0o755); err != nil {
		t.Fatalf("making the driver directory: %v", err)
	}
	if err := os.MkdirAll(browsersDir, 0o755); err != nil {
		t.Fatalf("making the browsers directory: %v", err)
	}
	t.Setenv("PLAYWRIGHT_DRIVER_PATH", driverDir)
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", browsersDir)
	return driverDir, browsersDir
}

func assertEmpty(t *testing.T, dir, what string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", what, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("%s is not empty: %s -- something downloaded a driver for a run with no browser step in it",
			what, strings.Join(names, ", "))
	}
}

// assertNoProcessUnder fails if any running process names dir in its command
// line. dir is a per-test temporary path, so a match can only be a browser this
// test caused -- which is the point: an empty directory would also be the
// symptom of a browser that launched from somewhere else.
//
// pgrep is a POSIX nicety rather than a guarantee; where it is missing the
// directory check above still stands on its own.
func assertNoProcessUnder(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Logf("pgrep is not on the PATH; the empty-directory check is the whole assertion here")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "pgrep", "-f", dir).Output()
	if err != nil {
		// pgrep exits 1 when it matches nothing, which is the passing case.
		return
	}
	if pids := strings.Fields(string(out)); len(pids) > 0 {
		t.Fatalf("processes still running under %s: %s", dir, strings.Join(pids, " "))
	}
}
