package report

import (
	"testing"
	"time"

	"artemis/pkg/result"
)

func TestJSONReportIDs(t *testing.T) {
	labelled := &result.StepResult{Name: "Pay", ID: "pay", Label: "Pay", Attempts: 1}
	labelled.Finish(time.Millisecond)
	bare := &result.StepResult{Name: "check_it", ID: "check_it", Attempts: 1}
	bare.Finish(time.Millisecond)
	sc := &result.ScenarioResult{
		Name: "With a coupon", ID: "with_coupon", Label: "With a coupon",
		Flow: "checkout", FlowLabel: "Checkout", File: "s.art",
		Steps: []*result.StepResult{labelled, bare},
	}
	sc.Finish(2 * time.Millisecond)
	run := &result.RunResult{StartedAt: startedAt, Scenarios: []*result.ScenarioResult{sc}}
	run.Finish()

	doc := decode(t, writeJSON(t, run))
	if v := doc["schema_version"]; v != float64(1) {
		t.Errorf("schema_version = %v, want 1", v)
	}
	s := doc["scenarios"].([]any)[0].(map[string]any)
	for k, want := range map[string]string{"id": "with_coupon", "label": "With a coupon", "name": "With a coupon", "flow": "checkout", "flow_label": "Checkout"} {
		if s[k] != want {
			t.Errorf("scenario %s = %v, want %q", k, s[k], want)
		}
	}
	steps := s["steps"].([]any)
	st := steps[1].(map[string]any)
	if st["id"] != "check_it" || st["label"] != "" || st["name"] != "check_it" {
		t.Errorf("bare step: id %v label %v name %v", st["id"], st["label"], st["name"])
	}
	if l, ok := st["label"]; !ok || l != "" {
		t.Errorf("a step with no label must still carry \"label\": \"\"")
	}
}
