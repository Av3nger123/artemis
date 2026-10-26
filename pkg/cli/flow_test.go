package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"artemis/pkg/result"
)

// flowServer answers the paths the flow tests ask for: /slow/<x> after 200ms,
// /job a state that is "pending" until the third ask, /fail a 500, and
// anything else a JSON echo of its path.
func flowServer(t *testing.T) (*httptest.Server, *sync.Map) {
	t.Helper()
	asked := &sync.Map{}
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Store(r.URL.Path, true)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/slow/"):
			time.Sleep(200 * time.Millisecond)
			fmt.Fprintf(w, `{"id": %q}`, strings.TrimPrefix(r.URL.Path, "/slow/"))
		case r.URL.Path == "/job":
			state := "pending"
			if polls.Add(1) >= 3 {
				state = "done"
			}
			fmt.Fprintf(w, `{"state": %q}`, state)
		case r.URL.Path == "/fail":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			fmt.Fprintf(w, `{"path": %q, "tier": "gold"}`, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, asked
}

func steps(run *result.RunResult) map[string]*result.StepResult {
	out := map[string]*result.StepResult{}
	for _, sc := range run.Scenarios {
		for _, st := range sc.Steps {
			out[st.Name] = st
		}
	}
	return out
}

func TestParallelBranchesRunAtTheSameTime(t *testing.T) {
	srv, _ := flowServer(t)
	start := time.Now()
	run, diags := runArt(t, fmt.Sprintf(`scenario "parallel" {
  var url = %q

  parallel {
    step "a" {
      get "${url}/slow/a"
      capture a = body.id
    }
    step "b" {
      get "${url}/slow/b"
      capture b = body.id
    }
    step "c" {
      get "${url}/slow/c"
    }
  }

  step "after" {
    get "${url}/joined/${a}/${b}"
    expect body.path == "/joined/a/b"
  }
}
`, srv.URL))
	took := time.Since(start)
	if !run.Passed() {
		t.Fatalf("run failed:\n%s%s", diags, describeRun(run))
	}
	if took > 500*time.Millisecond {
		t.Errorf("three 200ms branches took %v; they did not run at once", took)
	}
	var names []string
	for _, st := range run.Scenarios[0].Steps {
		names = append(names, st.Name)
	}
	if got := strings.Join(names, ","); got != "a,b,c,after" {
		t.Errorf("report order = %s, want source order a,b,c,after", got)
	}
}

func TestIfAndSwitchRunOneBranch(t *testing.T) {
	srv, asked := flowServer(t)
	run, diags := runArt(t, fmt.Sprintf(`scenario "choose" {
  var url = %q

  step "profile" {
    get "${url}/me"
    capture tier = body.tier
  }

  if tier == "silver" {
    step "silver" { get "${url}/silver" }
  } else if tier == "gold" {
    step "gold" { get "${url}/gold" }
  } else {
    step "other" { get "${url}/other" }
  }

  switch tier {
    case "bronze" {
      step "bronze" { get "${url}/bronze" }
    }
    default {
      step "fallback" { get "${url}/fallback" }
    }
  }
}
`, srv.URL))
	if !run.Passed() {
		t.Fatalf("run failed:\n%s%s", diags, describeRun(run))
	}
	got := steps(run)
	for _, name := range []string{"gold", "fallback"} {
		if got[name].Status != result.StatusPass {
			t.Errorf("%s: status %s, want it run", name, got[name].Status)
		}
	}
	for _, name := range []string{"silver", "other", "bronze"} {
		if got[name].Status != result.StatusSkip || !strings.Contains(got[name].Error, "not taken") {
			t.Errorf("%s: status %s %q, want skipped as not taken", name, got[name].Status, got[name].Error)
		}
		if _, hit := asked.Load("/" + name); hit {
			t.Errorf("%s was requested", name)
		}
	}
}

// A catch runs when the try failed and the finally always; the step that
// failed still fails the scenario.
func TestTryCatchFinally(t *testing.T) {
	srv, asked := flowServer(t)
	run, _ := runArt(t, fmt.Sprintf(`scenario "try" {
  var url = %q

  try {
    step "pay" {
      get "${url}/fail"
      expect status == 200
    }
    step "after pay" { get "${url}/after" }
  } catch {
    step "refund" { get "${url}/refund" }
  } finally {
    step "unlock" { get "${url}/unlock" }
  }
}
`, srv.URL))
	if run.Passed() {
		t.Fatal("a scenario whose try failed passed: a catch is not a cure")
	}
	got := steps(run)
	if got["after pay"].Status != result.StatusSkip {
		t.Errorf("after pay: %s, want skipped once pay failed", got["after pay"].Status)
	}
	for _, path := range []string{"/refund", "/unlock"} {
		if _, hit := asked.Load(path); !hit {
			t.Errorf("%s was not requested", path)
		}
	}
}

func TestRepeatPollsUntilItHolds(t *testing.T) {
	srv, _ := flowServer(t)
	run, diags := runArt(t, fmt.Sprintf(`scenario "poll" {
  var url = %q

  repeat every "10ms" max 10 {
    step "status" {
      get "${url}/job"
      capture state = body.state
    }
  } until state == "done"

  step "result" {
    get "${url}/result"
    expect state == "done"
  }
}
`, srv.URL))
	if !run.Passed() {
		t.Fatalf("run failed:\n%s%s", diags, describeRun(run))
	}
	sc := run.Scenarios[0]
	if len(sc.Steps) != 2 || sc.Steps[0].Round != 3 {
		t.Errorf("steps = %d, first round %d; want the last round's step, round 3, and result", len(sc.Steps), sc.Steps[0].Round)
	}
}

func TestARepeatThatRunsOutNamesTheBoundAndTheLastValue(t *testing.T) {
	srv, _ := flowServer(t)
	run, _ := runArt(t, fmt.Sprintf(`scenario "poll" {
  var url = %q

  repeat max 2 {
    step "status" {
      get "${url}/job"
      capture state = body.state
    }
  } until state == "done"
}
`, srv.URL))
	if run.Passed() {
		t.Fatal("a repeat that never held passed")
	}
	sc := run.Scenarios[0]
	last := sc.Steps[len(sc.Steps)-1]
	want := `until state == "done" never held within 2 rounds; state was "pending"`
	if last.Status != result.StatusError || last.Error != want {
		t.Errorf("last step = %s %q, want an error %q", last.Status, last.Error, want)
	}
}
