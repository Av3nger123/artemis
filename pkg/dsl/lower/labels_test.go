package lower

import (
	"testing"

	"artemis/pkg/dsl/expand"
)

func TestLowerLabels(t *testing.T) {
	tree, info := expanded(t, expand.MapLoader{
		"main.art": "flow \"f.art\"\n\nscenario with_coupon as \"With a coupon\" {}\n",
		"f.art":    "flow checkout as \"Checkout\" {\n  step pay as \"Pay\" {\n    get \"x\"\n  }\n  step check_it {\n    get \"y\"\n  }\n}\n",
	})
	scs, err := File(tree, info)
	if err != nil {
		t.Fatal(err)
	}
	sc := scs[0]
	if sc.ID != "with_coupon" || sc.Label != "With a coupon" || sc.Name != "With a coupon" || sc.FlowName != "checkout" || sc.FlowLabel != "Checkout" {
		t.Fatalf("scenario %+v", sc)
	}
	pay, check := sc.Steps[0], sc.Steps[1]
	if pay.ID != "pay" || pay.Label != "Pay" || pay.Name != "Pay" {
		t.Errorf("pay: id %q label %q name %q", pay.ID, pay.Label, pay.Name)
	}
	if check.ID != "check_it" || check.Label != "" || check.Name != "check_it" {
		t.Errorf("check_it: id %q label %q name %q", check.ID, check.Label, check.Name)
	}
}
