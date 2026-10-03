package session

import (
	"fmt"
	"sync"

	playwright "github.com/mxschmitt/playwright-go"
)

// The driver -- a Node process running Playwright's CLI -- is per *process*,
// not per scenario. Starting it costs an install check and a pipe; starting it
// twenty times for twenty scenarios costs twenty of those and twenty Node
// processes. The browser, which is per scenario, is the thing scenarios must
// not share: two scenarios can disagree about `headless`.
//
// Install lives here rather than anywhere earlier because of the one rule this
// package exists to keep: a run with no browser step in it must download
// nothing. start is called from exactly one place -- Registry.open -- and
// Registry.open is called from exactly one place: the first step that asks for
// a session.

// driver is the process's Playwright driver. A plain mutex rather than a
// sync.Once because Shutdown has to be able to put it back to "not started":
// a process that stops the driver and later opens another session must start
// another one, not get a nil back from a Once that has already fired.
var driver struct {
	mu      sync.Mutex
	started bool
	pw      *playwright.Playwright
	err     error
}

// start installs the driver if this machine has never run one, starts it, and
// returns it. Later calls return what the first one got.
//
// The error is cached along with the result. A driver that will not install --
// no network, a CI image with no egress -- takes tens of seconds to fail, and a
// run of thirty browser scenarios that each retried it would hang for a quarter
// of an hour before reporting the failure the first scenario already knew.
//
// Only chromium is installed, which is full Chromium *and* the headless shell.
// Installing just the shell would halve the download and break
// `config browser { headless = false }`.
//
// The whole of it is under the mutex, including the download. A second
// scenario that asks while the first is still downloading waits, rather than
// starting a second install into the same directory.
func start() (*playwright.Playwright, error) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if driver.started {
		return driver.pw, driver.err
	}
	driver.started = true

	if err := playwright.Install(&playwright.RunOptions{
		Browsers: []string{"chromium"},
	}); err != nil {
		driver.err = fmt.Errorf("install the playwright driver and chromium: %w", err)
		return nil, driver.err
	}
	pw, err := playwright.Run()
	if err != nil {
		driver.err = fmt.Errorf("start the playwright driver: %w", err)
		return nil, driver.err
	}
	driver.pw = pw
	return driver.pw, nil
}

// Shutdown stops the driver this process started.
//
// It is a no-op when nothing ever opened a session, which is the normal case:
// an api-only run may call it unconditionally, and that is the point -- the
// runner defers it once at the end of the run without having to know whether
// any scenario turned out to contain a browser step.
//
// It stops the Node process, not any browser: by the time it runs, every
// scenario's own session has already been closed by its registry.
func Shutdown() error {
	driver.mu.Lock()
	pw := driver.pw
	driver.pw = nil
	driver.err = nil
	driver.started = false
	driver.mu.Unlock()

	if pw == nil {
		return nil
	}
	if err := pw.Stop(); err != nil {
		return fmt.Errorf("stop the playwright driver: %w", err)
	}
	return nil
}
