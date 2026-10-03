package postman

import (
	"strings"
	"testing"

	"artemis/pkg/shared/migrate"
)

// source is what Translate reads a generated file's provenance comment from.
const source = "orders.postman_collection.json"

// art is the collection as .art source, through the same path the command
// takes: Translate then migrate.Source. Asserting on the source rather than on
// the migrate.Config is deliberate -- the .art text is what a user gets, and a
// Config that holds the right fields in the wrong shape prints wrongly.
func art(t *testing.T, c Collection) string {
	t.Helper()
	config, comments, err := Translate(c, source)
	if err != nil {
		t.Fatalf("Translate() = %v, want nil", err)
	}
	src, err := migrate.Source(config, comments)
	if err != nil {
		t.Fatalf("migrate.Source() = %v, want nil", err)
	}
	return src
}

// refuse is the error Translate gives for c, which must not be nil.
func refuse(t *testing.T, c Collection) string {
	t.Helper()
	if _, _, err := Translate(c, source); err != nil {
		return err.Error()
	}
	t.Fatal("Translate() = nil, want an error")
	return ""
}

// folders wraps it in a chain of folders, innermost last, which is the shortest
// way to write a nested collection in the cases below.
func folders(path []string, it Item) Collection {
	node := it
	for i := len(path) - 1; i >= 0; i-- {
		node = Item{Name: path[i], Items: []Item{node}}
	}
	return Collection{Info: Info{Name: "C"}, Items: []Item{node}}
}

// The bug the issue is named for: the old importer looped over the top level
// only, so a collection whose requests all sat in folders produced no steps at
// all.
func TestNestedFoldersBecomeSteps(t *testing.T) {
	c := folders([]string{"Orders", "Admin"}, Item{
		Name:    "list orders",
		Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test/orders"}},
	})

	src := art(t, c)
	if !strings.Contains(src, `step "Orders / Admin / list orders"`) {
		t.Errorf("source does not carry the folder path:\n%s", src)
	}
	if !strings.Contains(src, `get "https://api.test/orders"`) {
		t.Errorf("source does not carry the request:\n%s", src)
	}
}

// Every request in the tree becomes a step, in collection order, however the
// folders are arranged -- including one nested under another request.
func TestEveryRequestInTheTreeBecomesAStep(t *testing.T) {
	get := func(name, path string) Item {
		return Item{Name: name, Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test" + path}}}
	}
	c := Collection{Info: Info{Name: "C"}, Items: []Item{
		get("top", "/t"),
		{Name: "Empty", Items: nil},
		{Name: "F", Items: []Item{get("in F", "/f"), {Name: "G", Items: []Item{get("in G", "/g")}}}},
	}}

	src := art(t, c)
	want := []string{`step "top"`, `step "F / in F"`, `step "F / G / in G"`}
	at := -1
	for _, w := range want {
		i := strings.Index(src, w)
		if i < 0 {
			t.Fatalf("%s is missing:\n%s", w, src)
		}
		if i < at {
			t.Errorf("%s is out of collection order:\n%s", w, src)
		}
		at = i
	}
	if strings.Contains(src, `"Empty"`) {
		t.Errorf("the empty folder produced a step:\n%s", src)
	}
}

// Headers come from the collection, in order, and the disabled ones do not.
// The old importer ignored request.header entirely and wrote one hardcoded
// Content-Type instead.
func TestHeadersAreCarriedInOrder(t *testing.T) {
	c := folders(nil, Item{Name: "p", Request: &Request{
		Method: "GET",
		URL:    URL{Raw: "https://api.test/p"},
		Header: Headers{
			{Key: "Accept", Value: "application/json"},
			{Key: "X-Trace", Value: "on", Disabled: true},
			{Key: "Accept", Value: "text/csv"},
		},
	}})

	src := art(t, c)
	if strings.Contains(src, "X-Trace") {
		t.Errorf("a disabled header was emitted:\n%s", src)
	}
	first := strings.Index(src, `header "Accept" = "application/json"`)
	second := strings.Index(src, `header "Accept" = "text/csv"`)
	if first < 0 || second < 0 || second < first {
		t.Errorf("the repeated header did not survive in order:\n%s", src)
	}
}

// url.query becomes query fields, and the raw URL loses its query string so
// lowering does not append the same parameters a second time.
func TestURLQueryBecomesQueryFields(t *testing.T) {
	c := folders(nil, Item{Name: "p", Request: &Request{
		Method: "GET",
		URL: URL{
			Raw: "https://api.test/o?limit=10&tag=new&cursor=abc",
			Query: []Entry{
				{Key: "limit", Value: "10"},
				{Key: "tag", Value: "new"},
				{Key: "cursor", Value: "abc", Disabled: true},
			},
		},
	}})

	src := art(t, c)
	if !strings.Contains(src, `get "https://api.test/o"`) {
		t.Errorf("the query string was left on the URL:\n%s", src)
	}
	for _, want := range []string{`query "limit" = "10"`, `query "tag" = "new"`} {
		if !strings.Contains(src, want) {
			t.Errorf("%s is missing:\n%s", want, src)
		}
	}
	if strings.Contains(src, "cursor") {
		t.Errorf("a disabled query parameter was emitted:\n%s", src)
	}
}

// A URL written as a plain string keeps its query string: it is the same
// request by a different spelling, and there is nothing to split it on.
func TestAStringURLKeepsItsQuery(t *testing.T) {
	c := folders(nil, Item{Name: "p", Request: &Request{
		Method: "GET", URL: URL{Raw: "https://api.test/o?limit=10"},
	}})

	if src := art(t, c); !strings.Contains(src, `get "https://api.test/o?limit=10"`) {
		t.Errorf("the URL changed:\n%s", src)
	}
}

func TestBodyModes(t *testing.T) {
	cases := []struct {
		name string
		body *Body
		want []string
		gone []string
	}{{
		name: "raw json becomes an object literal",
		body: &Body{Mode: ModeRaw, Raw: `{"sku":"A-1","qty":2}`, Options: &BodyOptions{Raw: &RawOptions{Language: "json"}}},
		want: []string{`body = {"qty": 2, "sku": "A-1"}`, `header "Content-Type" = "application/json"`},
	}, {
		name: "raw text is a string, and is not announced as JSON",
		body: &Body{Mode: ModeRaw, Raw: "plain words", Options: &BodyOptions{Raw: &RawOptions{Language: "text"}}},
		want: []string{`body = "plain words"`, `header "Content-Type" = "text/plain"`},
		gone: []string{"application/json"},
	}, {
		name: "raw with no language gets no Content-Type at all",
		body: &Body{Mode: ModeRaw, Raw: "plain words"},
		want: []string{`body = "plain words"`},
		gone: []string{"Content-Type"},
	}, {
		name: "urlencoded is a form-encoded string",
		body: &Body{Mode: ModeURLEncoded, URLEncoded: []Entry{
			{Key: "qty", Value: "3"},
			{Key: "note", Value: "rush order"},
			{Key: "void", Value: "1", Disabled: true},
		}},
		want: []string{`body = "note=rush+order&qty=3"`, `header "Content-Type" = "application/x-www-form-urlencoded"`},
		gone: []string{"void"},
	}, {
		name: "graphql is the document and its variables as JSON",
		body: &Body{Mode: ModeGraphQL, GraphQL: &GraphQL{Query: "{ orders { id } }", Variables: []byte(`{"tag":"new"}`)}},
		want: []string{`"query": "{ orders { id } }"`, `"variables": {"tag": "new"}`,
			`header "Content-Type" = "application/json"`},
	}, {
		name: "none is no body",
		body: &Body{Mode: ModeNone},
		gone: []string{"body ="},
	}, {
		name: "a disabled body is no body",
		body: &Body{Mode: ModeRaw, Raw: `{"a":1}`, Disabled: true},
		gone: []string{"body ="},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := art(t, folders(nil, Item{Name: "p", Request: &Request{
				Method: "POST", URL: URL{Raw: "https://api.test/o"}, Body: c.body,
			}}))
			for _, w := range c.want {
				if !strings.Contains(src, w) {
					t.Errorf("%s is missing:\n%s", w, src)
				}
			}
			for _, g := range c.gone {
				if strings.Contains(src, g) {
					t.Errorf("%s should not be there:\n%s", g, src)
				}
			}
		})
	}
}

// A request's own Content-Type wins over the one the mode implies: the
// collection said it, and Postman sends it.
func TestARequestsOwnContentTypeWins(t *testing.T) {
	src := art(t, folders(nil, Item{Name: "p", Request: &Request{
		Method: "POST",
		URL:    URL{Raw: "https://api.test/o"},
		Header: Headers{{Key: "content-type", Value: "application/vnd.api+json"}},
		Body:   &Body{Mode: ModeRaw, Raw: `{"a":1}`, Options: &BodyOptions{Raw: &RawOptions{Language: "json"}}},
	}}))

	if !strings.Contains(src, `header "content-type" = "application/vnd.api+json"`) {
		t.Errorf("the request's own header is missing:\n%s", src)
	}
	if strings.Contains(src, `"application/json"`) {
		t.Errorf("a second Content-Type was derived:\n%s", src)
	}
}

// A multipart or file body has no DSL request field, and guessing at one would
// send something the collection never said.
func TestBodyModesWithNoDSLSpelling(t *testing.T) {
	for _, mode := range []string{ModeFormData, ModeFile, "wat"} {
		t.Run(mode, func(t *testing.T) {
			msg := refuse(t, folders([]string{"F"}, Item{Name: "p", Request: &Request{
				Method: "POST", URL: URL{Raw: "https://api.test/o"}, Body: &Body{Mode: mode},
			}}))
			if !strings.Contains(msg, `"F / p"`) {
				t.Errorf("error = %s, want the request named", msg)
			}
		})
	}
}

func TestAuth(t *testing.T) {
	cases := []struct {
		name string
		auth *Auth
		want []string
	}{{
		name: "bearer",
		auth: &Auth{Type: AuthBearer, Params: map[string]string{"token": "t0ken"}},
		want: []string{`header "Authorization" = "Bearer t0ken"`},
	}, {
		name: "basic with literal credentials",
		auth: &Auth{Type: AuthBasic, Params: map[string]string{"username": "ada", "password": "lovelace"}},
		want: []string{`header "Authorization" = "Basic YWRhOmxvdmVsYWNl"`},
	}, {
		name: "apikey in a header by default",
		auth: &Auth{Type: AuthAPIKey, Params: map[string]string{"key": "X-Api-Key", "value": "k"}},
		want: []string{`header "X-Api-Key" = "k"`},
	}, {
		name: "apikey in the query",
		auth: &Auth{Type: AuthAPIKey, Params: map[string]string{"key": "api_key", "value": "k", "in": "query"}},
		want: []string{`query "api_key" = "k"`},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := art(t, Collection{Info: Info{Name: "C"}, Auth: c.auth, Items: []Item{
				{Name: "p", Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test/p"}}},
			}})
			for _, w := range c.want {
				if !strings.Contains(src, w) {
					t.Errorf("%s is missing:\n%s", w, src)
				}
			}
		})
	}
}

// Collection auth reaches every request, and a request's own auth replaces it.
// `noauth` on a request means no header, which is how a public endpoint inside
// an authenticated collection is written.
func TestRequestAuthOverridesTheCollections(t *testing.T) {
	get := func(name string, auth *Auth) Item {
		return Item{Name: name, Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test/" + name}, Auth: auth}}
	}
	src := art(t, Collection{
		Info: Info{Name: "C"},
		Auth: &Auth{Type: AuthBearer, Params: map[string]string{"token": "collection"}},
		Items: []Item{
			get("inherits", nil),
			get("overrides", &Auth{Type: AuthBearer, Params: map[string]string{"token": "own"}}),
			get("public", &Auth{Type: AuthNone}),
		},
	})

	for _, want := range []string{`"Bearer collection"`, `"Bearer own"`} {
		if !strings.Contains(src, want) {
			t.Errorf("%s is missing:\n%s", want, src)
		}
	}
	public := src[strings.Index(src, `step "public"`):]
	if strings.Contains(public, "Authorization") {
		t.Errorf("the noauth request got a header:\n%s", public)
	}
}

// There is no base64() in the DSL, so a templated credential cannot be encoded
// at run time, and encoding the literal text `{{user}}:{{pass}}` would send
// credentials nobody wrote.
func TestBasicAuthRefusesATemplatedCredential(t *testing.T) {
	msg := refuse(t, Collection{Info: Info{Name: "C"}, Items: []Item{{
		Name: "p",
		Request: &Request{
			Method: "GET", URL: URL{Raw: "https://api.test/p"},
			Auth: &Auth{Type: AuthBasic, Params: map[string]string{"username": "{{user}}", "password": "p"}},
		},
	}}})

	if !strings.Contains(msg, "base64") {
		t.Errorf("error = %s, want it to say why", msg)
	}
}

// Everything else needs a signature artemis does not compute.
func TestUnsupportedAuthTypesAreRefused(t *testing.T) {
	for _, kind := range []string{"oauth2", "awsv4", "digest", "hawk", "ntlm"} {
		t.Run(kind, func(t *testing.T) {
			msg := refuse(t, Collection{Info: Info{Name: "C"}, Auth: &Auth{Type: kind}, Items: []Item{
				{Name: "p", Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test/p"}}},
			}})
			if !strings.Contains(msg, kind) {
				t.Errorf("error = %s, want the auth type named", msg)
			}
		})
	}
}

// The status assertion. The old importer wrote 200 on every step, which fails
// on every 201; a request with no saved example gets the strongest honest
// claim instead.
func TestStatusComesFromTheSavedExample(t *testing.T) {
	src := art(t, Collection{Info: Info{Name: "C"}, Items: []Item{
		{Name: "created", Request: &Request{Method: "POST", URL: URL{Raw: "https://api.test/o"}},
			Response: []Response{{Name: "created", Code: 201}}},
		{Name: "unsaid", Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test/o"}}},
	}})

	for _, want := range []string{"expect status == 201", "expect status < 400"} {
		if !strings.Contains(src, want) {
			t.Errorf("%s is missing:\n%s", want, src)
		}
	}
}

// No retry. A collection says nothing about retrying a request, so neither
// does the file generated from one.
func TestNoRetryIsEmitted(t *testing.T) {
	src := art(t, folders(nil, Item{Name: "p", Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test/p"}}}))
	if strings.Contains(src, "retry") {
		t.Errorf("a retry was invented:\n%s", src)
	}
}

// A collection that does not name itself takes the file's name, because
// `scenario ""` parses and is a report nobody can read.
func TestScenarioNameFallsBackToTheFile(t *testing.T) {
	src := art(t, Collection{Items: []Item{
		{Name: "p", Request: &Request{Method: "GET", URL: URL{Raw: "https://api.test/p"}}},
	}})
	if !strings.Contains(src, `scenario "orders.postman_collection"`) {
		t.Errorf("scenario name is wrong:\n%s", src)
	}
}

// The provenance comment, the renames and the invented env vars are all things
// the reader cannot see from the output and has to know.
func TestTheFileCommentSaysWhatWasChanged(t *testing.T) {
	src := art(t, Collection{
		Info:      Info{Name: "C"},
		Variables: []Variable{{Key: "base-url", Value: "https://api.test"}},
		Items: []Item{{Name: "p", Request: &Request{
			Method: "GET", URL: URL{Raw: "{{base-url}}/p"},
			Header: Headers{{Key: "Authorization", Value: "Bearer {{token}}"}},
		}}},
	})

	for _, want := range []string{
		"# Generated by artemis generate from orders.postman_collection.json.",
		`# The Postman variable "base-url" is written base_url here`,
		"token from TOKEN",
		`var base_url = "https://api.test"`,
		`var token = env("TOKEN")`,
		`get "${base_url}/p"`,
		`header "Authorization" = "Bearer ${token}"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%s is missing:\n%s", want, src)
		}
	}
}

// The method is migrate's to validate -- it lowercases, checks token.IsMethod
// and defaults an empty one to GET -- so a verb artemis does not know is
// reported from there rather than guessed at here.
func TestAnUnknownMethodIsRefused(t *testing.T) {
	config, comments, err := Translate(folders(nil, Item{Name: "p", Request: &Request{
		Method: "TRACE", URL: URL{Raw: "https://api.test/p"},
	}}), source)
	if err != nil {
		t.Fatalf("Translate() = %v, want nil: the verb is migrate's to check", err)
	}
	if _, err := migrate.Source(config, comments); err == nil {
		t.Error("migrate.Source() = nil, want an error naming the method")
	}
}
