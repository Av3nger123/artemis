package session

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	playwright "github.com/mxschmitt/playwright-go"
)

// Config is `config browser { headless, viewport }`, resolved.
//
// It mirrors lower.Browser field for field and is deliberately not that type:
// pkg/dsl/lower is the front end, and a runtime package that imports it cannot
// be used by anything that did not come from a .art file. The runner maps one
// to the other in three lines.
//
// The zero Config is headed with Playwright's default viewport, which is why
// nothing constructs one: a caller that means "the defaults" writes
// Config{Headless: true}, matching BrowserConfig.Resolve, whose zero value for
// a scenario that wrote no config block is headless.
type Config struct {
	// Headless is whether the browser runs with no window.
	Headless bool

	// Viewport is the window size as the scenario wrote it ("1280x720"). Empty
	// means Playwright's own default, which is 1280x720 -- the same numbers,
	// but chosen by Playwright rather than by us, so a future Playwright that
	// changes its mind is not something artemis has to track.
	Viewport string
}

// Session is one scenario's live browser: a page, the context it is isolated
// in, and the process both live in.
//
// A step borrows one. There is no exported Close, so the only thing that can
// end a session is the Registry that opened it -- see the package comment.
type Session struct {
	browser playwright.Browser
	context playwright.BrowserContext
	page    playwright.Page

	// closeMu and closed make close idempotent: a Registry closing a session it
	// has already closed, or two goroutines unwinding at once, must not hand
	// Playwright a closed handle twice.
	closeMu sync.Mutex
	closed  bool

	// closeFn replaces the three real closes in tests, which is how the
	// lifetime rules are tested without a browser.
	closeFn func() error
}

// Page is the scenario's page. It is the same page on every call and it carries
// whatever earlier steps did to the DOM.
func (s *Session) Page() playwright.Page { return s.page }

// close ends the session.
//
// Page, then context, then browser: closing the browser first would make the
// other two error on a handle whose transport has gone. Every error is kept
// rather than returning the first, because "the page would not close" and "the
// browser process would not exit" are different failures and the second is the
// one that leaks.
//
// It is idempotent and safe on a zero Session, because a Registry that opened
// nothing still closes.
func (s *Session) close() error {
	if s == nil {
		return nil
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	if s.closeFn != nil {
		return s.closeFn()
	}

	var errs []error
	if s.page != nil {
		if err := s.page.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close page: %w", err))
		}
	}
	if s.context != nil {
		if err := s.context.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close browser context: %w", err))
		}
	}
	if s.browser != nil {
		if err := s.browser.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close browser: %w", err))
		}
	}
	return errors.Join(errs...)
}

// parseViewport turns "1280x720" into the size Playwright wants.
//
// An empty string is not an error and is not a size: it is "the scenario said
// nothing", and nil leaves Playwright its own default. Anything else that is
// not <width>x<height> in positive integers is an error naming the setting the
// way the scenario spelled it, because by the time this runs the checker has
// been and gone -- a bad viewport here came out of an expression
// (`viewport = env("SIZE")`) and only the run could have known.
func parseViewport(s string) (*playwright.Size, error) {
	if s == "" {
		return nil, nil
	}
	w, h, ok := strings.Cut(s, "x")
	if !ok {
		return nil, fmt.Errorf("config browser viewport is %q, want <width>x<height> like \"1280x720\"", s)
	}
	width, err := positiveInt(w)
	if err != nil {
		return nil, fmt.Errorf("config browser viewport %q: width: %w", s, err)
	}
	height, err := positiveInt(h)
	if err != nil {
		return nil, fmt.Errorf("config browser viewport %q: height: %w", s, err)
	}
	return &playwright.Size{Width: width, Height: height}, nil
}

// positiveInt is the half of parseViewport that both sides share. A zero
// dimension is rejected rather than passed on: Playwright treats 0 as "no
// viewport", which is a different setting from the one "0x720" asks for.
func positiveInt(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%d is not a positive number", n)
	}
	return n, nil
}
