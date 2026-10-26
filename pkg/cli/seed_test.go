package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recorder is a server that answers status to everything and remembers the
// `id` query parameter of each request, which is where these tests put the
// values a scenario made up.
type recorder struct {
	mu  sync.Mutex
	ids []string
}

func recordingServer(t *testing.T, status int) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.ids = append(rec.ids, r.URL.Query().Get("id"))
		rec.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ids...)
}

func randomArt(url string) string {
	return fmt.Sprintf(`scenario "made up" {
  var id = uuid()

  step "send it" {
    get %q {
      query "id" = "${id}-${random_int(1, 1000000)}-${random_string(8)}"
    }
    expect status == 200
  }
}
`, url)
}

// A run that drew a random value and failed says which seed it drew from, in
// the report and on the console; one that drew nothing says nothing about a
// seed, because there is nothing to reproduce.
func TestASeedIsReportedOnlyWhenARunDrewOne(t *testing.T) {
	srv, _ := recordingServer(t, 500)

	stdout, stderr, err := runStreams(t, randomArt(srv.URL), "--report", "json", "--seed", "4242")
	if err == nil {
		t.Fatal("the run passed against a server that answers 500")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if doc["seed"] != float64(4242) {
		t.Errorf("seed = %v, want 4242", doc["seed"])
	}
	if !strings.Contains(stderr, "rerun with --seed 4242") {
		t.Errorf("the console does not say how to rerun:\n%s", stderr)
	}

	stdout, stderr, err = runStreams(t, namedScenarioArt("health", srv.URL, "ok"), "--report", "json", "--seed", "4242")
	if err == nil {
		t.Fatal("the run passed against a server that answers 500")
	}
	if strings.Contains(stdout, `"seed"`) {
		t.Errorf("a run that drew nothing reports a seed:\n%s", stdout)
	}
	if strings.Contains(stderr, "--seed") {
		t.Errorf("a run that drew nothing prints a seed line:\n%s", stderr)
	}
}

// A passing run that drew random values has nothing to reproduce on the
// console, and still records the seed in its report, where a later comparison
// can read it.
func TestAPassingRunPrintsNoSeedLine(t *testing.T) {
	srv, _ := recordingServer(t, 200)
	stdout, stderr, err := runStreams(t, randomArt(srv.URL), "--report", "json", "--seed", "9")
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if strings.Contains(stderr, "--seed") {
		t.Errorf("a passing run prints a seed line:\n%s", stderr)
	}
	if !strings.Contains(stdout, `"seed": 9`) {
		t.Errorf("the report of a run that drew values has no seed:\n%s", stdout)
	}
}

// --seed is the whole promise: the same seed draws the same uuid(),
// random_int() and random_string() in the same scenario, and another seed
// draws others.
func TestTheSameSeedDrawsTheSameValues(t *testing.T) {
	srv, rec := recordingServer(t, 200)
	for _, seed := range []string{"7", "7", "8"} {
		if _, _, err := runStreams(t, randomArt(srv.URL), "--seed", seed); err != nil {
			t.Fatalf("--seed %s: %v", seed, err)
		}
	}
	ids := rec.seen()
	if len(ids) != 3 {
		t.Fatalf("the server saw %d requests, want 3: %v", len(ids), ids)
	}
	if ids[0] != ids[1] {
		t.Errorf("--seed 7 drew %q and then %q", ids[0], ids[1])
	}
	if ids[0] == ids[2] {
		t.Errorf("--seed 7 and --seed 8 drew the same %q", ids[0])
	}
}

// A scenario's file is keyed by its path within what was run, so the same
// suite named three ways seeds its generators the same way.
func TestSeedKeyIsThePathWithinTheRun(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"suite/orders/a.art": "", "suite/b.art": ""})
	file := filepath.Join(dir, "suite", "orders", "a.art")
	cases := []struct{ root, want string }{
		{filepath.Join(dir, "suite"), "orders/a.art"},
		{filepath.Join(dir, "suite") + string(filepath.Separator), "orders/a.art"},
		{filepath.Join(dir, "suite", "orders", "a.art"), "a.art"},
		{"", "a.art"},
	}
	for _, c := range cases {
		rt := &runtimeEnv{root: c.root}
		if got := rt.seedKey(file); got != c.want {
			t.Errorf("seedKey under root %q = %q, want %q", c.root, got, c.want)
		}
	}
}
