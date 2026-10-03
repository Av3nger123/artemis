package browserstep

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

// This is the only file in artemis outside pkg/session that imports
// playwright-go. Everything above it is driver's plain strings and errors.
//
// Each method is the thinnest possible mapping onto one Playwright call, and
// the mapping itself is what page_test.go -- `-tags browser` -- checks against
// a real Chromium. There is no logic here worth testing in CI, which is the
// point: the logic is in act.go and elements.go, over driver.
//
// # Locators, and the four calls that stay page-level
//
// Playwright deprecated its selector-taking Page methods in favour of locators,
// so the acts below go through p.locator(selector) -- which is
// page.Locator(sel).First(), because a locator is strict and would error on a
// selector matching two elements where the method it replaces used the first.
// `click ".row"` clicking the first row is the behaviour artemis shipped, and a
// lint fix is not the place to change it.
//
// Four calls have no locator spelling that means the same thing, and each is
// marked with the reason at its own call site:
//
//   - WaitForTimeout, whose deprecation is advice ("never wait for timeout in
//     production") rather than a replacement. The DSL has `wait "250ms"`.
//   - QuerySelector and the two reads off its handle, TextContent and
//     InputValue. The whole point of the handle path is that QuerySelector
//     answers nil with no delay for a selector matching nothing, which is what
//     makes `expect text(".x") is null` an answer instead of a timeout. A
//     locator read auto-waits and then errors.

// pwPage is a playwright Page as a driver.
type pwPage struct{ page playwright.Page }

// newPage adapts a page. It takes the interface rather than *session.Session so
// that this file knows nothing about where the page came from.
func newPage(p playwright.Page) *pwPage { return &pwPage{page: p} }

var _ driver = (*pwPage)(nil)

// Goto navigates, resolving a relative target against where the page already
// is.
//
// SPEC.md: "A relative one resolves against the page's current address". That
// resolution is done here rather than left to the engine because Playwright has
// no base URL unless the browser context was given one, and this context was
// not -- a `goto "/orders"` handed straight through fails with "Protocol error"
// rather than navigating. net/url's ResolveReference is the same rule a browser
// applies to a link.
//
// A fresh page is at about:blank, which is not a base anything can resolve
// against, so a relative first `goto` is an error that says what to write
// instead -- the scenario meant an absolute URL, or meant to navigate first.
func (p *pwPage) Goto(target string) error {
	resolved, err := p.resolve(target)
	if err != nil {
		return err
	}
	if _, err := p.page.Goto(resolved); err != nil {
		return fmt.Errorf("goto %s: %w", resolved, err)
	}
	return nil
}

// resolve turns a possibly relative target into the URL to navigate to.
func (p *pwPage) resolve(target string) (string, error) {
	ref, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("goto %q: not a URL: %w", target, err)
	}
	if ref.IsAbs() {
		return target, nil
	}
	here := p.page.URL()
	if here == "" || here == "about:blank" {
		return "", fmt.Errorf("goto %q is relative and the page has not been anywhere yet "+
			"(the first goto of a scenario needs a full URL, like \"http://localhost:8080%s\")", target, target)
	}
	base, err := url.Parse(here)
	if err != nil {
		// Unreachable: it is an address the browser is already showing.
		return "", fmt.Errorf("goto %q: the page is at %q, which is not a URL: %w", target, here, err)
	}
	return base.ResolveReference(ref).String(), nil
}

func (p *pwPage) Click(selector string) error {
	if err := p.locator(selector).Click(); err != nil {
		return fmt.Errorf("click %s: %w", selector, err)
	}
	return nil
}

func (p *pwPage) Hover(selector string) error {
	if err := p.locator(selector).Hover(); err != nil {
		return fmt.Errorf("hover %s: %w", selector, err)
	}
	return nil
}

func (p *pwPage) Fill(selector, value string) error {
	if err := p.locator(selector).Fill(value); err != nil {
		return fmt.Errorf("fill %s: %w", selector, err)
	}
	return nil
}

// Select chooses by the option's *value*, which is what SPEC.md's table says
// and is why this is Values and not Labels: `select "#plan" = "pro"` means the
// option whose value attribute is "pro", not the one whose text reads "pro".
func (p *pwPage) Select(selector, value string) error {
	if _, err := p.locator(selector).SelectOption(playwright.SelectOptionValues{
		Values: &[]string{value},
	}); err != nil {
		return fmt.Errorf("select %s = %q: %w", selector, value, err)
	}
	return nil
}

// Upload attaches a file. The path is on the machine artemis is running on, and
// a path that is not there is Playwright's error rather than a stat here: the
// engine resolves it relative to its own working directory and duplicating that
// rule would be a second answer to where a relative path points.
func (p *pwPage) Upload(selector, path string) error {
	if err := p.locator(selector).SetInputFiles(path); err != nil {
		return fmt.Errorf("upload %s = %q: %w", selector, path, err)
	}
	return nil
}

// Press sends one key to whatever has focus.
//
// page.Keyboard().Press, not page.Press(selector, key): SPEC.md says "Sends one
// key to whatever has focus", and `press "Enter"` carries a key and no
// selector. page.Press would need one, and inventing "body" as the selector
// would send the key somewhere other than the field a `fill` just typed into.
func (p *pwPage) Press(key string) error {
	if err := p.page.Keyboard().Press(key); err != nil {
		return fmt.Errorf("press %q: %w", key, err)
	}
	return nil
}

// Wait pauses. page.WaitForTimeout rather than time.Sleep so the pause happens
// on the browser's clock with the page still being serviced -- a Go sleep would
// block this goroutine while the driver connection sat idle.
//
// Playwright deprecates it as advice rather than in favour of anything: the DSL
// has `wait "250ms"`, SPEC.md documents it, and a scenario that asks to wait is
// asking for exactly this.
func (p *pwPage) Wait(d time.Duration) {
	p.page.WaitForTimeout(float64(d.Milliseconds())) //nolint:staticcheck // SA1019: no replacement; see the package comment
}

// SetTimeout bounds every later call that waits.
func (p *pwPage) SetTimeout(d time.Duration) {
	p.page.SetDefaultTimeout(float64(d.Milliseconds()))
}

// URL is `page.url`: the address after any redirect or in-page navigation,
// which is what Playwright's URL() already reports.
func (p *pwPage) URL() (string, error) { return p.page.URL(), nil }

// Title is `page.title`.
func (p *pwPage) Title() (string, error) {
	title, err := p.page.Title()
	if err != nil {
		return "", fmt.Errorf("read page.title: %w", err)
	}
	return title, nil
}

// Text is `text(sel)`: the first match's text content, or null for no match.
//
// QuerySelector and then the handle, rather than page.TextContent(sel): see the
// package comment. QuerySelector answers nil with no error and no delay for a
// selector that matches nothing, which is what makes `null` the natural answer
// instead of a timeout to be caught and translated.
func (p *pwPage) Text(selector string) (any, error) {
	el, err := p.query(selector)
	if el == nil || err != nil {
		return nil, err
	}
	text, err := el.TextContent() //nolint:staticcheck // SA1019: a locator read auto-waits; see the package comment
	if err != nil {
		return nil, fmt.Errorf("text(%s): %w", selector, err)
	}
	return text, nil
}

// Value is `value(sel)`: the first match's value as a form control.
func (p *pwPage) Value(selector string) (any, error) {
	el, err := p.query(selector)
	if el == nil || err != nil {
		return nil, err
	}
	value, err := el.InputValue() //nolint:staticcheck // SA1019: a locator read auto-waits; see the package comment
	if err != nil {
		return nil, fmt.Errorf("value(%s): %w", selector, err)
	}
	return value, nil
}

// Attr is `attr(sel, name)`: the named attribute, null when the element has
// none and null when there is no element.
//
// The two nulls are the same answer by design. SPEC.md: "An absent attribute on
// a present element is null for the same reason -- `exists` is the operator for
// asking".
//
// It goes through Evaluate rather than the handle's GetAttribute because
// GetAttribute cannot tell the two apart: playwright-go turns the engine's null
// into "", so an absent attribute and `href=""` arrive identically, and
// `expect attr("#link", "href") is null` could never pass. The DOM's own
// getAttribute returns null, which crosses the wire as nil, so this reports
// exactly what the DOM says. The attribute name is passed as an argument rather
// than interpolated into the expression, so a name with a quote in it cannot
// change the script.
func (p *pwPage) Attr(selector, name string) (any, error) {
	el, err := p.query(selector)
	if el == nil || err != nil {
		return nil, err
	}
	value, err := el.Evaluate("(el, name) => el.getAttribute(name)", name)
	if err != nil {
		return nil, fmt.Errorf("attr(%s, %s): %w", selector, name, badSelector(err))
	}
	return value, nil
}

// Count is `count(sel)`: how many elements match, as a number in the
// evaluator's domain -- a float64, like every other number, so `expect
// count(".invoice") > 0` compares two numbers of one type.
//
// A locator count does not wait and is 0 rather than an error for no match,
// which is SPEC.md's decision and the reason `expect count(".invoice") == 0` is
// writable at all.
func (p *pwPage) Count(selector string) (any, error) {
	n, err := p.page.Locator(selector).Count()
	if err != nil {
		return nil, fmt.Errorf("count(%s): %w", selector, err)
	}
	return float64(n), nil
}

// Visible is `visible(sel)`: whether the first match is visible, and false --
// not an error -- when nothing matches. IsVisible is Playwright's own
// non-waiting predicate and has exactly that behaviour.
func (p *pwPage) Visible(selector string) (any, error) {
	ok, err := p.locator(selector).IsVisible()
	if err != nil {
		return nil, fmt.Errorf("visible(%s): %w", selector, err)
	}
	return ok, nil
}

// Screenshot writes the whole scrollable page, not just the viewport: a failure
// below the fold is exactly the one a reader cannot otherwise see, and the
// larger file is worth it for something only written when something broke.
func (p *pwPage) Screenshot(path string) error {
	if _, err := p.page.Screenshot(playwright.PageScreenshotOptions{
		Path:     playwright.String(path),
		FullPage: playwright.Bool(true),
	}); err != nil {
		return fmt.Errorf("screenshot to %s: %w", path, err)
	}
	return nil
}

// locator is the strict-free locator the acts use: Playwright's replacement for
// the deprecated selector-taking Page methods, with First() so that a selector
// matching two elements still acts on the first -- which is what the methods it
// replaces did, and what artemis shipped.
func (p *pwPage) locator(selector string) playwright.Locator {
	return p.page.Locator(selector).First()
}

// query is the no-match-is-not-an-error read the three value functions share.
//
// A nil handle and a nil error is "nothing matched", which the callers turn
// into null. An error is a selector the engine would not accept -- a malformed
// one -- and that is a real fault worth reporting, because a selector artemis
// cannot use will never match and reporting null would hide the typo.
func (p *pwPage) query(selector string) (playwright.ElementHandle, error) {
	el, err := p.page.QuerySelector(selector) //nolint:staticcheck // SA1019: nil-with-no-delay is the behaviour; see the package comment
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", selector, badSelector(err))
	}
	return el, nil
}

// badSelector trims the engine's own prefix off a selector complaint, which
// otherwise arrives as a multi-line "Error: " block inside an assertion's
// reason.
func badSelector(err error) error {
	msg := strings.TrimSpace(err.Error())
	if i := strings.Index(msg, "\n"); i > 0 {
		msg = msg[:i]
	}
	return fmt.Errorf("%s", strings.TrimPrefix(msg, "Error: "))
}
