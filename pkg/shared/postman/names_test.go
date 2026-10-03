package postman

import (
	"strings"
	"testing"
)

// identifier is what decides whether a generated file parses at all: `var
// base-url = ...` is a syntax error in a file nobody wrote by hand.
func TestIdentifier(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"already a name", "base_url", "base_url"},
		{"a hyphen", "base-url", "base_url"},
		{"a dot", "api.host", "api_host"},
		{"spaces", "base url", "base_url"},
		{"mixed case is kept", "baseURL", "baseURL"},
		{"a leading digit", "2fa", "_2fa"},
		{"digits elsewhere", "v2token", "v2token"},
		{"a reserved word", "import", "import_"},
		{"a word reserved only after renaming", "for-", "for_"},
		{"non-ascii is dropped with its separators", "ürl", "rl"},
		{"nothing usable", "---", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := identifier(c.in); got != c.want {
				t.Errorf("identifier(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// A rename has to reach the placeholders too, or the file declares base_url
// and then interpolates a name nothing binds.
func TestResolveRenamesAndRewrites(t *testing.T) {
	c := Collection{
		Variables: []Variable{{Key: "base-url", Value: "https://api.test"}},
		Items:     []Item{{Name: "p", Request: &Request{Method: "GET", URL: URL{Raw: "{{base-url}}/ping"}}}},
	}

	n, err := resolve(c)
	if err != nil {
		t.Fatalf("resolve() = %v, want nil", err)
	}
	if n.rename["base-url"] != "base_url" {
		t.Errorf("rename = %v, want base-url to base_url", n.rename)
	}
	if got := n.apply("{{base-url}}/ping"); got != "{{base_url}}/ping" {
		t.Errorf("apply = %q, want the renamed placeholder", got)
	}
	if len(n.renamed) != 1 || n.renamed[0] != "base-url" {
		t.Errorf("renamed = %v, want the one key", n.renamed)
	}
	if len(n.undeclared) != 0 {
		t.Errorf("undeclared = %v, want none", n.undeclared)
	}
}

// Two keys that differ only in what has to be rewritten would land on one
// name, and then one variable's value would silently win. Refused.
func TestResolveRefusesARenameCollision(t *testing.T) {
	c := Collection{Variables: []Variable{
		{Key: "base-url", Value: "a"},
		{Key: "base_url", Value: "b"},
	}}

	_, err := resolve(c)
	if err == nil {
		t.Fatal("resolve() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "base-url") || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("error = %v, want both keys named", err)
	}
}

// A placeholder no variable defines is read from the environment. That is what
// Postman's own environments are, and it is the one translation that makes a
// collection referencing an environment check clean.
func TestResolveReadsAnUndeclaredPlaceholderFromTheEnvironment(t *testing.T) {
	c := Collection{Items: []Item{{Name: "p", Request: &Request{
		Method: "GET",
		URL:    URL{Raw: "https://api.test/ping"},
		Header: Headers{{Key: "Authorization", Value: "Bearer {{token}}"}},
	}}}}

	n, err := resolve(c)
	if err != nil {
		t.Fatalf("resolve() = %v, want nil", err)
	}
	if len(n.undeclared) != 1 || n.undeclared[0] != "token" {
		t.Errorf("undeclared = %v, want [token]", n.undeclared)
	}
	if got := envName("token"); got != "TOKEN" {
		t.Errorf("envName = %q, want TOKEN", got)
	}
}

// `{{env.NAME}}` is already migrate's spelling for env("NAME"). It is not a
// variable reference and must not get a `var` line of its own.
func TestResolveLeavesEnvPlaceholdersAlone(t *testing.T) {
	c := Collection{Items: []Item{{Name: "p", Request: &Request{
		Method: "GET", URL: URL{Raw: "{{env.API_URL}}/ping"},
	}}}}

	n, err := resolve(c)
	if err != nil {
		t.Fatalf("resolve() = %v, want nil", err)
	}
	if len(n.undeclared) != 0 {
		t.Errorf("undeclared = %v, want none", n.undeclared)
	}
}

// A dynamic variable is computed by Postman at send time. There is no DSL
// expression for one, and sending the literal text would be a request that
// looks right and is wrong.
func TestResolveRefusesADynamicVariable(t *testing.T) {
	cases := map[string]Collection{
		"in a URL": {Items: []Item{{Name: "p", Request: &Request{
			Method: "POST", URL: URL{Raw: "https://api.test/o/{{$guid}}"},
		}}}},
		"in a body": {Items: []Item{{Name: "p", Request: &Request{
			Method: "POST", URL: URL{Raw: "https://api.test/o"},
			Body: &Body{Mode: ModeRaw, Raw: `{"at": "{{$timestamp}}"}`},
		}}}},
		"in a variable's value": {Variables: []Variable{{Key: "id", Value: "{{$randomInt}}"}}},
		"in auth":               {Auth: &Auth{Type: AuthBearer, Params: map[string]string{"token": "{{$guid}}"}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := resolve(c)
			if err == nil {
				t.Fatal("resolve() = nil, want an error")
			}
			if !strings.Contains(err.Error(), "dynamic variable") {
				t.Errorf("error = %v, want it to name the dynamic variable", err)
			}
		})
	}
}

// A disabled variable is not sent and gets no `var` line.
func TestResolveSkipsADisabledVariable(t *testing.T) {
	c := Collection{Variables: []Variable{{Key: "gone", Value: "x", Disabled: true}}}

	n, err := resolve(c)
	if err != nil {
		t.Fatalf("resolve() = %v, want nil", err)
	}
	if _, ok := n.rename["gone"]; ok {
		t.Errorf("rename = %v, want the disabled variable left out", n.rename)
	}
}

// A variable key with nothing name-shaped in it cannot be renamed into
// anything, so it is an error rather than a `var _ = ...`.
func TestResolveRefusesAnUnnameableKey(t *testing.T) {
	c := Collection{Variables: []Variable{{Key: "---", Value: "x"}}}

	if _, err := resolve(c); err == nil {
		t.Fatal("resolve() = nil, want an error")
	}
}

// apply leaves an unclosed `{{` alone: migrate's interpString is the authority
// on that and reports it with the whole string in the message.
func TestApplyLeavesAnUnclosedPlaceholder(t *testing.T) {
	n := names{rename: map[string]string{"a-b": "a_b"}}
	if got := n.apply("x {{a-b}} y {{unclosed"); got != "x {{a_b}} y {{unclosed" {
		t.Errorf("apply = %q", got)
	}
}
