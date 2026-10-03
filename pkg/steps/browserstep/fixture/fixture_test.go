package fixture

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// These tests need no browser, which is the point of having them. A fixture
// that stopped serving #who would otherwise fail as a browser timeout in a
// -tags browser run nobody runs in CI; here it fails as "the form page has no
// #user", in the default suite, naming the selector.

func get(t *testing.T, srv *httptest.Server, path string) string {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path) //nolint:noctx // a test against its own loopback server
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: reading the body: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s is %d, want 200", path, resp.StatusCode)
	}
	return string(body)
}

func post(t *testing.T, srv *httptest.Server, path string, form url.Values) string {
	t.Helper()
	resp, err := srv.Client().PostForm(srv.URL+path, form) //nolint:noctx // likewise
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("POST %s: reading the body: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s is %d, want 200", path, resp.StatusCode)
	}
	return string(body)
}

func server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := NewServer()
	t.Cleanup(srv.Close)
	return srv
}

// Every control a browser action drives is on the form page. The selectors are
// the package's own constants, so a test that selects on one and a fixture that
// serves it cannot disagree -- which is the one thing this file exists for.
func TestTheFormPageHoldsEveryControlAnActionDrives(t *testing.T) {
	body := get(t, server(t), PathForm)

	for _, sel := range []string{SelUser, SelPass, SelPlan, SelAvatar, SelSubmit} {
		if !strings.Contains(body, `id="`+strings.TrimPrefix(sel, "#")+`"`) {
			t.Errorf("the form page has no %s", sel)
		}
	}
	if !strings.Contains(body, `class="menu"`) {
		t.Errorf("the form page has no %s for hover to move over", SelMenu)
	}
	if !strings.Contains(body, `<title>Sign in</title>`) {
		t.Error("the form page has no title, so page.title has nothing to read")
	}
	// A real select with real options: `select "#plan" = "pro"` chooses by the
	// option's value, so an option whose value attribute is missing would make
	// the action fail for a reason that is the fixture's fault.
	if !strings.Contains(body, `<option value="pro">`) {
		t.Error(`the plan select has no option with value="pro"`)
	}
	if !strings.Contains(body, `type="file"`) {
		t.Error("the form page has no file input, so upload has nowhere to attach")
	}
}

// What #who holds is the proof that `fill` typed into the right box: the server
// echoes the posted value, so a fill that silently did nothing produces an
// empty #who rather than a passing test.
func TestDoneEchoesWhatWasPosted(t *testing.T) {
	body := post(t, server(t), PathDone, url.Values{"user": {"alice"}, "plan": {"pro"}})

	if !strings.Contains(body, `<span id="who">alice</span>`) {
		t.Errorf("#who does not hold the posted user; body:\n%s", body)
	}
	if !strings.Contains(body, `plan: pro`) {
		t.Error(`.plan-badge does not read "plan: pro", which is what capture match() reads`)
	}
	if !strings.Contains(body, `<title>Welcome</title>`) {
		t.Error("the done page is not titled Welcome, so page.title has nothing to assert")
	}
	if n := strings.Count(body, `class="invoice"`); n != Invoices {
		t.Errorf("the done page serves %d .invoice rows, want %d", n, Invoices)
	}
}

// A form value is escaped before it reaches the page. It is a fixture and
// nothing hostile posts to it, but a user called `<b>` that arrived as markup
// would change the DOM a test selects on, which is a confusing failure rather
// than a security one.
func TestDoneEscapesWhatItEchoes(t *testing.T) {
	body := post(t, server(t), PathDone, url.Values{"user": {`<b>bold</b>`}})

	if strings.Contains(body, "<b>bold</b>") {
		t.Error("the done page echoed markup unescaped")
	}
	if !strings.Contains(body, "&lt;b&gt;bold&lt;/b&gt;") {
		t.Errorf("the done page did not echo the escaped user; body:\n%s", body)
	}
}

// The settle delay is the fixture's reason to exist: [role=status] starts as
// "Working" and the page's own script makes it "Pro" later. Both halves are
// asserted on the HTML, because a test of `within` that is really testing a
// typo in a selector is worse than no test.
func TestStatusStartsUnsettledAndSettlesLate(t *testing.T) {
	body := post(t, server(t), PathDone, url.Values{"user": {"alice"}})

	if !strings.Contains(body, `<div role="status">Working</div>`) {
		t.Error(`[role=status] does not start as "Working", so a one-shot read would pass by luck`)
	}
	if !strings.Contains(body, `textContent = 'Pro'`) {
		t.Error(`nothing on the page ever sets [role=status] to "Pro"`)
	}
	if !strings.Contains(body, fmt.Sprintf("}, %d);", DefaultSettle.Milliseconds())) {
		t.Errorf("the default settle delay is not %d ms in the served page", DefaultSettle.Milliseconds())
	}
}

// ?settle= is what lets one route serve both the assertion that settles and the
// assertion that does not have to wait.
func TestSettleIsReadOffTheQuery(t *testing.T) {
	srv := server(t)
	for _, tc := range []struct {
		query string
		want  int64
	}{
		{"", DefaultSettle.Milliseconds()},
		{"?settle=0", 0},
		{"?settle=250", 250},
		// Unreadable and negative values are the default rather than an error:
		// a 400 from a fixture reaches the reader as a browser assertion
		// failing for an unrelated reason.
		{"?settle=soon", DefaultSettle.Milliseconds()},
		{"?settle=-5", DefaultSettle.Milliseconds()},
	} {
		body := post(t, srv, PathDone+tc.query, url.Values{"user": {"a"}})
		if !strings.Contains(body, fmt.Sprintf("}, %d);", tc.want)) {
			t.Errorf("POST %s settles at something other than %d ms", PathDone+tc.query, tc.want)
		}
	}
}

// The page a `within` budget is meant to expire against. Nothing appears on it,
// so a test that measures an expiring budget measures the budget: it cannot
// accidentally pass because a slow machine got there eventually.
func TestNeverHoldsNothingThatSettles(t *testing.T) {
	body := get(t, server(t), PathNever)

	if strings.Contains(body, "setTimeout") || strings.Contains(body, "<script") {
		t.Error("/never runs a script, so something on it might appear after all")
	}
	if strings.Contains(body, "role=\"status\"") {
		t.Error("/never serves a [role=status], which is the element a within test waits for elsewhere")
	}
	if !strings.Contains(body, `<title>Never</title>`) {
		t.Error("/never has no title")
	}
}

// goto in a scenario navigates to the same URL more than once, and the second
// visit has to be the server's answer rather than the first visit's: a cached
// /done would arrive with its settle delay already elapsed, so a `within` test
// would pass without waiting and prove nothing.
func TestPagesAreNotCached(t *testing.T) {
	srv := server(t)
	resp, err := srv.Client().Get(srv.URL + PathForm) //nolint:noctx // a test against its own loopback server
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control is %q, want no-store", got)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "charset=utf-8") {
		t.Errorf("Content-Type is %q, want an explicit charset so text content is predictable", got)
	}
}

// The server is on loopback and nothing it serves fetches anything. This is the
// "no public sites in CI, ever" rule, asserted rather than trusted: a page that
// grew a CDN script would make every browser test depend on someone else's
// deployment.
func TestNothingReachesOffTheMachine(t *testing.T) {
	srv := server(t)
	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") {
		t.Errorf("the fixture is served from %s, want 127.0.0.1", srv.URL)
	}
	for _, path := range []string{PathForm, PathNever} {
		for _, bad := range []string{"http://", "https://", "//"} {
			if strings.Contains(get(t, srv, path), bad) {
				t.Errorf("%s holds %q: a fixture page must load nothing from off the machine", path, bad)
			}
		}
	}
	body := post(t, srv, PathDone, url.Values{"user": {"a"}})
	for _, bad := range []string{"http://", "https://"} {
		if strings.Contains(body, bad) {
			t.Errorf("%s holds %q", PathDone, bad)
		}
	}
}

// A path no route claims serves the form rather than a 404. ServeMux's "/" is a
// catch-all and that is what is wanted here: a browser reports a 404 as a page
// with no elements on it, which is a long way from "you typed the path wrong".
func TestAnUnknownPathServesTheForm(t *testing.T) {
	if !strings.Contains(get(t, server(t), "/typo"), `<title>Sign in</title>`) {
		t.Error("/typo does not serve the form page")
	}
}
