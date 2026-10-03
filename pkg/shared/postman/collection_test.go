package postman

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Postman collection is an export from another tool, so every field this
// package reads has at least two spellings in the wild. These tests are the
// decoder's half of that: the translator below can assume one shape only
// because of them.

func TestURLDecodesBothSpellings(t *testing.T) {
	var asString URL
	if err := json.Unmarshal([]byte(`"https://api.test/ping?a=1"`), &asString); err != nil {
		t.Fatal(err)
	}
	if asString.Raw != "https://api.test/ping?a=1" {
		t.Errorf("Raw = %q, want the whole string", asString.Raw)
	}

	var asObject URL
	src := `{"raw":"https://api.test/o?a=1","protocol":"https","host":["api","test"],"path":["o"],
	         "query":[{"key":"a","value":"1"}]}`
	if err := json.Unmarshal([]byte(src), &asObject); err != nil {
		t.Fatal(err)
	}
	if asObject.Raw != "https://api.test/o?a=1" {
		t.Errorf("Raw = %q", asObject.Raw)
	}
	if len(asObject.Host) != 2 || asObject.Host[1] != "test" {
		t.Errorf("Host = %v, want the segments", asObject.Host)
	}
	if len(asObject.Query) != 1 || asObject.Query[0].Key != "a" {
		t.Errorf("Query = %+v, want one entry", asObject.Query)
	}
}

// v2.0 writes a URL's host and path as one string rather than segments.
func TestStringPartsDecodesAStringAsOneSegment(t *testing.T) {
	var u URL
	if err := json.Unmarshal([]byte(`{"host":"api.test","path":"/orders"}`), &u); err != nil {
		t.Fatal(err)
	}
	if len(u.Host) != 1 || u.Host[0] != "api.test" {
		t.Errorf("Host = %v, want one segment", u.Host)
	}
	if len(u.Path) != 1 || u.Path[0] != "/orders" {
		t.Errorf("Path = %v, want one segment", u.Path)
	}
}

// `"request": "<url>"` is the v2.0 shorthand for a GET.
func TestRequestDecodesTheURLShorthand(t *testing.T) {
	var r Request
	if err := json.Unmarshal([]byte(`"https://api.test/ping"`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Method != "GET" || r.URL.Raw != "https://api.test/ping" {
		t.Errorf("got %s %q, want GET of the string", r.Method, r.URL.Raw)
	}
}

// A raw header block is split, so the translator only ever sees entries. A
// line with no colon is not a header.
func TestHeadersDecodeTheRawBlock(t *testing.T) {
	var h Headers
	if err := json.Unmarshal([]byte(`"Accept: text/plain\nnonsense\nX-Trace: 1"`), &h); err != nil {
		t.Fatal(err)
	}
	want := Headers{{Key: "Accept", Value: "text/plain"}, {Key: "X-Trace", Value: "1"}}
	if len(h) != len(want) {
		t.Fatalf("got %+v, want %+v", h, want)
	}
	for i := range want {
		if h[i] != want[i] {
			t.Errorf("header %d = %+v, want %+v", i, h[i], want[i])
		}
	}
}

// Auth parameters are keyed by the auth's own type, and come as an array in
// v2.1 and an object in v2.0. Both flatten to the same map.
func TestAuthDecodesBothSpellings(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"v2.1 array", `{"type":"bearer","bearer":[{"key":"token","value":"t0ken","type":"string"}]}`},
		{"v2.0 object", `{"type":"bearer","bearer":{"token":"t0ken"}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var a Auth
			if err := json.Unmarshal([]byte(c.src), &a); err != nil {
				t.Fatal(err)
			}
			if a.Type != AuthBearer {
				t.Errorf("Type = %q, want %q", a.Type, AuthBearer)
			}
			if got := a.Param("token"); got != "t0ken" {
				t.Errorf("Param(\"token\") = %q, want %q", got, "t0ken")
			}
		})
	}
}

// A parameter that is not a string is rendered as text rather than failing the
// decode: the only thing done with it is to put it in a header.
func TestAuthParamTakesANonStringScalar(t *testing.T) {
	var a Auth
	if err := json.Unmarshal([]byte(`{"type":"apikey","apikey":[{"key":"n","value":7}]}`), &a); err != nil {
		t.Fatal(err)
	}
	if got := a.Param("n"); got != "7" {
		t.Errorf("Param(\"n\") = %q, want %q", got, "7")
	}
}

// Param on a nil Auth is the empty string, so the translator can ask about a
// request that declares none without a nil check at every call.
func TestAuthParamOnNil(t *testing.T) {
	var a *Auth
	if got := a.Param("token"); got != "" {
		t.Errorf("Param on a nil Auth = %q, want %q", got, "")
	}
}

// An unknown key is ignored, not rejected. A collection carries plenty artemis
// has no use for, and refusing one because of a `protocolProfileBehavior`
// would make the importer useless on real files.
func TestParseIgnoresWhatItDoesNotModel(t *testing.T) {
	path := write(t, "c.json", `{"info":{"name":"C","_postman_id":"x"},"protocolProfileBehavior":{"strictSSL":false},
		"item":[{"name":"p","request":{"method":"GET","url":"https://api.test/p"},"event":[{"listen":"test"}]}]}`)

	c, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse() = %v, want nil", err)
	}
	if c.Info.Name != "C" || len(c.Items) != 1 {
		t.Errorf("got %+v, want the one item", c)
	}
}

// A file that is not a collection names itself in the error: the file came out
// of another tool, so "which file" is half the answer.
func TestParseReportsTheFile(t *testing.T) {
	path := write(t, "broken.json", `{"info":`)

	if _, err := Parse(path); err == nil {
		t.Fatal("Parse() = nil, want an error")
	} else if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("error = %v, want the path in it", err)
	}
}

func TestParseReportsAMissingFile(t *testing.T) {
	if _, err := Parse(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("Parse() = nil, want an error")
	}
}

// write puts src in a fresh temp directory and returns the path.
func write(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
