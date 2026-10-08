package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
)

// manyFiles writes n scenario files, each with one step against url, named so
// discovery order is 01, 02, ... and a reader can see the order in the output.
func manyFiles(t *testing.T, url string, n int) string {
	t.Helper()
	files := map[string]string{}
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("%02d_file.art", i)
		files[name] = fmt.Sprintf(`scenario "scenario %02d" {
  step "ping" {
    get %q
    expect status == 200
  }
}`, i, url)
	}
	return writeTree(t, t.TempDir(), files)
}

func runWithJobs(t *testing.T, root string, jobs int) (*result.RunResult, string) {
	t.Helper()
	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	var out bytes.Buffer
	run := runFilesWith(&runtimeEnv{
		reg:  executor.Default(),
		jobs: jobs,
	}, files, report.NewConsole(&out), io.Discard)
	return run, out.String()
}

// The property everything else depends on: a parallel run reports scenarios in
// discovery order, not completion order. The server answers the *later* files
// faster, so a run that recorded completion order would come out reversed.
func TestJobsKeepsFileOrder(t *testing.T) {
	initLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 01 is slowest, 06 is fastest.
		if n := strings.TrimPrefix(r.URL.Path, "/"); n != "" {
			if d, err := time.ParseDuration(n + "ms"); err == nil {
				time.Sleep(d)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer srv.Close()

	files := map[string]string{}
	for i := 1; i <= 6; i++ {
		files[fmt.Sprintf("%02d_file.art", i)] = fmt.Sprintf(`scenario "scenario %02d" {
  step "ping" {
    get "%s/%d"
    expect status == 200
  }
}`, i, srv.URL, (7-i)*20)
	}
	root := writeTree(t, t.TempDir(), files)

	run, out := runWithJobs(t, root, 6)
	if !run.Passed() {
		t.Fatalf("run did not pass: %s", runFailedError(run))
	}
	if len(run.Scenarios) != 6 {
		t.Fatalf("recorded %d scenarios, want 6", len(run.Scenarios))
	}
	for i, sc := range run.Scenarios {
		if want := fmt.Sprintf("scenario %02d", i+1); sc.Name != want {
			t.Errorf("scenarios[%d] = %q, want %q: file order was not kept", i, sc.Name, want)
		}
	}
	// And the console agrees: each scenario's block appears in the same order.
	var at []int
	for i := 1; i <= 6; i++ {
		at = append(at, strings.Index(out, fmt.Sprintf("scenario %02d", i)))
	}
	for i := 1; i < len(at); i++ {
		if at[i] < at[i-1] {
			t.Errorf("the console printed scenario %02d before %02d:\n%s", i+1, i, out)
		}
	}
}

// mustRun is the console output of a passing run at this many jobs.
func mustRun(t *testing.T, root string, jobs int) string {
	t.Helper()
	run, out := runWithJobs(t, root, jobs)
	if !run.Passed() {
		t.Fatalf("run at %d jobs did not pass: %s", jobs, runFailedError(run))
	}
	return out
}

// One job and many jobs must produce the same lines in the same order, which is
// what makes the flag safe to turn on: the goldens are written at one job.
func TestJobsProducesTheSameOutputAsOne(t *testing.T) {
	initLog(t)
	srv := okServer(t, 200, `{"status":"ok"}`)
	root := manyFiles(t, srv.URL, 5)

	// Durations differ between any two runs, so they are scrubbed with the same
	// pattern the goldens use. What is being compared is the lines and their
	// order, which is the thing --jobs could get wrong.
	serial := spacedDur.ReplaceAllString(mustRun(t, root, 1), " "+durSentinel)
	parallel := spacedDur.ReplaceAllString(mustRun(t, root, 4), " "+durSentinel)

	if serial != parallel {
		t.Errorf("--jobs 4 printed something else:\n--- 1 job ---\n%s\n--- 4 jobs ---\n%s", serial, parallel)
	}
}

// A file that will not compile keeps its place among files that ran, which is
// the ordering the compile pass exists to preserve.
func TestJobsKeepsAFileThatWillNotCompileInPlace(t *testing.T) {
	initLog(t)
	srv := okServer(t, 200, `{"status":"ok"}`)
	root := writeTree(t, t.TempDir(), map[string]string{
		"01_ok.art":     namedScenarioArt("first", srv.URL, "ok"),
		"02_broken.art": "scenario \"x\" {\n",
		"03_ok.art":     namedScenarioArt("third", srv.URL, "ok"),
	})
	run, _ := runWithJobs(t, root, 3)

	if len(run.Scenarios) != 3 {
		t.Fatalf("recorded %d scenarios, want 3", len(run.Scenarios))
	}
	if run.Scenarios[0].Name != "first" || run.Scenarios[2].Name != "third" {
		t.Errorf("names = %q, %q, %q", run.Scenarios[0].Name, run.Scenarios[1].Name, run.Scenarios[2].Name)
	}
	if run.Scenarios[1].Passed() {
		t.Error("the broken file in the middle is not an errored scenario")
	}
}

// The bound is real: no more than --jobs files are in flight at once.
func TestJobsRespectsTheBound(t *testing.T) {
	initLog(t)
	var inFlight, peak int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&inFlight, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if n <= old || atomic.CompareAndSwapInt64(&peak, old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt64(&inFlight, -1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer srv.Close()

	root := manyFiles(t, srv.URL, 12)
	run, _ := runWithJobs(t, root, 3)
	if !run.Passed() {
		t.Fatalf("run did not pass: %s", runFailedError(run))
	}
	if got := atomic.LoadInt64(&peak); got > 3 {
		t.Errorf("%d requests were in flight at once, want at most 3", got)
	}
}

// Zero and one both mean one, so a caller that never set the field gets today's
// behaviour.
func TestJobsZeroMeansOne(t *testing.T) {
	for _, n := range []int{0, -4, 1} {
		rt := &runtimeEnv{jobs: n}
		if got := rt.workers(); got != 1 {
			t.Errorf("workers() with jobs=%d = %d, want 1", n, got)
		}
	}
	var nilRT *runtimeEnv
	if got := nilRT.workers(); got != 1 {
		t.Errorf("workers() on nil = %d, want 1", got)
	}
}
