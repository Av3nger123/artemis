// Package fixture is the local web server the browser tests drive.
//
// It is a package and not a helper in a _test.go file on purpose. Subsystem C
// -- ART-48's Python target and ART-49's execution-parity test -- runs
// generated pytest "against the same fixture server", which it cannot do if
// the server only exists inside a Go test binary. So NewServer takes no
// *testing.T, returns a plain *httptest.Server, and the one definition of what
// /done serves is shared by a Go test, a browser-tagged test and a Python
// subprocess.
//
// Every page is static HTML with one exception, and that exception is the whole
// reason this exists: [role=status] on /done says "Working" and becomes "Pro"
// after ?settle= milliseconds. An assertion that reads it once is a race, which
// is what `expect ... within` is for -- so a test of `within` needs a DOM that
// settles late, on a schedule it chose.
//
// Nothing here reaches off the machine. httptest binds 127.0.0.1 and the HTML
// loads no script, style or image from anywhere: a browser test in CI that
// touched a public site would be a test that fails when someone else's
// deployment does.
package fixture

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"
)

// DefaultSettle is how long [role=status] on /done stays "Working" when the
// request did not say.
//
// 1.2s is ART-45's number, from the spike that confirmed playwright-go: long
// enough that a single read reliably misses it, short enough that a test
// waiting for it is not a test anyone skips. A `within "10s"` assertion against
// it should pass in well under half its budget, which is what makes "it waited,
// and it stopped waiting" two separate things a test can tell apart.
const DefaultSettle = 1200 * time.Millisecond

// Routes served. They are constants so a test can name the route it drives
// rather than repeat a string the server might rename.
const (
	// PathForm is the sign-in form: what goto, fill, select, upload, press,
	// hover and click are pointed at.
	PathForm = "/"

	// PathDone is where the form posts: the page the roots and the five
	// element functions read, and the one that settles late.
	PathDone = "/done"

	// PathNever holds no #settled element and never grows one, which is what
	// a `within` budget that is meant to expire is pointed at. Without it a
	// test of an expiring budget would have to wait out a selector that might
	// appear, and could not tell a slow machine from a working one.
	PathNever = "/never"
)

// Elements of the fixture, as the selectors a scenario writes. They are here so
// that a fixture page and the test that selects on it cannot drift: the
// fixture's own test asserts each of these appears in the HTML it serves.
const (
	SelUser    = "#user"
	SelPass    = "#pass"
	SelPlan    = "#plan"
	SelAvatar  = "#avatar"
	SelSubmit  = "#go"
	SelMenu    = ".menu"
	SelHome    = "#home"
	SelWho     = "#who"
	SelBadge   = ".plan-badge"
	SelInvoice = ".invoice"
	SelStatus  = "[role=status]"
	SelMissing = "#nothing-matches-this"
)

// Invoices is how many .invoice rows /done serves, so `expect
// count(".invoice") == 3` and the page cannot disagree.
const Invoices = 3

// NewServer starts the fixture on 127.0.0.1 and returns it. The caller closes
// it -- in a Go test, srv.Close via t.Cleanup; in ART-49's parity test,
// whatever stands the Python run up.
func NewServer() *httptest.Server {
	return httptest.NewServer(Handler())
}

// Handler is the fixture's routing, exported so a caller that wants its own
// server -- a fixed port for a Python subprocess, say -- does not have to
// reimplement the pages.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(PathDone, done)
	mux.HandleFunc(PathNever, never)
	// Last, and on "/": ServeMux treats "/" as a catch-all, so a typo in a
	// test's path serves the form rather than a 404 the browser reports as a
	// blank page. The form is the page a scenario starts on anyway.
	mux.HandleFunc(PathForm, form)
	return mux
}

// form is the sign-in page. Every control a browser action drives is here, and
// each is the plainest HTML that has the behaviour: a real <select> so `select`
// has options to choose between, a real file input so `upload` has somewhere to
// attach, and a submit button so `click` navigates.
func form(w http.ResponseWriter, _ *http.Request) {
	writeHTML(w, `<!doctype html>
<html><head><title>Sign in</title></head>
<body>
<nav><a id="home" href="/">Home</a> <span class="menu" title="plans">Plans</span></nav>
<form method="POST" action="/done">
<input id="user" name="user" value="">
<input id="pass" name="pass" type="password" value="">
<select id="plan" name="plan">
<option value="free">Free</option>
<option value="pro">Pro</option>
</select>
<input id="avatar" name="avatar" type="file">
<button id="go" type="submit">Sign in</button>
</form>
</body></html>`)
}

// done is the page after the form.
//
// #who echoes what was filled, which is how a test proves the form sent the
// text `fill` typed rather than that `fill` returned no error. .plan-badge is
// the "plan: pro" that SPEC.md's `capture plan = match(text(".plan-badge"),
// /plan: (\w+)/)` reads. The script is the only one in this package and does
// one thing: flip [role=status] once, after the settle delay.
func done(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	user := r.FormValue("user")
	plan := r.FormValue("plan")
	if plan == "" {
		plan = "free"
	}

	var invoices string
	for i := 1; i <= Invoices; i++ {
		invoices += fmt.Sprintf("<li class=\"invoice\" data-n=\"%d\">invoice %d</li>\n", i, i)
	}

	writeHTML(w, fmt.Sprintf(`<!doctype html>
<html><head><title>Welcome</title></head>
<body>
<div id="greeting">hello <span id="who">%s</span></div>
<span class="plan-badge">plan: %s</span>
<ul>
%s</ul>
<div role="status">Working</div>
<script>
setTimeout(function () {
  document.querySelector('[role=status]').textContent = 'Pro';
}, %d);
</script>
</body></html>`, html.EscapeString(user), html.EscapeString(plan), invoices, settle(r).Milliseconds()))
}

// never is a page nothing ever appears on. It is what a `within` budget that
// must expire is pointed at: the assertion against it cannot pass on a fast
// machine or a slow one, so a test of an expiring budget measures the budget
// and nothing else.
func never(w http.ResponseWriter, _ *http.Request) {
	writeHTML(w, `<!doctype html>
<html><head><title>Never</title></head>
<body><p id="still">Nothing settles here.</p></body></html>`)
}

// settle reads ?settle=<milliseconds>.
//
// An absent or unreadable value is DefaultSettle rather than an error: this is
// a fixture, and a 400 from it would be reported to the reader as a browser
// assertion failing for an unrelated reason. A zero is honoured and means "it
// is already there", which is how a test that is not about waiting avoids
// waiting.
func settle(r *http.Request) time.Duration {
	raw := r.URL.Query().Get("settle")
	if raw == "" {
		return DefaultSettle
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 {
		return DefaultSettle
	}
	return time.Duration(ms) * time.Millisecond
}

// writeHTML sends a page with the one header that matters: without an explicit
// charset, a browser sniffs, and a sniffed page is a page whose text content a
// test cannot predict.
func writeHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Cache-Control, because a scenario navigates to the same URL more than
	// once and the second visit must be the server's answer, not the first
	// visit's -- a page with a settle delay that came out of the cache would
	// have already settled.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}
