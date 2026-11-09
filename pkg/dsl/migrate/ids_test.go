package migrate

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
)

func migrated(t *testing.T, in map[string]string) map[string]string {
	t.Helper()
	out, _, err := Files(in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func wantIn(t *testing.T, file, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s has no %q:\n%s", file, w, got)
		}
	}
}

func TestMigrateStepNames(t *testing.T) {
	out := migrated(t, map[string]string{
		"flows/f.art": "import \"../collections/auth.art\"\n\nflow f {\n  step \"Get a token\" {\n    get \"x\"\n  }\n\n  step \"log in\" = auth.login {}\n}\n",
	})
	wantIn(t, "flows/f.art", out["flows/f.art"],
		"step get_a_token as \"Get a token\" {", "step log_in as \"log in\" = auth.login {}")
}

func TestMigrateCollectionTitle(t *testing.T) {
	out := migrated(t, map[string]string{
		"collections/orders.art": "collection \"Orders API\" {\n  request create {\n    get \"x\"\n  }\n}\n",
	})
	wantIn(t, "collections/orders.art", out["collections/orders.art"], "collection orders as \"Orders API\" {")
}

func TestMigrateNameClash(t *testing.T) {
	in := map[string]string{
		"flows/f.art":     "flow f {\n  step \"a b\" {\n    get \"x\"\n  }\n\n  step \"a-b\" {\n    get \"y\"\n  }\n}\n",
		"scenarios/s.art": "flow \"../flows/f.art\"\n\nscenario \"a b\" {}\n\nscenario \"a-b\" {}\n",
	}
	out := migrated(t, in)
	wantIn(t, "flows/f.art", out["flows/f.art"], "step a_b as \"a b\" {", "step a_b_2 as \"a-b\" {")
	wantIn(t, "scenarios/s.art", out["scenarios/s.art"], "scenario a_b as \"a b\" {}", "scenario a_b_2 as \"a-b\" {}")
	u := front.CompileWith("scenarios/s.art", out["scenarios/s.art"], expand.MapLoader(out))
	if u.Bag.Len() != 0 {
		t.Errorf("the migrated files do not compile: %v", u.Bag.All())
	}
}

func TestMigrateIdempotent(t *testing.T) {
	once := migrated(t, map[string]string{
		"flows/f.art":            "flow f {\n  step \"Get a token\" {\n    get \"x\"\n  }\n}\n",
		"collections/orders.art": "collection \"Orders API\" {\n  request create {\n    get \"x\"\n  }\n}\n",
	})
	twice := migrated(t, once)
	for k, v := range once {
		if twice[k] != v {
			t.Errorf("%s changed on a second migration:\n%s\n--- was ---\n%s", k, twice[k], v)
		}
	}
}

// A quoted name that is already an id needs no label: it reads the same.
func TestMigrateNoRedundantLabel(t *testing.T) {
	out := migrated(t, map[string]string{
		"flows/f.art":            "flow f {\n  step \"pay\" {\n    get \"x\"\n  }\n}\n",
		"collections/orders.art": "collection \"orders\" {\n  request create {\n    get \"x\"\n  }\n}\n",
	})
	wantIn(t, "flows/f.art", out["flows/f.art"], "step pay {")
	wantIn(t, "collections/orders.art", out["collections/orders.art"], "collection orders {")
}

// A scope that mixes quoted names and ids reserves every id first, so a made
// id never takes one written later in the file.
func TestMigrateMixedScopeKeepsIDsUnique(t *testing.T) {
	out := migrated(t, map[string]string{
		"flows/f.art":     "flow f {\n  step \"Log in!\" {\n    get \"x\"\n  }\n\n  step log_in {\n    get \"y\"\n  }\n}\n",
		"scenarios/s.art": "flow \"../flows/f.art\"\n\nscenario \"Full price\" {}\n\nscenario \"full-price\" {}\n\nscenario full_price_2 {}\n",
	})
	wantIn(t, "flows/f.art", out["flows/f.art"], "step log_in_2 as \"Log in!\" {", "step log_in {")
	wantIn(t, "scenarios/s.art", out["scenarios/s.art"], "scenario full_price as \"Full price\"", "scenario full_price_3 as \"full-price\"", "scenario full_price_2 {}")
	u := front.CompileWith("scenarios/s.art", out["scenarios/s.art"], expand.MapLoader(out))
	if u.Bag.Len() != 0 {
		t.Errorf("the migrated files do not compile: %v", u.Bag.All())
	}
}

func compiles(t *testing.T, out map[string]string, scenarioFile string) {
	t.Helper()
	u := front.CompileWith(scenarioFile, out[scenarioFile], expand.MapLoader(out))
	if u.Bag.Len() != 0 {
		t.Errorf("the migrated files do not compile: %v\n%v", u.Bag.All(), out)
	}
}

// An item or flow named by an identifier that is not snake_case is renamed,
// and every step that calls the item follows.
func TestMigrateRenamesNonSnakeIDs(t *testing.T) {
	out := migrated(t, map[string]string{
		"collections/users.art": "collection users {\n  request getUser {\n    get \"x\"\n  }\n}\n",
		"flows/f.art":           "import \"../collections/users.art\"\n\nflow CheckoutFlow {\n  step load = users.getUser {}\n}\n",
		"scenarios/s.art":       "flow \"../flows/f.art\"\n\nscenario s {}\n",
	})
	wantIn(t, "collections/users.art", out["collections/users.art"], "request get_user {")
	wantIn(t, "flows/f.art", out["flows/f.art"], "flow checkout_flow {", "step load = users.get_user {}")
	compiles(t, out, "scenarios/s.art")
}

// A collection whose id is not the name its file was imported by keeps every
// import working: the import names it as it did.
func TestMigrateKeepsImportsResolving(t *testing.T) {
	out := migrated(t, map[string]string{
		"ordersAPI.art":   "collection \"Orders\" {\n  request create {\n    get \"x\"\n  }\n}\n",
		"f.art":           "import \"ordersAPI.art\"\n\nflow f {\n  step \"a\" = ordersAPI.create {}\n}\n",
		"scenarios/s.art": "flow \"../f.art\"\n\nscenario s {}\n",
	})
	wantIn(t, "ordersAPI.art", out["ordersAPI.art"], "collection orders_api as \"Orders\" {")
	wantIn(t, "f.art", out["f.art"], "import \"ordersAPI.art\" as ordersAPI")
	compiles(t, out, "scenarios/s.art")
}

// A collection file whose name cannot be an identifier was imported with as,
// which keeps working.
func TestMigrateKeepsAnImportAs(t *testing.T) {
	out := migrated(t, map[string]string{
		"Backend APIs.art": "collection \"Backend\" {\n  request ping {\n    get \"x\"\n  }\n}\n",
		"f.art":            "import \"Backend APIs.art\" as be\n\nflow f {\n  step \"p\" = be.ping {}\n}\n",
		"scenarios/s.art":  "flow \"../f.art\"\n\nscenario s {}\n",
	})
	wantIn(t, "Backend APIs.art", out["Backend APIs.art"], "collection backend_apis as \"Backend\" {")
	wantIn(t, "f.art", out["f.art"], "import \"Backend APIs.art\" as be\n")
	compiles(t, out, "scenarios/s.art")
}
