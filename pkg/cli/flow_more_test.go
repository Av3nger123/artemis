package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
)

// A use inside a block expands there: its step runs in the branch, and its
// capture is in scope after the block like any branch's.
func TestAUseInsideABlockExpandsThere(t *testing.T) {
	srv := authServer(t)
	dir := t.TempDir()
	auth, err := os.ReadFile("testdata/collections/auth.art")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "auth.art"), auth, 0o600)
	path := filepath.Join(dir, "s.art")
	writeSplit(t, path, fmt.Sprintf(`import "auth.art"

flow uses_in_blocks {
  var url = %q

  parallel {
    step auth_login as "auth.login" = auth.login { user = "alice", password = "s3cret", base = url }
    step missing {
      get "${url}/nowhere"
      expect status == 404
    }
  }

  if token == "t" {
    step orders {
      get "${url}/orders" { header "Authorization" = "Bearer ${token}" }
      expect status == 200
    }
  }
}

scenario uses_in_blocks as "uses in blocks" {}
`, srv.URL))
	run := runFiles(executor.Default(), []string{path}, report.Discard(), io.Discard)
	if !run.Passed() {
		t.Fatalf("run failed:\n%s", describeRun(run))
	}
	if got := steps(run); got["auth.login"] == nil || got["orders"].Status != result.StatusPass {
		t.Errorf("steps = %v, want auth.login run in the branch and orders after it", names(run))
	}
}

// Blocks in setup and teardown run there: a setup block's failure skips the
// scenario's own steps, naming the step, and a teardown block still runs.
func TestBlocksInSetupAndTeardown(t *testing.T) {
	srv, asked := flowServer(t)
	run, _ := runArt(t, fmt.Sprintf(`flow phases {
  var url = %q

  setup {
    parallel {
      step warm { get "${url}/warm" }
      step broken {
        get "${url}/fail"
        expect status == 200
      }
    }
  }

  step own { get "${url}/own" }

  teardown {
    if 1 == 1 {
      step clean { get "${url}/clean" }
    }
  }
}

scenario phases {}
`, srv.URL))
	got := steps(run)
	if got["own"].Status != result.StatusSkip || got["own"].Error != `setup step "broken" did not pass` {
		t.Errorf("own = %s %q, want skipped naming the setup step", got["own"].Status, got["own"].Error)
	}
	if got["clean"].Status != result.StatusPass || got["clean"].Phase != result.PhaseTeardown {
		t.Errorf("clean = %s phase %q, want a passing teardown step", got["clean"].Status, got["clean"].Phase)
	}
	if _, hit := asked.Load("/own"); hit {
		t.Error("a skipped step sent its request")
	}
}

// Blocks nest at run time: a repeat and an if inside one parallel block.
func TestNestedBlocksRun(t *testing.T) {
	srv, _ := flowServer(t)
	run, diags := runArt(t, fmt.Sprintf(`flow nested {
  var url = %q

  parallel {
    repeat every "5ms" max 10 {
      step poll {
        get "${url}/job"
        var state = body.state
      }
    } until state == "done"
    if 2 > 1 {
      step side { get "${url}/side" }
    }
  }

  step after {
    get "${url}/after/${state}"
    expect body.path == "/after/done"
  }
}

scenario nested {}
`, srv.URL))
	if !run.Passed() {
		t.Fatalf("run failed:\n%s%s", diags, describeRun(run))
	}
	if got := strings.Join(names(run), ","); got != "poll,side,after" {
		t.Errorf("report order = %s, want poll,side,after", got)
	}
}

// A condition that cannot be evaluated is an errored entry where the block
// is, and every branch is skipped.
func TestAConditionThatWillNotEvaluate(t *testing.T) {
	srv, asked := flowServer(t)
	run, _ := runArt(t, fmt.Sprintf(`flow bad_conditions {
  var url = %q

  if 1 {
    step never { get "${url}/never" }
  }

  switch number("abc") {
    case 1 {
      step nor { get "${url}/nor" }
    }
  }
}

scenario bad_conditions as "bad conditions" {}
`, srv.URL))
	sc := run.Scenarios[0]
	var errs []string
	for _, st := range sc.Steps {
		if st.Status == result.StatusError {
			errs = append(errs, st.Name+": "+st.Error)
		}
	}
	if len(errs) != 2 || !strings.Contains(errs[0], "is 1, want true or false") || !strings.HasPrefix(errs[1], "switch at line 8") {
		t.Errorf("errored entries = %q, want the if's and the switch's", errs)
	}
	for _, p := range []string{"/never", "/nor"} {
		if _, hit := asked.Load(p); hit {
			t.Errorf("%s was requested from a branch that could not be chosen", p)
		}
	}
}

// A branch that fails does not stop its siblings or the steps after the
// block, and fails the scenario.
func TestAFailingParallelBranch(t *testing.T) {
	srv, asked := flowServer(t)
	run, _ := runArt(t, fmt.Sprintf(`flow one_branch_fails {
  var url = %q

  parallel {
    step broken {
      get "${url}/fail"
      expect status == 200
    }
    step fine { get "${url}/slow/fine" }
  }

  step after { get "${url}/after" }
}

scenario one_branch_fails as "one branch fails" {}
`, srv.URL))
	if run.Passed() {
		t.Fatal("a scenario with a failing branch passed")
	}
	for _, p := range []string{"/slow/fine", "/after"} {
		if _, hit := asked.Load(p); !hit {
			t.Errorf("%s was not requested", p)
		}
	}
}

// within bounds a repeat in time, and the error names it.
func TestARepeatBoundedInTime(t *testing.T) {
	srv, _ := flowServer(t)
	run, _ := runArt(t, fmt.Sprintf(`flow times_out {
  var url = %q

  repeat every "30ms" within "100ms" {
    step status {
      get "${url}/job"
      var state = body.state
    }
  } until state == "never"
}

scenario times_out as "times out" {}
`, srv.URL))
	sc := run.Scenarios[0]
	last := sc.Steps[len(sc.Steps)-1]
	if last.Status != result.StatusError || !strings.Contains(last.Error, "never held within 100ms") {
		t.Errorf("last = %s %q, want an error naming within 100ms", last.Status, last.Error)
	}
}

// The --events stream carries every step once, in report order, with the
// indexes counting up -- parallel branches and a repeat's last round included.
func TestEventsOfBlocksAreInReportOrder(t *testing.T) {
	srv, _ := flowServer(t)
	path := writeArt(t, "events.art", fmt.Sprintf(`flow events {
  var url = %q

  parallel {
    step a { get "${url}/slow/a" }
    step b { get "${url}/b" }
  }

  repeat every "5ms" max 5 {
    step poll {
      get "${url}/job"
      var state = body.state
    }
  } until state == "done"

  step c { get "${url}/c" }
}

scenario events {}
`, srv.URL))
	var started, ended []string
	var indexes []float64
	for _, e := range runArtEvents(t, path) {
		switch e["event"] {
		case "step-start":
			started = append(started, e["step"].(string))
			indexes = append(indexes, e["index"].(float64))
		case "step-end":
			ended = append(ended, e["result"].(map[string]any)["name"].(string))
		}
	}
	if got := strings.Join(started, ","); got != "a,b,poll,c" {
		t.Errorf("step-start order = %s, want a,b,poll,c", got)
	}
	if strings.Join(ended, ",") != strings.Join(started, ",") {
		t.Errorf("step-end order = %v, want it to match step-start's", ended)
	}
	for i, idx := range indexes {
		if int(idx) != i {
			t.Errorf("indexes = %v, want 0 counting up", indexes)
			break
		}
	}
}

// The JSON report says which round a repeat's step is from.
func TestTheJSONReportCarriesTheRound(t *testing.T) {
	srv, _ := flowServer(t)
	path := writeArt(t, "round.art", fmt.Sprintf(`flow round {
  var url = %q

  repeat every "5ms" max 5 {
    step poll {
      get "${url}/job"
      var state = body.state
    }
  } until state == "done"

  step after { get "${url}/after" }
}

scenario round {}
`, srv.URL))
	out := filepath.Join(t.TempDir(), "run.json")
	if _, stderr, err := runCLI(t, "run", path, "--report", "json="+out); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	raw, _ := os.ReadFile(out) //nolint:gosec // a path this test chose
	var doc struct {
		Scenarios []struct {
			Steps []map[string]any `json:"steps"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	st := doc.Scenarios[0].Steps
	if st[0]["round"] != 3.0 {
		t.Errorf("poll's round = %v, want 3", st[0]["round"])
	}
	if _, has := st[1]["round"]; has {
		t.Errorf("a step outside a repeat carries a round: %v", st[1])
	}
}

// --seed draws the same random values in parallel branches on every run,
// whatever order the branches finished in.
func TestParallelBranchesDrawReproducibly(t *testing.T) {
	drawn := func() []string {
		var mu sync.Mutex
		var paths []string
		srv, _ := flowServer(t)
		_ = srv
		path := writeArt(t, "seed.art", fmt.Sprintf(`flow seeded {
  var url = %q

  parallel {
    step a { get "${url}/a/${random_string(12)}" }
    step b { get "${url}/b/${random_string(12)}" }
  }
}

scenario seeded {}
`, srv.URL))
		out := filepath.Join(t.TempDir(), "run.json")
		if _, stderr, err := runCLI(t, "run", path, "--seed", "42", "--trace", filepath.Dir(out)); err != nil {
			t.Fatalf("run: %v\n%s", err, stderr)
		}
		entries, _ := os.ReadDir(filepath.Dir(out))
		for _, e := range entries {
			rec := readTrace(t, filepath.Join(filepath.Dir(out), e.Name()))
			mu.Lock()
			paths = append(paths, rec.Sent["url"].(string)[len(srv.URL):])
			mu.Unlock()
		}
		sort.Strings(paths)
		return paths
	}
	first, second := drawn(), drawn()
	if len(first) != 2 || strings.Join(first, " ") != strings.Join(second, " ") {
		t.Errorf("two runs with --seed 42 drew %v and %v", first, second)
	}
	if first[0][3:] == first[1][3:] {
		t.Errorf("the two branches drew the same value: %v", first)
	}
}

func names(run *result.RunResult) []string {
	var out []string
	for _, sc := range run.Scenarios {
		for _, st := range sc.Steps {
			out = append(out, st.Name)
		}
	}
	return out
}

// A step that calls an item inside setup: the var the item writes is bound
// for the steps after setup.
func TestAnItemCallInsideSetup(t *testing.T) {
	srv := authServer(t)
	dir := t.TempDir()
	auth, err := os.ReadFile("testdata/collections/auth.art")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "auth.art"), auth, 0o600)
	path := filepath.Join(dir, "s.art")
	writeSplit(t, path, fmt.Sprintf(`import "auth.art"

flow setup_logs_in {
  var url = %q

  setup {
    step auth_login as "auth.login" = auth.login { user = "alice", password = "s3cret", base = url }
  }

  step orders {
    get "${url}/orders" { header "Authorization" = "Bearer ${token}" }
    expect status == 200
  }
}

scenario setup_logs_in as "setup logs in" {}
`, srv.URL))
	run := runFiles(executor.Default(), []string{path}, report.Discard(), io.Discard)
	if !run.Passed() {
		t.Fatalf("run failed:\n%s", describeRun(run))
	}
}
