//go:build browser

// The browser half of ART-49's execution parity: the same comparison
// parity_test.go runs over testdata/conformance, over the browser flow in
// testdata/conformance/browser.
//
//	go test -tags browser ./pkg/codegen
//	make test-browser
//
// Tagged off by default for the reason pkg/cli/browser_test.go is: CI has no
// browser, and the first run on a cold machine downloads about 683 MB of
// Chromium for the Go side (docs/browser-engine.md) on top of Playwright's own
// browsers for the Python side. What the default suite still checks about this
// corpus is everything short of running it -- that it compiles, that every
// scenario declares an outcome, and that a browser flow is in the corpus at all.
//
// Every page comes from the fixture server, on loopback, which is the same
// server the interpreter's browser tests drive.

package codegen

import (
	"os/exec"
	"testing"
)

// playwrightModule is the import the generated module needs on the Python side.
// Absent, this skips with the note pythonWith writes -- `pip install playwright`
// and `playwright install chromium` are two steps and the second is the 683 MB.
const playwrightModule = "playwright"

func TestBrowserExecutionParity(t *testing.T) {
	python := pythonWith(t, playwrightModule)
	requireChromium(t, python)
	parity(t, browserConformanceDir, nil, playwrightModule)
}

// requireChromium skips when Playwright's own browsers have not been
// downloaded for this interpreter.
//
// The module importing is not enough: `pip install playwright` leaves a
// Playwright with no browser, and launching one then fails with a message about
// running `playwright install`. Without this probe that missing download would
// be reported as a divergence between the backends -- the most misleading
// failure this test could produce, since nothing about either backend is wrong.
//
// The probe is a launch and a close, because asking Playwright to start the
// browser is the only thing that answers the question it is actually asked
// later.
func requireChromium(t *testing.T, python string) {
	t.Helper()
	const probe = "from playwright.sync_api import sync_playwright\n" +
		"with sync_playwright() as p:\n" +
		"    p.chromium.launch(headless=True).close()\n"
	if out, err := exec.Command(python, "-c", probe).CombinedOutput(); err != nil { //nolint:gosec // python came from exec.LookPath
		t.Skipf("playwright cannot launch chromium for %s; skipping the browser execution-parity test "+
			"(run `%s -m playwright install chromium`)\n%s", python, python, out)
	}
}
