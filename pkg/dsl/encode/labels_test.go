package encode

import (
	"encoding/json"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
)

var labelledFiles = []string{
	"collection admin as \"Admin\" {\n  request legacy_readiness as \"Legacy readiness\" {\n    get \"x\"\n  }\n\n  command migrate {\n    run \"make\"\n  }\n}\n",
	"import \"c.art\"\n\nflow checkout as \"Checkout\" {\n  step pay as \"Pay\" {\n    get \"x\"\n  }\n\n  step check_it {\n    get \"y\"\n  }\n\n  step sign_in as \"Sign in\" = c.login as admin {}\n}\n",
	"flow \"f.art\"\n\nscenario with_coupon as \"With a coupon\" {}\n\nscenario plain {}\n",
}

func TestEncodeLabelRoundTrip(t *testing.T) {
	for _, src := range labelledFiles {
		tree, bag := parser.Parse("t.art", src)
		if bag.HasErrors() {
			t.Fatalf("%q: %v", src, bag.All())
		}
		if canon := print.Canonical(tree); canon != src {
			t.Errorf("canonical form changed the file:\n%s\n--- want ---\n%s", canon, src)
		}
		info, _ := check.Check(tree)
		doc, err := Encode(tree, info)
		if err != nil {
			t.Fatal(err)
		}
		back, err := Source(doc)
		if err != nil {
			t.Fatalf("%q: decoding: %v", src, err)
		}
		if back != src {
			t.Errorf("round trip changed the file:\n%s\n--- want ---\n%s", back, src)
		}
	}
}

func TestEncodeLabelShape(t *testing.T) {
	tree, _ := parser.Parse("t.art", "flow f {\n  step pay as \"Pay\" {\n    get \"x\"\n  }\n\n  step bare {\n    get \"y\"\n  }\n}\n")
	info, _ := check.Check(tree)
	doc, err := Encode(tree, info)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Scenarios []struct {
			Body []map[string]json.RawMessage `json:"body"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	steps := v.Scenarios[0].Body
	type quoted struct{ Text, Value string }
	var label, name quoted
	if err := json.Unmarshal(steps[0]["label"], &label); err != nil || label != (quoted{`"Pay"`, "Pay"}) {
		t.Errorf("label = %+v (%v)", label, err)
	}
	if _, ok := steps[1]["label"]; ok {
		t.Errorf("a step with no label has a label key")
	}
	if err := json.Unmarshal(steps[1]["name"], &name); err != nil || name != (quoted{"bare", "bare"}) {
		t.Errorf("name = %+v (%v)", name, err)
	}
}
