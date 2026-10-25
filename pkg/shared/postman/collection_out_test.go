package postman

import (
	"errors"
	"strings"
	"testing"
)

// files is c as a collection file and a scenario, through Translate and
// CollectionFiles, as the command does it.
func files(t *testing.T, c Collection) Files {
	t.Helper()
	config, comments, err := Translate(c, source)
	if err != nil {
		t.Fatalf("Translate() = %v, want nil", err)
	}
	f, err := CollectionFiles(config, comments, "out/orders.collection.art")
	if err != nil {
		t.Fatalf("CollectionFiles() = %v, want nil", err)
	}
	return f
}

// The collection is titled as the Postman collection is, and the scenario
// uses it by its file's name: orders.collection.art is orders.
func TestEachRequestBecomesARequestAndAUse(t *testing.T) {
	c := Collection{
		Info:      Info{Name: "Orders API"},
		Variables: []Variable{{Key: "base-url", Value: "https://api.test"}},
		Items: []Item{
			{Name: "ping", Request: &Request{Method: "GET", URL: URL{Raw: "{{base-url}}/ping"}}},
			{Name: "Orders", Items: []Item{
				{Name: "list", Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test/orders"}}},
			}},
		},
	}
	f := files(t, c)
	for _, want := range []string{
		`collection "Orders API" {`,
		`request ping(base_url) {`,
		`get "${base_url}/ping"`,
		`request orders_list() {`,
		`get "https://api.test/orders"`,
	} {
		if !strings.Contains(f.Collection, want) {
			t.Errorf("collection does not hold %s:\n%s", want, f.Collection)
		}
	}
	for _, want := range []string{
		`import "orders.collection.art"`,
		`var base_url = "https://api.test"`,
		`use orders.ping { base_url = base_url }`,
		`use orders.orders_list`,
	} {
		if !strings.Contains(f.Scenario, want) {
			t.Errorf("scenario does not hold %s:\n%s", want, f.Scenario)
		}
	}
}

func TestTwoRequestsOfOneNameGetUniqueNames(t *testing.T) {
	get := Item{Name: "create", Request: &Request{Method: "POST", URL: URL{Raw: "https://api.test/x"}}}
	c := Collection{Info: Info{Name: "C"}, Items: []Item{get, get, get}}
	f := files(t, c)
	for _, want := range []string{"request create()", "request create_2()", "request create_3()"} {
		if !strings.Contains(f.Collection, want) {
			t.Errorf("collection does not hold %s:\n%s", want, f.Collection)
		}
	}
	for _, want := range []string{"use orders.create\n", "use orders.create_2\n", "use orders.create_3\n"} {
		if !strings.Contains(f.Scenario, want) {
			t.Errorf("scenario does not hold %q:\n%s", want, f.Scenario)
		}
	}
}

// A var named after an override word cannot be a parameter, because a use
// block reads that word as an override.
func TestAVarNamedLikeAnOverrideIsPassedUnderAnotherName(t *testing.T) {
	c := Collection{
		Info:      Info{Name: "C"},
		Variables: []Variable{{Key: "body", Value: "x"}},
		Items:     []Item{{Name: "r", Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test/{{body}}"}}}},
	}
	f := files(t, c)
	if !strings.Contains(f.Collection, "request r(body_value)") || !strings.Contains(f.Scenario, "body_value = body") {
		t.Errorf("collection:\n%s\nscenario:\n%s", f.Collection, f.Scenario)
	}
}

func TestACollectionWithNoRequestsIsRefused(t *testing.T) {
	c := Collection{Info: Info{Name: "C"}, Items: []Item{{Name: "empty folder"}}}
	config, comments, err := Translate(c, source)
	if err != nil {
		t.Fatalf("Translate() = %v, want nil", err)
	}
	if _, err := CollectionFiles(config, comments, "c.collection.art"); !errors.Is(err, ErrNoRequests) {
		t.Fatalf("CollectionFiles() = %v, want ErrNoRequests", err)
	}
}
