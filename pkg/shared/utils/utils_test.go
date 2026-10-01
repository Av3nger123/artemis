package utils

import (
	"errors"
	"net/http"
	"testing"

	"artemis/pkg/shared/models"
)

// Slugify names the file `artemis generate` writes, so what it does to a
// collection name decides what a user has to type afterwards.
func TestSlugify(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already a slug", "orders", "orders"},
		{"spaces", "Order API", "order-api"},
		{"mixed case", "OrderAPI", "orderapi"},
		{"punctuation", "Orders: v2 (draft)", "orders-v2-draft"},
		{"a run of separators collapses", "a   ---   b", "a-b"},
		{"leading and trailing junk", "  ...Orders!  ", "orders"},
		{"digits are kept", "api-v2", "api-v2"},
		{"underscores are separators", "order_api", "order-api"},
		{"non-ascii", "Ördercollection", "rdercollection"},
		{"empty", "", ""},
		{"nothing usable", "---", ""},
		{"only punctuation", "!!!", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Slugify(c.in); got != c.want {
				t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// LogDecorator wraps the API call to time it. It must be transparent: whatever
// the wrapped function returns is what the caller gets, error or not.
func TestLogDecoratorPassesTheResultThrough(t *testing.T) {
	want := &http.Response{StatusCode: 204}
	var sawStep models.Step
	var sawConfig *map[string]interface{}

	wrapped := LogDecorator(func(s models.Step, c *map[string]interface{}) (*http.Response, error) {
		sawStep, sawConfig = s, c
		return want, nil
	})

	config := &map[string]interface{}{"k": "v"}
	got, err := wrapped(models.Step{Name: "ping"}, config)
	if err != nil {
		t.Fatalf("wrapped() = %v, want nil", err)
	}
	if got != want {
		t.Errorf("wrapped() = %#v, want the wrapped function's own return", got)
	}
	if sawStep.Name != "ping" {
		t.Errorf("the wrapped function saw step %q, want %q", sawStep.Name, "ping")
	}
	if sawConfig != config {
		t.Error("the wrapped function was handed a different config pointer")
	}
}

func TestLogDecoratorPassesTheErrorThrough(t *testing.T) {
	want := errors.New("boom")

	got, err := LogDecorator(func(models.Step, *map[string]interface{}) (*http.Response, error) {
		return nil, want
	})(models.Step{Name: "ping"}, &map[string]interface{}{})

	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if got != nil {
		t.Errorf("wrapped() = %#v, want nil alongside the error", got)
	}
}

// The decorator is generic; it has to work for a return type that is not a
// pointer, and must not swallow a zero value.
func TestLogDecoratorIsGenericOverTheReturnType(t *testing.T) {
	got, err := LogDecorator(func(models.Step, *map[string]interface{}) (int, error) {
		return 0, nil
	})(models.Step{}, &map[string]interface{}{})
	if err != nil || got != 0 {
		t.Fatalf("wrapped() = %v, %v; want 0, nil", got, err)
	}
}
