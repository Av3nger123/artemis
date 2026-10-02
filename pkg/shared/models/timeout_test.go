package models

import (
	"strings"
	"testing"
	"time"
)

const def = 30 * time.Second

func TestAttemptTimeoutFallsBackToTheDefault(t *testing.T) {
	cases := []struct {
		name string
		step Step
	}{
		{"absent", Step{}},
		{"zero", Step{Timeout: "0s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.step.AttemptTimeout(def)
			if err != nil {
				t.Fatalf("AttemptTimeout() error = %v, want nil", err)
			}
			if got != def {
				t.Errorf("AttemptTimeout() = %v, want the default %v -- there is no spelling of \"wait forever\"", got, def)
			}
		})
	}
}

func TestAttemptTimeoutUsesWhatTheStepAsksFor(t *testing.T) {
	cases := map[string]time.Duration{
		"5s":    5 * time.Second,
		"250ms": 250 * time.Millisecond,
		"1m30s": 90 * time.Second,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := Step{Timeout: in}.AttemptTimeout(def)
			if err != nil {
				t.Fatalf("AttemptTimeout() error = %v, want nil", err)
			}
			if got != want {
				t.Errorf("AttemptTimeout(%q) = %v, want %v", in, got, want)
			}
		})
	}
}

// A timeout that will not parse is the scenario's mistake, and the message has
// to name the value: "soon" is not a duration and nothing else in the file says
// so.
func TestAttemptTimeoutRejectsWhatIsNotADuration(t *testing.T) {
	cases := []struct {
		in     string
		wantIn string
	}{
		{"soon", `timeout "soon" is not a duration`},
		{"5", `timeout "5" is not a duration`},
		{"-1s", `timeout "-1s" is negative`},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := Step{Timeout: tc.in}.AttemptTimeout(def)
			if err == nil {
				t.Fatalf("AttemptTimeout(%q) = %v, nil; want an error", tc.in, got)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantIn)
			}
			if got != 0 {
				t.Errorf("AttemptTimeout() = %v alongside the error, want 0 -- no caller should send on it", got)
			}
		})
	}
}

// CheckTimeout answers only the question the runner asks: will this parse.
func TestCheckTimeout(t *testing.T) {
	for _, tc := range []struct {
		in      string
		wantErr bool
	}{{"", false}, {"5s", false}, {"0s", false}, {"soon", true}, {"-1s", true}} {
		err := Step{Timeout: tc.in}.CheckTimeout()
		if (err != nil) != tc.wantErr {
			t.Errorf("Step{Timeout: %q}.CheckTimeout() = %v, wantErr %v", tc.in, err, tc.wantErr)
		}
	}
}
