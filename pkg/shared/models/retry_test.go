package models

import (
	"strings"
	"testing"
	"time"
)

// The retry policy the runner reads off a lowered step. The YAML spellings that
// used to produce one -- `retry: 5`, `retry: {times: 3, delay: "1s"}` -- are
// pkg/shared/migrate's now; what is left here is what Attempts and Wait mean,
// which is what pkg/cli/artrun.go's loop is built on.

func TestAttemptsIsAtLeastOne(t *testing.T) {
	for _, times := range []int{-3, 0, 1} {
		if got := (Retry{Times: times}).Attempts(); got != 1 {
			t.Errorf("Retry{Times: %d}.Attempts() = %d, want 1 -- a step is never attempted zero times", times, got)
		}
	}
	if got := (Retry{Times: 3}).Attempts(); got != 3 {
		t.Errorf("Retry{Times: 3}.Attempts() = %d, want 3 -- times is the total, not the retries after the first", got)
	}
}

func TestWaitReadsTheDelay(t *testing.T) {
	cases := []struct {
		delay string
		want  time.Duration
	}{
		{"", 0},
		{"500ms", 500 * time.Millisecond},
		{"1m30s", 90 * time.Second},
	}
	for _, c := range cases {
		got, err := (Retry{Delay: c.delay}).Wait()
		if err != nil {
			t.Fatalf("Retry{Delay: %q}.Wait() = %v, want nil", c.delay, err)
		}
		if got != c.want {
			t.Errorf("Retry{Delay: %q}.Wait() = %v, want %v", c.delay, got, c.want)
		}
	}
}

// A delay the runner cannot use fails the step before any attempt, so it has to
// be an error here rather than a silently-zero sleep.
func TestWaitRefusesWhatItCannotUse(t *testing.T) {
	for _, delay := range []string{"soon", "-1s"} {
		got, err := (Retry{Delay: delay}).Wait()
		if err == nil {
			t.Errorf("Retry{Delay: %q}.Wait() = %v, nil; want an error", delay, got)
			continue
		}
		if !strings.Contains(err.Error(), delay) {
			t.Errorf("error = %q, want it to quote the delay", err)
		}
	}
}
