package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A data-driven scenario runs once per row, each row a scenario of its own in
// the console and in the report, where it says which row of which data it is.
func TestEachRowRunsAsAScenario(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("role") == "viewer" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	users := `[{"role": "admin", "want": 200}, {"role": "viewer", "want": 403}]`
	if err := os.WriteFile(filepath.Join(dir, "users.json"), []byte(users), 0o600); err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(dir, "login.art")
	src := fmt.Sprintf(`scenario "login as ${user.role}" for user in json("users.json") {
  step "ask" {
    get %q {
      query "role" = user.role
    }
    expect status == user.want
  }
}
`, srv.URL)
	if err := os.WriteFile(art, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLI(t, "run", art, "--report", "json")
	if err != nil {
		t.Fatalf("run: %v\n%s\n%s", err, stdout, stderr)
	}
	var doc struct {
		Scenarios []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Row    *struct {
				Source string `json:"source"`
				Index  int    `json:"index"`
			} `json:"row"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("report: %v\n%s", err, stdout)
	}
	if len(doc.Scenarios) != 2 {
		t.Fatalf("%d scenarios in the report, want 2", len(doc.Scenarios))
	}
	for i, want := range []string{"login as admin", "login as viewer"} {
		sc := doc.Scenarios[i]
		if sc.Name != want || sc.Status != "pass" || sc.Row == nil || sc.Row.Index != i || sc.Row.Source != "users.json" {
			t.Errorf("scenario %d = %+v (row %+v)", i, sc, sc.Row)
		}
	}
	if !strings.Contains(stderr, "login as viewer") {
		t.Errorf("the console does not name the second row:\n%s", stderr)
	}
}
