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

// suiteServer logs in at /login -- a token in the body and a session cookie --
// echoes the Cookie header at /me, and counts logins and logouts.
type suiteServer struct {
	mu              sync.Mutex
	logins, logouts int
}

func newSuiteServer(t *testing.T, failLogin bool) (*httptest.Server, *suiteServer) {
	t.Helper()
	ss := &suiteServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ss.mu.Lock()
		defer ss.mu.Unlock()
		switch r.URL.Path {
		case "/login":
			ss.logins++
			if failLogin {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "s1", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"token": "t1"}`)
		case "/logout":
			ss.logouts++
		case "/me":
			fmt.Fprint(w, r.Header.Get("Cookie")+" "+r.Header.Get("Authorization"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, ss
}

func suiteTree(t *testing.T, url string) string {
	t.Helper()
	me := fmt.Sprintf(`scenario %%q {
  step "me" {
    get "${suite.url}/me" {
      header "Authorization" = "Bearer ${suite.token}"
    }
    expect raw == "sid=s1 Bearer t1"
  }
}
`)
	return writeTree(t, t.TempDir(), map[string]string{
		"suite/suite.art": fmt.Sprintf(`suite {
  var url = %q

  setup {
    step "log in" {
      post "${url}/login"
      expect status == 200
      capture token = body.token
    }
  }

  teardown {
    step "log out" {
      post "${url}/logout"
    }
  }
}
`, url),
		"suite/a.art":        fmt.Sprintf(me, "a"),
		"suite/orders/b.art": fmt.Sprintf(me, "b"),
	})
}

type suiteDoc struct {
	Scenarios []struct {
		Name   string `json:"name"`
		File   string `json:"file"`
		Kind   string `json:"kind"`
		Status string `json:"status"`
		Steps  []struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"steps"`
	} `json:"scenarios"`
}

// A suite's setup runs once, before every scenario under it, and its teardown
// once after them all; every scenario reads its values as suite.<name> and
// starts with its cookies.
func TestASuiteSetsUpOnceForItsFolder(t *testing.T) {
	srv, ss := newSuiteServer(t, false)
	dir := suiteTree(t, srv.URL)

	stdout, stderr, err := runCLI(t, "run", dir+"/suite", "--report", "json")
	if err != nil {
		t.Fatalf("run: %v\n%s\n%s", err, stdout, stderr)
	}
	if ss.logins != 1 || ss.logouts != 1 {
		t.Errorf("logins = %d, logouts = %d, want one of each", ss.logins, ss.logouts)
	}
	var doc suiteDoc
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("report: %v\n%s", err, stdout)
	}
	var names []string
	for _, sc := range doc.Scenarios {
		names = append(names, sc.Name+"/"+sc.Kind+"/"+sc.Status)
	}
	if got := strings.Join(names, ", "); got != "suite setup/suite/pass, a//pass, b//pass, suite teardown/suite/pass" {
		t.Errorf("scenarios = %s", got)
	}
}

// A suite setup that does not pass runs no scenario under it -- each is
// skipped, naming the suite -- and the run fails.
func TestAFailedSuiteSkipsItsScenarios(t *testing.T) {
	srv, _ := newSuiteServer(t, true)
	dir := suiteTree(t, srv.URL)

	stdout, _, err := runCLI(t, "run", dir+"/suite", "--report", "json")
	if err == nil {
		t.Fatal("a run whose suite failed passed")
	}
	var doc suiteDoc
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("report: %v\n%s", err, stdout)
	}
	for _, sc := range doc.Scenarios {
		if sc.Kind == "suite" {
			continue
		}
		if len(sc.Steps) != 1 || sc.Steps[0].Status != "skip" || !strings.Contains(sc.Steps[0].Error, "suite setup") {
			t.Errorf("scenario %s = %+v, want its step skipped naming the suite", sc.Name, sc.Steps)
		}
	}
}

// suite.<name> is checked like page.<name>: a name the suite does not give is a
// compile error with the names it does.
func TestASuiteNameIsChecked(t *testing.T) {
	srv, _ := newSuiteServer(t, false)
	dir := suiteTree(t, srv.URL)
	writeTree(t, dir, map[string]string{"suite/typo.art": `scenario "typo" {
  step "s" {
    get "${suite.tokn}"
  }
}
`})
	_, stderr, err := runCLI(t, "parse", "-f", dir+"/suite/typo.art")
	if err == nil {
		t.Fatal("suite.tokn compiled")
	}
	if !strings.Contains(stderr, `unknown field "tokn"`) || !strings.Contains(stderr, `did you mean "token"?`) {
		t.Errorf("stderr:\n%s", stderr)
	}
}
