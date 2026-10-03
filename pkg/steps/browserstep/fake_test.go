package browserstep

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// fakePage is the driver CI runs against.
//
// It is the second implementation of driver, and the reason the interface
// exists: with it, the act loop, the roots, the screenshot naming and -- in
// pkg/cli -- the settle loop are all covered by `go test ./...` on a machine
// with no Chromium and no 683 MB download. What it cannot prove is that
// playwright-go behaves the way page.go assumes; that is page_test.go's job,
// behind `-tags browser`.
//
// It records every call as a line, which is what the act tests assert on: a
// sequence of calls in order is exactly what a browser block is.
type fakePage struct {
	mu sync.Mutex

	// calls is every driver method called, in order, as "<method> <args>".
	calls []string

	// url and title are what the two roots read. A Goto rewrites url, so the
	// roots change the way they would in a browser.
	url   string
	title string

	// dom answers the element functions. A selector with no entry matches
	// nothing, which is how the empty-match answers are exercised.
	dom map[string]element

	// settle, when set for a selector, is how many reads of it come back
	// unsettled before it changes to after. It is how a waiting assertion is
	// tested with no browser and no clock: the element changes on the Nth
	// read rather than after N milliseconds.
	settle map[string]*settling

	// shots is every path Screenshot was asked to write.
	shots []string

	// fail, when set for a method name, is the error that method returns.
	fail map[string]error

	// timeout is the last SetTimeout, and timeouts every one of them, which is
	// what proves the act loop re-derives the step's remaining budget per act.
	timeout  time.Duration
	timeouts []time.Duration

	// waited is every Wait duration, so `wait "250ms"` is checked without
	// waiting 250ms.
	waited []time.Duration
}

// element is one selector's answers. A zero element is a present element with
// no text, no value and no attributes.
type element struct {
	text    string
	value   string
	attrs   map[string]string
	count   int
	hidden  bool
	missing bool // the selector matches nothing at all
}

// settling is an element that changes after n reads.
type settling struct {
	reads  int
	after  int
	before element
	then   element
}

func newFake() *fakePage {
	return &fakePage{
		url:    "about:blank",
		title:  "",
		dom:    map[string]element{},
		settle: map[string]*settling{},
		fail:   map[string]error{},
	}
}

// at puts the page somewhere, as a Goto would, without recording a call.
func (f *fakePage) at(url, title string) *fakePage {
	f.url, f.title = url, title
	return f
}

// with adds an element.
func (f *fakePage) with(selector string, el element) *fakePage {
	if el.count == 0 && !el.missing {
		el.count = 1
	}
	f.dom[selector] = el
	return f
}

// settles makes selector answer before for the first n reads and then after.
func (f *fakePage) settles(selector string, n int, before, after element) *fakePage {
	if before.count == 0 && !before.missing {
		before.count = 1
	}
	if after.count == 0 && !after.missing {
		after.count = 1
	}
	f.settle[selector] = &settling{after: n, before: before, then: after}
	return f
}

// breaks makes method return err.
func (f *fakePage) breaks(method string, err error) *fakePage {
	f.fail[method] = err
	return f
}

func (f *fakePage) record(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

// log is the recorded calls, one per line, for a test's error message.
func (f *fakePage) log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

func (f *fakePage) err(method string) error { return f.fail[method] }

// look resolves a selector to the element that answers it now, honouring a
// settling entry and counting the read.
func (f *fakePage) look(selector string) element {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.settle[selector]; ok {
		s.reads++
		if s.reads > s.after {
			return s.then
		}
		return s.before
	}
	if el, ok := f.dom[selector]; ok {
		return el
	}
	return element{missing: true}
}

// The driver implementation. Each method records itself, then either returns
// the error the test asked for or does the fake's version of the real thing.

func (f *fakePage) Goto(target string) error {
	f.record("goto %s", target)
	if err := f.err("Goto"); err != nil {
		return err
	}
	f.mu.Lock()
	f.url = target
	f.mu.Unlock()
	return nil
}

func (f *fakePage) Click(selector string) error {
	f.record("click %s", selector)
	return f.err("Click")
}

func (f *fakePage) Hover(selector string) error {
	f.record("hover %s", selector)
	return f.err("Hover")
}

func (f *fakePage) Fill(selector, value string) error {
	f.record("fill %s = %s", selector, value)
	return f.err("Fill")
}

func (f *fakePage) Select(selector, value string) error {
	f.record("select %s = %s", selector, value)
	return f.err("Select")
}

func (f *fakePage) Upload(selector, path string) error {
	f.record("upload %s = %s", selector, path)
	return f.err("Upload")
}

func (f *fakePage) Press(key string) error {
	f.record("press %s", key)
	return f.err("Press")
}

// Wait records the duration rather than sleeping it. A test of `wait "2s"`
// that took two seconds would be a test nobody runs.
func (f *fakePage) Wait(d time.Duration) {
	f.record("wait %s", d)
	f.mu.Lock()
	f.waited = append(f.waited, d)
	f.mu.Unlock()
}

func (f *fakePage) SetTimeout(d time.Duration) {
	f.mu.Lock()
	f.timeout = d
	f.timeouts = append(f.timeouts, d)
	f.mu.Unlock()
}

func (f *fakePage) URL() (string, error) {
	if err := f.err("URL"); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.url, nil
}

func (f *fakePage) Title() (string, error) {
	if err := f.err("Title"); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.title, nil
}

// The five element functions, with the empty-match answers page.go gets out of
// Playwright: null, null, null, 0, false.

func (f *fakePage) Text(selector string) (any, error) {
	if err := f.err("Text"); err != nil {
		return nil, err
	}
	el := f.look(selector)
	if el.missing {
		return nil, nil
	}
	return el.text, nil
}

func (f *fakePage) Value(selector string) (any, error) {
	if err := f.err("Value"); err != nil {
		return nil, err
	}
	el := f.look(selector)
	if el.missing {
		return nil, nil
	}
	return el.value, nil
}

func (f *fakePage) Attr(selector, name string) (any, error) {
	if err := f.err("Attr"); err != nil {
		return nil, err
	}
	el := f.look(selector)
	if el.missing {
		return nil, nil
	}
	v, ok := el.attrs[name]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (f *fakePage) Count(selector string) (any, error) {
	if err := f.err("Count"); err != nil {
		return nil, err
	}
	el := f.look(selector)
	if el.missing {
		return float64(0), nil
	}
	return float64(el.count), nil
}

func (f *fakePage) Visible(selector string) (any, error) {
	if err := f.err("Visible"); err != nil {
		return nil, err
	}
	el := f.look(selector)
	if el.missing {
		return false, nil
	}
	return !el.hidden, nil
}

func (f *fakePage) Screenshot(path string) error {
	f.record("screenshot %s", path)
	if err := f.err("Screenshot"); err != nil {
		return err
	}
	f.mu.Lock()
	f.shots = append(f.shots, path)
	f.mu.Unlock()
	return nil
}

var _ driver = (*fakePage)(nil)
