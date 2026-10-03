// Package browserstep is the browser step: it performs a scenario's actions in
// a page and reports what the page then looks like. It is registered in the
// default registry under the step type "browser", which is what pkg/dsl/lower
// stamps on a step whose action block is `browser { ... }`.
//
// Like the other two executors it makes no assertions. What a scenario expects
// of a page is an `expect` expression, evaluated by pkg/eval against the roots
// Observe returns and the element functions Bindings hands over.
//
// # It borrows a page and never owns one
//
// A browser step is not stateless: step 4 reads what step 3 typed. So the page
// belongs to the scenario, not to the step, and this package reaches it the way
// pkg/executor/sessions.go specifies -- SessionsOf, then Session, then the cast
// to *session.Session that pkg/session/doc.go prescribes. Nothing here can
// close a session: *session.Session has Page and no exported Close, so a step
// that panicked cannot leak a browser process.
//
// # Actions wait; functions do not
//
//	browser { goto "/billing"  click "text=Upgrade" }
//	expect text("[role=status]") contains "Pro" within "10s"
//
// Those two lines wait in two different ways and the difference is the whole
// design. The `click` auto-waits for the element to be there and be clickable,
// which is Playwright's own behaviour and is inherited rather than
// reimplemented -- nothing in this package polls or sleeps on an action's
// behalf. The `expect` is the opposite: text() reads the DOM *now* and answers
// now, and the waiting is the runner's settle loop re-asking that one assertion
// until its `within` budget runs out.
//
// Making the element functions wait internally would break two things SPEC.md
// promises. `expect text(".error") is null` would block for the full Playwright
// timeout before passing, because waiting for an element that is deliberately
// absent is waiting for the timeout; and a `within "2s"` budget would be a
// fiction next to a read that had already blocked for 30s. So text(), value()
// and attr() go through QuerySelector -- which answers nil, with no error and
// no delay, for a selector that matches nothing -- rather than through
// page.TextContent, which auto-waits.
//
// # The driver seam
//
// Nothing in this package but page.go mentions Playwright. The driver interface
// is the eight actions, the two roots, the five element functions and a
// screenshot, all in plain Go values; pwPage is the one real implementation and
// a fake one in the tests is the other.
//
// That is what lets `go test ./...` -- the run CI makes, on a machine with no
// Chromium and no 683 MB download -- cover the act loop, the roots, the
// element functions and the screenshot naming. The build-tagged tests then have
// one job each: page_test.go proves pwPage maps each method onto the right
// Playwright call, and scenario_test.go proves the whole thing against a real
// browser and the local fixture server.
//
//	go test ./pkg/steps/browserstep/...               # what CI runs
//	go test -tags browser ./pkg/steps/browserstep     # needs Chromium
//
// It is also docs/browser-engine.md's undo path made cheap. That document calls
// playwright-go the most reversible-at-cost decision in the design; this
// interface is what that reversal would cost -- one new implementation of one
// file.
//
// # The fixture server
//
// The pages the browser tests drive are served by the fixture subpackage, on
// loopback, with no script, style or image loaded from anywhere. No test in
// artemis reaches a public site under any build tag. It is a package rather
// than a test helper because ART-48's and ART-49's generated Python runs
// against the same server.
package browserstep
