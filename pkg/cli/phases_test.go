package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// phaseServer creates carts and orders and records every request it sees, so a
// test can read what ran and in what order. A path in fail answers 500.
type phaseServer struct {
	mu   sync.Mutex
	seen []string
}

func newPhaseServer(t *testing.T, fail ...string) (*httptest.Server, *phaseServer) {
	t.Helper()
	ps := &phaseServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		ps.seen = append(ps.seen, r.Method+" "+r.URL.Path)
		ps.mu.Unlock()
		for _, f := range fail {
			if r.URL.Path == f {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id": "x1"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, ps
}

func (ps *phaseServer) requests() string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return strings.Join(ps.seen, ", ")
}

func checkoutArt(url string) string {
	return fmt.Sprintf(`scenario "checkout" {
  var url = %q

  setup {
    step "make a cart" {
      post "${url}/carts"
      expect status == 200
      capture cart_id = body.id
    }
  }

  step "pay" {
    post "${url}/pay"
    expect status == 200
    capture order_id = body.id
  }

  teardown {
    step "cancel the order" {
      delete "${url}/orders/${order_id}"
    }
    step "drop the cart" {
      delete "${url}/carts/${cart_id}"
    }
  }
}
`, url)
}

type phasedStep struct {
	Name   string `json:"name"`
	Phase  string `json:"phase"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

func phasedSteps(t *testing.T, stdout string) []phasedStep {
	t.Helper()
	var doc struct {
		Scenarios []struct {
			Steps []phasedStep `json:"steps"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("report: %v\n%s", err, stdout)
	}
	return doc.Scenarios[0].Steps
}

// Setup, the scenario's steps and teardown run in that order, and the report
// says which block each step was in.
func TestPhasesRunInOrder(t *testing.T) {
	srv, ps := newPhaseServer(t)
	stdout, _, err := runStreams(t, checkoutArt(srv.URL), "--report", "json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := ps.requests(); got != "POST /carts, POST /pay, DELETE /orders/x1, DELETE /carts/x1" {
		t.Errorf("requests = %s", got)
	}
	steps := phasedSteps(t, stdout)
	want := []string{"setup", "", "teardown", "teardown"}
	for i, s := range steps {
		if s.Phase != want[i] || s.Status != "pass" {
			t.Errorf("step %d = %+v, want phase %q and a pass", i, s, want[i])
		}
	}
}

// A setup step that fails skips the scenario's own steps -- naming it -- and
// teardown still runs, skipping the step whose capture never happened.
func TestAFailedSetupSkipsTheStepsAndNotTheTeardown(t *testing.T) {
	srv, ps := newPhaseServer(t, "/carts")
	stdout, stderr, err := runStreams(t, checkoutArt(srv.URL), "--report", "json")
	if err == nil {
		t.Fatal("a run whose setup failed passed")
	}
	if got := ps.requests(); got != "POST /carts" {
		t.Errorf("requests = %s, want only the setup's", got)
	}
	steps := phasedSteps(t, stdout)
	// error rather than fail: a 500 has no body, so its capture errors too.
	if steps[0].Status == "pass" || steps[0].Status == "skip" {
		t.Errorf("the setup step = %+v", steps[0])
	}
	if steps[1].Status != "skip" || !strings.Contains(steps[1].Error, `setup step "make a cart" did not pass`) {
		t.Errorf("the scenario's step = %+v, want a skip naming the setup step", steps[1])
	}
	for _, s := range steps[2:] {
		if s.Status != "skip" || !strings.Contains(s.Error, "which no step before it captured") {
			t.Errorf("teardown step %+v, want a skip for a capture that never happened", s)
		}
	}
	if !strings.Contains(stderr, "setup: make a cart") {
		t.Errorf("the console does not mark the setup step:\n%s", stderr)
	}
}

// A failing step of the scenario's own does not stop teardown: the cart setup
// made is dropped, and the order pay never captured is skipped.
func TestTeardownRunsAfterAFailure(t *testing.T) {
	srv, ps := newPhaseServer(t, "/pay")
	stdout, _, err := runStreams(t, checkoutArt(srv.URL), "--report", "json")
	if err == nil {
		t.Fatal("a run whose step failed passed")
	}
	if got := ps.requests(); got != "POST /carts, POST /pay, DELETE /carts/x1" {
		t.Errorf("requests = %s", got)
	}
	steps := phasedSteps(t, stdout)
	if steps[2].Status != "skip" || steps[3].Status != "pass" {
		t.Errorf("teardown = %+v, %+v", steps[2], steps[3])
	}
}
