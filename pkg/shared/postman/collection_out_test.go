package postman

import (
	"errors"
	"strings"
	"testing"
)

// files is c as a collection file, a flow and a scenario file, through
// Translate and CollectionFiles, as the command does it.
func files(t *testing.T, c Collection) Files {
	t.Helper()
	config, comments, err := Translate(c, source)
	if err != nil {
		t.Fatalf("Translate() = %v, want nil", err)
	}
	f, err := CollectionFiles(config, comments, "out/orders.collection.art", "out/orders.flow.art")
	if err != nil {
		t.Fatalf("CollectionFiles() = %v, want nil", err)
	}
	return f
}

// The collection is titled as the Postman collection is, and the flow calls it
// by its file's name: orders.collection.art is orders. Each var a request
// reads is an input with no value, which the flow's var of that name supplies.
func TestEachRequestBecomesARequestAndAStep(t *testing.T) {
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
		`collection orders as "Orders API" {`,
		"  request ping {\n    var base_url\n\n    get \"${base_url}/ping\"\n",
		"  request orders_list {\n    get \"https://api.test/orders\"\n",
	} {
		if !strings.Contains(f.Collection, want) {
			t.Errorf("collection does not hold %s:\n%s", want, f.Collection)
		}
	}
	for _, want := range []string{
		"import \"orders.collection.art\"\n",
		"flow orders_api {\n",
		`  var base_url = "https://api.test"`,
		`  step ping = orders.ping {}`,
		`  step orders_list as "Orders / list" = orders.orders_list {}`,
	} {
		if !strings.Contains(f.Flow, want) {
			t.Errorf("flow does not hold %s:\n%s", want, f.Flow)
		}
	}
	if want := "flow \"orders.flow.art\"\n\nscenario orders_api as \"Orders API\" {}\n"; !strings.HasSuffix(f.Scenario, want) {
		t.Errorf("scenario file =\n%s\nwant it to end\n%s", f.Scenario, want)
	}
}

// The flat output is a flow with each request written in place, and the same
// scenario file. It has no collection.
func TestFlatFilesWriteTheStepsInTheFlow(t *testing.T) {
	c := Collection{
		Info:      Info{Name: "Orders API"},
		Variables: []Variable{{Key: "base-url", Value: "https://api.test"}},
		Items:     []Item{{Name: "ping", Request: &Request{Method: "GET", URL: URL{Raw: "{{base-url}}/ping"}}}},
	}
	config, comments, err := Translate(c, source)
	if err != nil {
		t.Fatal(err)
	}
	f, err := FlatFiles(config, comments, "out/orders.flow.art")
	if err != nil {
		t.Fatalf("FlatFiles() = %v, want nil", err)
	}
	if f.Collection != "" {
		t.Errorf("the flat output has a collection:\n%s", f.Collection)
	}
	want := "flow orders_api {\n  var base_url = \"https://api.test\"\n\n  step ping {\n    get \"${base_url}/ping\"\n"
	if !strings.Contains(f.Flow, want) || strings.Contains(f.Flow, "import") {
		t.Errorf("flow =\n%s\nwant it to hold\n%s", f.Flow, want)
	}
	if !strings.HasSuffix(f.Scenario, "flow \"orders.flow.art\"\n\nscenario orders_api as \"Orders API\" {}\n") {
		t.Errorf("scenario file =\n%s", f.Scenario)
	}
}

func TestTwoRequestsOfOneNameGetUniqueNames(t *testing.T) {
	get := Item{Name: "create", Request: &Request{Method: "POST", URL: URL{Raw: "https://api.test/x"}}}
	c := Collection{Info: Info{Name: "C"}, Items: []Item{get, get, get}}
	f := files(t, c)
	for _, want := range []string{"request create {", "request create_2 {", "request create_3 {"} {
		if !strings.Contains(f.Collection, want) {
			t.Errorf("collection does not hold %s:\n%s", want, f.Collection)
		}
	}
	for _, want := range []string{"= orders.create {}\n", "= orders.create_2 {}\n", "= orders.create_3 {}\n"} {
		if !strings.Contains(f.Flow, want) {
			t.Errorf("flow does not hold %q:\n%s", want, f.Flow)
		}
	}
}

// A var named after an override word cannot be an input under its own name,
// because a step block reads that word as an override, so the step passes it.
func TestAVarNamedLikeAnOverrideIsPassedUnderAnotherName(t *testing.T) {
	c := Collection{
		Info:      Info{Name: "C"},
		Variables: []Variable{{Key: "body", Value: "x"}},
		Items:     []Item{{Name: "r", Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test/{{body}}"}}}},
	}
	f := files(t, c)
	if !strings.Contains(f.Collection, "request r {\n    var body_value\n") || !strings.Contains(f.Flow, `step r = orders.r { body_value = body }`) {
		t.Errorf("collection:\n%s\nflow:\n%s", f.Collection, f.Flow)
	}
}

func TestACollectionWithNoRequestsIsRefused(t *testing.T) {
	c := Collection{Info: Info{Name: "C"}, Items: []Item{{Name: "empty folder"}}}
	config, comments, err := Translate(c, source)
	if err != nil {
		t.Fatalf("Translate() = %v, want nil", err)
	}
	if _, err := CollectionFiles(config, comments, "c.collection.art", "c.flow.art"); !errors.Is(err, ErrNoRequests) {
		t.Fatalf("CollectionFiles() = %v, want ErrNoRequests", err)
	}
}
