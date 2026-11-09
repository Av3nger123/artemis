package mcpserver_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestListReportsIDsAndLabels(t *testing.T) {
	root := t.TempDir()
	for rel, src := range map[string]string{
		filepath.Join("collections", "admin_v2.art"): "collection admin as \"Admin\" {\n  request legacy_readiness as \"Legacy readiness\" {\n    get \"x\"\n  }\n\n  request plain {\n    get \"y\"\n  }\n}\n",
		filepath.Join("flows", "checkout.art"):       "flow checkout as \"Checkout\" {\n}\n",
		filepath.Join("scenarios", "checkout.art"):   "flow \"../flows/checkout.art\"\n\nscenario with_coupon as \"With a coupon\" {}\n\nscenario plain {}\n",
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	doc := structured(t, call(t, connect(t, root), "artemis_list", nil))
	raw, _ := json.Marshal(doc)
	var got struct {
		Collections      map[string][]string          `json:"collections"`
		CollectionLabels map[string]map[string]string `json:"collection_labels"`
		ScenarioFiles    map[string]struct {
			Flow           string            `json:"flow"`
			FlowLabel      string            `json:"flow_label"`
			Scenarios      []string          `json:"scenarios"`
			ScenarioLabels map[string]string `json:"scenario_labels"`
		} `json:"scenario_files"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	coll := filepath.Join("collections", "admin_v2.art")
	if items := got.Collections[coll]; len(items) != 2 || items[0] != "admin.legacy_readiness" || items[1] != "admin.plain" {
		t.Errorf("items = %q, want the collection's id as the prefix", items)
	}
	if l := got.CollectionLabels[coll]; len(l) != 1 || l["admin.legacy_readiness"] != "Legacy readiness" {
		t.Errorf("collection_labels = %v", got.CollectionLabels)
	}
	sf := got.ScenarioFiles[filepath.Join("scenarios", "checkout.art")]
	if sf.Flow != "checkout" || sf.FlowLabel != "Checkout" || len(sf.Scenarios) != 2 || sf.Scenarios[0] != "with_coupon" ||
		len(sf.ScenarioLabels) != 1 || sf.ScenarioLabels["with_coupon"] != "With a coupon" {
		t.Errorf("scenario file = %+v", sf)
	}
}
