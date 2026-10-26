package httpstep

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// session is a server that logs a client in with a cookie and echoes back the
// Cookie header it was sent, which is everything a jar test needs to see.
func session(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(r.Header.Get("Cookie")))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, ctx context.Context, url string, headers map[string]string) map[string]any {
	t.Helper()
	roots, err := Executor{}.Observe(ctx, models.Step{
		Name:    "get",
		Request: models.Request{Method: "GET", URL: url, Headers: headers},
	}, executor.NewScope())
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return roots
}

// A cookie a response sets is sent on the scenario's next request: the jar is
// what makes an API that logs in with a cookie testable.
func TestTheJarSendsWhatALoginSet(t *testing.T) {
	srv := session(t)
	ctx := WithJar(context.Background(), NewJar())

	get(t, ctx, srv.URL+"/login", nil)
	if got := get(t, ctx, srv.URL+"/me", nil)[RootRaw]; got != "sid=abc" {
		t.Errorf("the request after the login sent Cookie %q, want sid=abc", got)
	}
}

// No jar is the scenario that wrote `config http { cookies = false }`, and the
// context of anything that never set one: every request sends only the headers
// it wrote.
func TestNoJarSendsNoCookie(t *testing.T) {
	srv := session(t)
	for _, ctx := range []context.Context{context.Background(), WithJar(context.Background(), nil)} {
		get(t, ctx, srv.URL+"/login", nil)
		if got := get(t, ctx, srv.URL+"/me", nil)[RootRaw]; got != "" {
			t.Errorf("a request with no jar sent Cookie %q", got)
		}
	}
}

// A step that writes its own Cookie header sends exactly that -- a test of a
// forged session must not be handed the real one -- and what its response sets
// is still stored for the steps after it.
func TestAnExplicitCookieHeaderBypassesTheJar(t *testing.T) {
	srv := session(t)
	ctx := WithJar(context.Background(), NewJar())

	get(t, ctx, srv.URL+"/login", nil)
	if got := get(t, ctx, srv.URL+"/me", map[string]string{"Cookie": "sid=forged"})[RootRaw]; got != "sid=forged" {
		t.Errorf("a step that wrote its own Cookie sent %q, want sid=forged alone", got)
	}

	fresh := WithJar(context.Background(), NewJar())
	get(t, fresh, srv.URL+"/login", map[string]string{"Cookie": "sid=old"})
	if got := get(t, fresh, srv.URL+"/me", nil)[RootRaw]; got != "sid=abc" {
		t.Errorf("a login sent with its own Cookie did not store what it set: the next request sent %q", got)
	}
}

// `cookies` is what this response set, name to value and attributes, with an
// attribute it did not send as null.
func TestCookiesRootIsWhatTheResponseSet(t *testing.T) {
	srv := session(t)
	roots := get(t, context.Background(), srv.URL+"/login", nil)

	cookies, ok := roots[RootCookies].(map[string]any)
	if !ok {
		t.Fatalf("cookies = %#v, want an object", roots[RootCookies])
	}
	sid, ok := cookies["sid"].(map[string]any)
	if !ok {
		t.Fatalf("cookies.sid = %#v, want an object", cookies["sid"])
	}
	want := map[string]any{
		"value": "abc", "domain": nil, "path": "/", "expires": nil,
		"secure": false, "http_only": true, "same_site": "lax",
	}
	for k, v := range want {
		if sid[k] != v {
			t.Errorf("cookies.sid.%s = %#v, want %#v", k, sid[k], v)
		}
	}
	if len(sid) != len(want) {
		t.Errorf("cookies.sid has %d attributes, want %d: %v", len(sid), len(want), sid)
	}

	if none := get(t, context.Background(), srv.URL+"/me", nil)[RootCookies]; len(none.(map[string]any)) != 0 {
		t.Errorf("a response that set no cookie has cookies = %v, want {}", none)
	}
}
