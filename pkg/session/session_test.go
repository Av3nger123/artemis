package session

import (
	"errors"
	"strings"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

// The one thing a step gets. Compile-time, so it is checked even when the
// browser-tagged tests are not run.
var _ = func(s *Session) playwright.Page { return s.Page() }

func TestParseViewportSize(t *testing.T) {
	size, err := parseViewport("1280x720")
	if err != nil {
		t.Fatalf("parseViewport: %v", err)
	}
	if size == nil {
		t.Fatal("parseViewport returned no size")
	}
	if size.Width != 1280 || size.Height != 720 {
		t.Fatalf("got %dx%d, want 1280x720", size.Width, size.Height)
	}
}

// An unset viewport is not an error and not a size: nil leaves Playwright its
// own default, so "the scenario said nothing" and "the scenario said 1280x720"
// stay different facts.
func TestParseViewportEmptyIsPlaywrightDefault(t *testing.T) {
	size, err := parseViewport("")
	if err != nil {
		t.Fatalf("parseViewport(\"\"): %v", err)
	}
	if size != nil {
		t.Fatalf("got %+v, want nil", size)
	}
}

func TestParseViewportRejects(t *testing.T) {
	for _, in := range []string{"1280", "axb", "0x0", "1280x0", "-1x720", "1280x720x1", "x720", "1280x"} {
		t.Run(in, func(t *testing.T) {
			if _, err := parseViewport(in); err == nil {
				t.Fatalf("parseViewport(%q) accepted it", in)
			} else if !strings.Contains(err.Error(), "config browser viewport") {
				t.Fatalf("error does not name the setting: %v", err)
			}
		})
	}
}

// Closing twice must not hand Playwright a closed handle twice, and a Session
// that never opened anything must still close cleanly -- both are reached by a
// registry that closes on a path it has already closed on.
func TestSessionCloseIsIdempotent(t *testing.T) {
	calls := 0
	s := &Session{closeFn: func() error { calls++; return nil }}

	if err := s.close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := s.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if calls != 1 {
		t.Fatalf("close ran %d times, want 1", calls)
	}
}

func TestNilSessionCloses(t *testing.T) {
	var s *Session
	if err := s.close(); err != nil {
		t.Fatalf("closing a nil session: %v", err)
	}
}

func TestSessionCloseReportsItsError(t *testing.T) {
	boom := errors.New("the browser would not exit")
	s := &Session{closeFn: func() error { return boom }}
	if err := s.close(); !errors.Is(err, boom) {
		t.Fatalf("got %v, want %v", err, boom)
	}
}

// Page is the only thing a step can reach. If an exported Close ever appears on
// *Session, the lifetime rule stops being enforced by the type and starts being
// a comment -- this test is here to fail when that happens.
func TestSessionHasNoExportedClose(t *testing.T) {
	var s any = &Session{}
	if _, ok := s.(interface{ Close() error }); ok {
		t.Fatal("*Session has an exported Close; only the registry may end a session")
	}
	if _, ok := s.(interface{ Close() }); ok {
		t.Fatal("*Session has an exported Close; only the registry may end a session")
	}
}
