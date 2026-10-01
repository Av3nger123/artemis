package models

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v2"
)

// parseStep decodes a step's YAML, which is how a retry policy ever reaches the
// runner.
func parseStep(t *testing.T, src string) Step {
	t.Helper()
	var step Step
	if err := yaml.Unmarshal([]byte(src), &step); err != nil {
		t.Fatalf("Unmarshal(%q) = %v, want nil", src, err)
	}
	return step
}

func TestRetryOmittedIsOneAttemptAndNoWait(t *testing.T) {
	step := parseStep(t, "name: ping\ntype: api\n")

	if got := step.Retry.Attempts(); got != 1 {
		t.Errorf("Attempts() = %d, want 1", got)
	}
	wait, err := step.Retry.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v, want nil", err)
	}
	if wait != 0 {
		t.Errorf("Wait() = %v, want 0 -- an omitted retry must not sleep", wait)
	}
}

func TestRetryScalarMeansTimes(t *testing.T) {
	step := parseStep(t, "name: ping\nretry: 5\n")

	if got := step.Retry.Attempts(); got != 5 {
		t.Errorf("Attempts() = %d, want 5 for `retry: 5`", got)
	}
	if wait, err := step.Retry.Wait(); err != nil || wait != 0 {
		t.Errorf("Wait() = %v, %v, want 0, nil", wait, err)
	}
}

func TestRetryMappingParsesTimesAndDelay(t *testing.T) {
	step := parseStep(t, "name: ping\nretry:\n  times: 3\n  delay: 250ms\n")

	if got := step.Retry.Attempts(); got != 3 {
		t.Errorf("Attempts() = %d, want 3", got)
	}
	wait, err := step.Retry.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v, want nil", err)
	}
	if wait != 250*time.Millisecond {
		t.Errorf("Wait() = %v, want 250ms", wait)
	}
}

func TestRetryAttemptsIsNeverZero(t *testing.T) {
	for _, times := range []int{0, -1, -100} {
		if got := (Retry{Times: times}).Attempts(); got != 1 {
			t.Errorf("Retry{Times: %d}.Attempts() = %d, want 1", times, got)
		}
	}
}

func TestRetryDelayOnlyStillAttemptsOnce(t *testing.T) {
	step := parseStep(t, "name: ping\nretry:\n  delay: 1s\n")

	if got := step.Retry.Attempts(); got != 1 {
		t.Errorf("Attempts() = %d, want 1 when only delay is given", got)
	}
}

func TestRetryBadDelayIsAnError(t *testing.T) {
	for _, delay := range []string{"soon", "2", "", "-1s"} {
		wait, err := Retry{Delay: delay}.Wait()
		if delay == "" {
			if err != nil || wait != 0 {
				t.Errorf("Retry{Delay: \"\"}.Wait() = %v, %v, want 0, nil", wait, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("Retry{Delay: %q}.Wait() = %v, nil, want an error", delay, wait)
			continue
		}
		if !strings.Contains(err.Error(), delay) {
			t.Errorf("Retry{Delay: %q}.Wait() error = %q, want it to name the value", delay, err)
		}
	}
}

func TestRetryUnparseableShapeIsAnError(t *testing.T) {
	var step Step
	err := yaml.Unmarshal([]byte("name: ping\nretry: [1, 2]\n"), &step)
	if err == nil {
		t.Fatal("Unmarshal() = nil, want an error for a list retry")
	}
	if !strings.Contains(err.Error(), "retry must be") {
		t.Errorf("error = %q, want it to say what retry may be", err)
	}
}
