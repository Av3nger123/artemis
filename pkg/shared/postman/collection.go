// Package postman reads a Postman collection and translates it into the shape
// `artemis migrate` translates, so that `artemis generate` writes `.art`
// through the same canonical printer rather than a format of its own.
//
//	artemis generate -f collection.json [-o scenario.art]
//
// # Why it goes through migrate
//
// Translate returns a migrate.Config and a migrate.Comments, and the caller
// hands both to migrate.Source. Quoting, `{{name}}` becoming `${name}`, a JSON
// body becoming an object literal, the action line and the parse-then-print
// round trip are all already there and already pinned by ART-39's goldens.
// Nothing in this package writes a brace.
//
// # What it refuses
//
// A Postman construct with no DSL spelling is an error naming the request: a
// `formdata` or `file` body, an auth type that is not bearer, basic or apikey,
// a dynamic variable like `{{$guid}}`, a variable rename that would collide
// with another variable. This is migrate's own stance -- a half-translated
// collection produces a suite that runs and tests something else, which is
// worse than one that will not convert.
//
// # What it does not read
//
// `item.event[]` -- Postman's pre-request and test scripts -- is decoded and
// ignored. The DSL has no spelling for arbitrary JavaScript and recovering the
// assertions inside a `pm.test` would need a JS parser. A Postman environment
// file is not read either; an undeclared `{{name}}` becomes `env("NAME")`,
// which is the seam an environment import would fill.
package postman

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Collection is a Postman collection, v2.0 or v2.1.
//
// Only what translates is modelled. A key that is not here is ignored rather
// than rejected: a collection is an export from another tool, and refusing one
// because it carries a `protocolProfileBehavior` would make the importer
// useless on real files. That is the opposite of migrate's strict decode, and
// deliberately so -- migrate reads a format artemis defined, this reads one it
// did not.
type Collection struct {
	Info Info `json:"info"`
	// Items are the collection's top level, each either a folder (Items set)
	// or a request (Request set).
	Items []Item `json:"item"`
	// Variables are the collection variables, which a request references as
	// `{{key}}`.
	Variables []Variable `json:"variable"`
	// Auth is the collection-level auth, applied to every request that does
	// not declare its own.
	Auth *Auth `json:"auth"`
}

type Info struct {
	Name string `json:"name"`
}

type Variable struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled"`
}

// Item is one node of the collection tree: a folder when Items is set, a
// request when Request is set.
//
// Postman allows both on one node and neither on another. A node with a
// Request is a request and its Items are walked too, so nothing is lost by a
// collection that nests under a request; a node with neither contributes
// nothing.
type Item struct {
	Name     string     `json:"name"`
	Items    []Item     `json:"item"`
	Request  *Request   `json:"request"`
	Response []Response `json:"response"`
}

// Request is what one item sends.
//
// UnmarshalJSON accepts the v2.0 shorthand in which the whole request is a URL
// string: `"request": "https://api.test/ping"` is a GET of that URL.
type Request struct {
	Method string  `json:"method"`
	Header Headers `json:"header"`
	URL    URL     `json:"url"`
	Body   *Body   `json:"body"`
	Auth   *Auth   `json:"auth"`
}

func (r *Request) UnmarshalJSON(data []byte) error {
	if url, ok := jsonString(data); ok {
		*r = Request{Method: "GET", URL: URL{Raw: url}}
		return nil
	}
	type plain Request
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*r = Request(p)
	return nil
}

// Headers is a request's headers.
//
// v2.1 writes an array of entries. v2.0 allows one string holding the raw
// header block, `"Accept: application/json\nX-Trace: 1"`, which is split here
// so the rest of the package only ever sees entries.
type Headers []Entry

func (h *Headers) UnmarshalJSON(data []byte) error {
	if raw, ok := jsonString(data); ok {
		*h = parseRawHeaders(raw)
		return nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	*h = entries
	return nil
}

// parseRawHeaders splits a raw header block into entries. A line with no colon
// is not a header and is skipped.
func parseRawHeaders(raw string) Headers {
	var out Headers
	for _, line := range strings.Split(raw, "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, Entry{Key: strings.TrimSpace(name), Value: strings.TrimSpace(value)})
	}
	return out
}

// Entry is one key/value pair in a header, query or urlencoded array.
//
// Disabled is the unchecked box in Postman's UI. A disabled entry is not sent,
// so it is not emitted: a header the collection's author switched off must not
// come back on because the collection was imported.
type Entry struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled"`
}

// URL is a request's URL, which Postman writes either as a string or as an
// object with the parts broken out.
//
// Raw is the whole URL including its query string. Query is the same query
// parameters as entries, which is the form the DSL writes; when both are
// present Raw's query string is dropped in favour of Query, because lowering
// appends the `query` fields to whatever the URL already carries and sending
// each parameter twice is not what the collection says.
type URL struct {
	Raw      string      `json:"raw"`
	Protocol string      `json:"protocol"`
	Host     StringParts `json:"host"`
	Path     StringParts `json:"path"`
	Query    []Entry     `json:"query"`
}

func (u *URL) UnmarshalJSON(data []byte) error {
	if raw, ok := jsonString(data); ok {
		*u = URL{Raw: raw}
		return nil
	}
	type plain URL
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*u = URL(p)
	return nil
}

// StringParts is a URL's host or path: an array of segments, or one string
// holding them already joined.
type StringParts []string

func (s *StringParts) UnmarshalJSON(data []byte) error {
	if one, ok := jsonString(data); ok {
		*s = StringParts{one}
		return nil
	}
	var parts []string
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	*s = parts
	return nil
}

// The body modes Postman writes. The first three translate; formdata and file
// do not, and Translate says so naming the request.
const (
	ModeRaw        = "raw"
	ModeURLEncoded = "urlencoded"
	ModeGraphQL    = "graphql"
	ModeFormData   = "formdata"
	ModeFile       = "file"
	ModeNone       = "none"
)

// Body is a request's body. Which field holds it is said by Mode.
type Body struct {
	Mode       string          `json:"mode"`
	Raw        string          `json:"raw"`
	URLEncoded []Entry         `json:"urlencoded"`
	FormData   []Entry         `json:"formdata"`
	GraphQL    *GraphQL        `json:"graphql"`
	Options    *BodyOptions    `json:"options"`
	Disabled   bool            `json:"disabled"`
	File       json.RawMessage `json:"file"`
}

// Language is the `raw` body's declared language, which is what Postman sends
// a Content-Type from when the request declares no header of its own.
func (b *Body) Language() string {
	if b == nil || b.Options == nil || b.Options.Raw == nil {
		return ""
	}
	return b.Options.Raw.Language
}

type BodyOptions struct {
	Raw *RawOptions `json:"raw"`
}

type RawOptions struct {
	Language string `json:"language"`
}

// GraphQL is a `graphql` body: the document and its variables. Variables is
// raw JSON because Postman writes it as either an object or a string holding
// one.
type GraphQL struct {
	Query     string          `json:"query"`
	Variables json.RawMessage `json:"variables"`
}

// The auth types that translate to header or query fields.
const (
	AuthBearer = "bearer"
	AuthBasic  = "basic"
	AuthAPIKey = "apikey"
	AuthNone   = "noauth"
)

// Auth is an auth block, at the collection or at a request.
//
// Postman keys the parameters by the type: a bearer block holds `"bearer": [{
// "key": "token", "value": "..." }]`. Params is every such array flattened,
// keyed by the parameter's own key, because the type is already in Type and no
// two types appear in one block.
type Auth struct {
	Type   string
	Params map[string]string
}

// Param is the value of the named auth parameter, or "".
func (a *Auth) Param(name string) string {
	if a == nil {
		return ""
	}
	return a.Params[name]
}

// UnmarshalJSON reads the type and flattens the parameters.
//
// Both spellings are accepted: v2.1's array of `{key, value}` and v2.0's
// object of `{key: value}`. A value that is not a string -- apikey's `in` is
// sometimes written as one, and a boolean turns up in the wild -- is rendered
// as text rather than rejected, because the only thing done with it is to put
// it in a header.
func (a *Auth) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	a.Type = ""
	a.Params = map[string]string{}
	if t, ok := raw["type"]; ok {
		if s, ok := jsonString(t); ok {
			a.Type = s
		}
	}

	block, ok := raw[a.Type]
	if !ok || a.Type == "" {
		return nil
	}

	var entries []authEntry
	if err := json.Unmarshal(block, &entries); err == nil {
		for _, e := range entries {
			a.Params[e.Key] = scalarString(e.Value)
		}
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(block, &object); err == nil {
		for key, value := range object {
			a.Params[key] = scalarString(value)
		}
	}
	return nil
}

type authEntry struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// Response is a saved example response. Code is the status it was recorded
// with, and is what the generated step asserts.
type Response struct {
	Name string `json:"name"`
	Code int    `json:"code"`
}

// Parse reads a collection from path.
//
// It replaces shared.ParsePostmanJSON. The error is the decoder's, with the
// path in it: the file is something the user exported from another tool, so
// "which file" is half the answer.
func Parse(path string) (Collection, error) {
	var c Collection
	raw, err := os.ReadFile(path) //nolint:gosec // the path is the one the user named
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("%s is not a Postman collection artemis can read: %w", path, err)
	}
	return c, nil
}

// jsonString reports whether data is a JSON string, and if so its value. It is
// how every string-or-something field here tells the two apart.
func jsonString(data []byte) (string, bool) {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return "", false
	}
	return s, true
}

// scalarString is a JSON scalar as text: a string unquoted, anything else as it
// was written. A null is the empty string.
func scalarString(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	if s, ok := jsonString(data); ok {
		return s
	}
	if string(data) == "null" {
		return ""
	}
	return string(data)
}
