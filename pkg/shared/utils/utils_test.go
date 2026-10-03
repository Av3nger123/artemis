package utils

import (
	"testing"
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
