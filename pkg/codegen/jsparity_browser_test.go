//go:build browser

// The browser half of the JavaScript target's execution parity: the same
// comparison jsparity_test.go runs over testdata/conformance, over the browser
// flow in testdata/conformance/browser.
//
//	go test -tags browser ./pkg/codegen
//	make test-browser
//
// Tagged off by default for the reason parity_browser_test.go is: CI has no
// browser, and the first run on a cold machine downloads about 683 MB of
// Chromium for the Go side (docs/browser-engine.md) on top of Playwright's own
// browsers for each export's side. What the default suite still checks about
// this corpus is everything short of running it -- that it compiles, that every
// scenario declares an outcome, and that a browser flow is in the corpus at all.
//
// This is the test the design document's "Playwright on all three sides" claim
// cashes out as. Go drives playwright-go, the generated pytest drives
// playwright.sync_api and the generated vitest drives @playwright/test; the three
// bindings sit on one protocol, so the browser semantics are identical by
// construction -- and this is what says so out loud.
//
// Every page comes from the fixture server, on loopback, which is the same
// server the interpreter's browser tests drive.

package codegen

import (
	"os/exec"
	"testing"
)

// playwrightPackage is the npm package the generated module imports. Absent,
// this skips with the note nodeWith writes.
const playwrightPackage = "@playwright/test"

func TestJSBrowserExecutionParity(t *testing.T) {
	js := nodeWith(t, "vitest", playwrightPackage)
	requireNodeChromium(t, js)
	jsParity(t, browserConformanceDir, nil, playwrightPackage)
}

// requireNodeChromium skips when Playwright's own browsers have not been
// downloaded for this install.
//
// The package being there is not enough: `npm install @playwright/test` leaves a
// Playwright with no browser, and launching one then fails with a message about
// running `playwright install`. Without this probe that missing download would
// be reported as a divergence between the backends -- the most misleading failure
// this test could produce, since nothing about either backend is wrong.
//
// The probe is a launch and a close, because asking Playwright to start the
// browser is the only thing that answers the question it is actually asked later.
func requireNodeChromium(t *testing.T, js jsToolchain) {
	t.Helper()
	const probe = "import { chromium } from \"@playwright/test\";\n" +
		"const browser = await chromium.launch({ headless: true });\n" +
		"await browser.close();\n"
	cmd := exec.Command(js.node, "--input-type=module", "--eval", probe) //nolint:gosec // node came from exec.LookPath
	cmd.Dir = js.modules
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("playwright cannot launch chromium from %s; skipping the javascript browser "+
			"execution-parity test (run `npx playwright install chromium`)\n%s", js.modules, out)
	}
}
