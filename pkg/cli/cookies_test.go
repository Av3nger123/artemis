package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cookieServer logs a client in with a cookie at /login and answers /me with
// the Cookie header it was sent, or "none".
func cookieServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/", HttpOnly: true})
			w.WriteHeader(http.StatusOK)
			return
		}
		got := r.Header.Get("Cookie")
		if got == "" {
			got = "none"
		}
		_, _ = w.Write([]byte(got))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A scenario's api steps share a cookie jar: the login's cookie reaches the
// next step, `cookies` reads what the login set, and `duration_ms` is a number
// every step has. The scenario after it starts with an empty jar, and one that
// wrote `config http { cookies = false }` sends none at all.
func TestAScenarioKeepsItsCookiesAndOnlyItsOwn(t *testing.T) {
	srv := cookieServer(t)
	src := fmt.Sprintf(`scenario "logs in" {
  step "login" {
    post %[1]q
    expect status == 200
    expect cookies.sid.value == "abc"
    expect cookies.sid.http_only == true
    expect duration_ms >= 0
  }
  step "me" {
    get %[2]q
    expect raw == "sid=abc"
  }
}

scenario "a new scenario has a new jar" {
  step "me" {
    get %[2]q
    expect raw == "none"
  }
}

scenario "no jar" {
  config http { cookies = false }

  step "login" {
    post %[1]q
    expect status == 200
  }
  step "me" {
    get %[2]q
    expect raw == "none"
  }
}
`, srv.URL+"/login", srv.URL+"/me")

	stdout, stderr, err := runStreams(t, src)
	if err != nil {
		t.Fatalf("Execute() = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "Scenarios     3  (3 passed)") {
		t.Errorf("not every scenario passed:\n%s", stdout)
	}
}

// duration_ms is in scope in a step and nowhere else: a var's value has no
// attempt to have measured.
func TestDurationIsNotInAVar(t *testing.T) {
	_, stderr, err := runStreams(t, `scenario "x" {
  var took = duration_ms
  step "s" { run "true" }
}
`)
	if err == nil {
		t.Fatal("a var reading duration_ms compiled")
	}
	if !strings.Contains(stderr, `"duration_ms" is not in scope in a var's value`) {
		t.Errorf("stderr does not say why:\n%s", stderr)
	}
}
