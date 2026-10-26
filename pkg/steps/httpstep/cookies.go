package httpstep

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"time"
)

// RootCookies is the cookies the response set, keyed by name.
const RootCookies = "cookies"

// jarKey is the context key a scenario's cookie jar travels under.
type jarKey struct{}

// WithJar gives every api step under ctx one cookie jar: a cookie a response
// sets is sent on the scenario's later requests to the same site, which is
// what a browser does and what an API that logs in with a cookie needs.
//
// One jar per scenario, made where the scenario starts, so what one scenario
// was given never reaches another -- the same boundary pkg/session draws around
// a browser. A nil jar is the scenario that wrote `config http { cookies =
// false }`: every request is sent with only the headers it wrote.
func WithJar(ctx context.Context, jar http.CookieJar) context.Context {
	return context.WithValue(ctx, jarKey{}, jar)
}

// jarOf is the scenario's jar, or nil when it has none.
func jarOf(ctx context.Context) http.CookieJar {
	jar, _ := ctx.Value(jarKey{}).(http.CookieJar)
	return jar
}

// NewJar is an empty jar for one scenario.
//
// It has no public suffix list, so a response may set a cookie for a whole
// registrable domain's parent -- which a browser refuses for `.com` and this
// accepts. A test suite talks to the services it names, and a list of every
// public suffix is a dependency for a protection those services do not need.
func NewJar() http.CookieJar {
	jar, _ := cookiejar.New(nil) // nil options cannot fail
	return jar
}

// clientFor is the client one request is sent with: the shared one, with the
// scenario's jar when there is a jar to use.
//
// A request whose step wrote its own Cookie header is sent with exactly that
// header and nothing from the jar: the author said what to send, and a test of
// a forged or expired session would otherwise be quietly given the real one.
// The response's cookies are still stored, by the caller, so the steps after it
// see what the server said.
func (e Executor) clientFor(ctx context.Context, req *http.Request) (*http.Client, http.CookieJar) {
	base := e.client()
	jar := jarOf(ctx)
	if jar == nil {
		return base, nil
	}
	if req.Header.Get("Cookie") != "" {
		return base, jar
	}
	withJar := *base
	withJar.Jar = jar
	return &withJar, nil
}

// cookieValues is the cookies a response set, as the object `cookies`
// resolves to: each name to its value and attributes, in the evaluator's
// domain.
//
// An attribute the response did not send is null rather than an empty string,
// so `expect cookies.sid.domain is null` says what it means. Two cookies of
// one name keep the last, which is the one a browser would hold.
func cookieValues(h http.Header) map[string]any {
	out := map[string]any{}
	for _, c := range (&http.Response{Header: h}).Cookies() {
		out[c.Name] = map[string]any{
			"value":     c.Value,
			"domain":    orNull(c.Domain),
			"path":      orNull(c.Path),
			"expires":   expires(c),
			"secure":    c.Secure,
			"http_only": c.HttpOnly,
			"same_site": sameSite(c.SameSite),
		}
	}
	return out
}

func orNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// expires is the cookie's expiry as RFC 3339 in UTC, or null for a session
// cookie or an Expires the parser could not read.
func expires(c *http.Cookie) any {
	if c.Expires.IsZero() {
		return nil
	}
	return c.Expires.UTC().Format(time.RFC3339)
}

func sameSite(s http.SameSite) any {
	switch s {
	case http.SameSiteLaxMode:
		return "lax"
	case http.SameSiteStrictMode:
		return "strict"
	case http.SameSiteNoneMode:
		return "none"
	}
	return nil
}
