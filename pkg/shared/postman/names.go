package postman

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"artemis/pkg/dsl/token"
)

// names is what the name pass worked out about a collection's `{{...}}`
// placeholders, before any request is translated.
//
// It exists because the two halves of the problem are not separable per
// request. A variable's new spelling has to be decided from the whole
// collection -- a rename that collides with another variable is an error, and
// it is not an error until both have been seen -- and a placeholder is only
// "undeclared" once every variable is known.
type names struct {
	// rename maps a Postman variable key to the DSL identifier it is written
	// as. A key that is already an identifier maps to itself.
	rename map[string]string

	// renamed is the keys that actually changed, sorted, for the file comment.
	renamed []string

	// undeclared is the identifiers a `{{name}}` referenced that no variable
	// defines, sorted. Each becomes `var name = env("NAME")`.
	undeclared []string
}

// resolve is the whole name pass over c.
//
// It renames every variable key that is not a DSL identifier, refuses a rename
// that would collide, then walks every templated string in the collection to
// find the placeholders that name nothing. A dynamic variable (`{{$guid}}`) is
// an error here rather than three functions later, because this is the one
// place that reads every placeholder in the file.
func resolve(c Collection) (names, error) {
	n := names{rename: map[string]string{}}

	// taken is every identifier already spoken for, so a rename cannot land on
	// one. The original key counts even when it is not an identifier: two keys
	// that differ only in a hyphen must not both become the same name.
	taken := map[string]string{}
	for _, v := range c.Variables {
		if v.Disabled || v.Key == "" {
			continue
		}
		taken[v.Key] = v.Key
	}

	for _, v := range c.Variables {
		if v.Disabled || v.Key == "" {
			continue
		}
		if _, done := n.rename[v.Key]; done {
			continue
		}
		ident := identifier(v.Key)
		if ident == v.Key {
			n.rename[v.Key] = v.Key
			continue
		}
		if ident == "" {
			return names{}, fmt.Errorf("the collection variable %q has no DSL name: a variable is a letter or "+
				"underscore followed by letters, digits and underscores", v.Key)
		}
		if other, clash := taken[ident]; clash && other != v.Key {
			return names{}, fmt.Errorf("the collection variable %q would be written %s here, which collides with "+
				"%q; rename one of them in Postman and export again", v.Key, ident, other)
		}
		n.rename[v.Key] = ident
		n.renamed = append(n.renamed, v.Key)
		taken[ident] = v.Key
	}
	sort.Strings(n.renamed)

	used, err := placeholders(c)
	if err != nil {
		return names{}, err
	}
	for _, name := range used {
		if _, declared := n.rename[name]; declared {
			continue
		}
		// A placeholder is matched against the keys as Postman wrote them, so
		// `{{base-url}}` is the declared `base-url` and resolves above. One
		// that reaches here names nothing in the collection.
		ident := identifier(name)
		if ident == "" {
			return names{}, fmt.Errorf("the placeholder {{%s}} has no DSL name, and no collection variable defines it", name)
		}
		if other, clash := taken[ident]; clash && other != name {
			return names{}, fmt.Errorf("the placeholder {{%s}} would be written %s here, which collides with the "+
				"collection variable %q", name, ident, other)
		}
		n.rename[name] = ident
		if ident != name {
			n.renamed = append(n.renamed, name)
		}
		n.undeclared = append(n.undeclared, ident)
		taken[ident] = name
	}
	sort.Strings(n.renamed)
	sort.Strings(n.undeclared)
	return n, nil
}

// apply rewrites every `{{key}}` in s to `{{ident}}`, so that migrate's own
// templated() -- which turns `{{name}}` into `${name}` and refuses a name the
// DSL cannot resolve -- sees only identifiers.
//
// A placeholder naming something resolve never saw is left alone, which makes
// it migrate's error rather than a silent pass-through: resolve walks the same
// strings, so this cannot happen without one of them being wrong.
func (n names) apply(s string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	var out strings.Builder
	rest := s
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			out.WriteString(rest)
			return out.String()
		}
		out.WriteString(rest[:open])
		body := rest[open+2:]
		end := strings.Index(body, "}}")
		if end < 0 {
			out.WriteString(rest[open:])
			return out.String()
		}
		key := strings.TrimSpace(body[:end])
		if ident, ok := n.rename[key]; ok {
			key = ident
		}
		out.WriteString("{{" + key + "}}")
		rest = body[end+2:]
	}
}

// identifier is key as a DSL identifier, or "" when there is nothing to make
// one out of.
//
// A character that cannot appear in a name becomes an underscore, which is what
// turns `base-url` into `base_url` and is the rewrite a reader would make by
// hand. A leading digit gets an underscore in front of it rather than being
// dropped, so `2fa_token` stays distinguishable from `fa_token`. A reserved
// word gets a trailing underscore: `step` is a name the DSL will not bind.
func identifier(key string) string {
	var b strings.Builder
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
			b.WriteByte(c)
		case c >= '0' && c <= '9':
			if b.Len() == 0 {
				b.WriteByte('_')
			}
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return ""
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "_" + out
	}
	if token.IsReserved(out) {
		out += "_"
	}
	return out
}

// envName is the environment variable an undeclared placeholder reads:
// `access_token` becomes ACCESS_TOKEN. Postman's own environments are named
// this way often enough that the guess is usually right, and when it is wrong
// the `var` line is one word to edit.
func envName(ident string) string {
	return strings.ToUpper(ident)
}

// placeholders is every distinct `{{...}}` name in the collection, sorted, so
// that the `var` lines a generated file opens with come out in the same order
// on every run.
//
// A dynamic variable -- `{{$guid}}`, `{{$timestamp}}`, `{{$randomInt}}` -- is
// an error. Postman computes those at send time and the DSL has no expression
// that does; emitting the literal text would send `{{$guid}}` as the value,
// which is a request that looks right and is wrong.
func placeholders(c Collection) ([]string, error) {
	seen := map[string]bool{}
	var out []string

	add := func(s, where string) error {
		for _, name := range scanPlaceholders(s) {
			if strings.HasPrefix(name, "$") {
				return fmt.Errorf("%s uses the Postman dynamic variable {{%s}}, which the DSL has no expression "+
					"for: replace it with a collection variable", where, name)
			}
			if name == "" {
				return fmt.Errorf("%s has an empty {{}} placeholder", where)
			}
			if strings.HasPrefix(name, "env.") {
				// migrate reads `{{env.NAME}}` as env("NAME") already; it is
				// not a variable reference and needs no `var` line.
				continue
			}
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
		return nil
	}

	for _, v := range c.Variables {
		if v.Disabled {
			continue
		}
		if err := add(v.Value, fmt.Sprintf("the collection variable %q", v.Key)); err != nil {
			return nil, err
		}
	}
	if err := walkAuth(c.Auth, "the collection's auth", add); err != nil {
		return nil, err
	}

	var walk func(items []Item, path []string) error
	walk = func(items []Item, path []string) error {
		for _, it := range items {
			// A fresh slice per level: appending to the caller's would have two
			// siblings sharing -- and overwriting -- the same backing array.
			here := append(append([]string{}, path...), it.Name)
			if it.Request != nil {
				if err := walkRequest(it.Request, "request "+strconv.Quote(stepName(here)), add); err != nil {
					return err
				}
			}
			if err := walk(it.Items, here); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(c.Items, nil); err != nil {
		return nil, err
	}

	sort.Strings(out)
	return out, nil
}

// walkRequest hands every templated string of one request to add.
func walkRequest(r *Request, where string, add func(s, where string) error) error {
	for _, s := range []string{r.URL.Raw, r.URL.Protocol} {
		if err := add(s, where); err != nil {
			return err
		}
	}
	for _, parts := range []StringParts{r.URL.Host, r.URL.Path} {
		for _, p := range parts {
			if err := add(p, where); err != nil {
				return err
			}
		}
	}
	for _, group := range [][]Entry{r.Header, r.URL.Query} {
		for _, e := range group {
			if e.Disabled {
				continue
			}
			if err := add(e.Key, where); err != nil {
				return err
			}
			if err := add(e.Value, where); err != nil {
				return err
			}
		}
	}
	if r.Body != nil && !r.Body.Disabled {
		if err := add(r.Body.Raw, where); err != nil {
			return err
		}
		for _, e := range r.Body.URLEncoded {
			if e.Disabled {
				continue
			}
			if err := add(e.Key, where); err != nil {
				return err
			}
			if err := add(e.Value, where); err != nil {
				return err
			}
		}
		if r.Body.GraphQL != nil {
			if err := add(r.Body.GraphQL.Query, where); err != nil {
				return err
			}
			if err := add(string(r.Body.GraphQL.Variables), where); err != nil {
				return err
			}
		}
	}
	return walkAuth(r.Auth, where+"'s auth", add)
}

// walkAuth hands an auth block's parameters to add, in sorted key order.
func walkAuth(a *Auth, where string, add func(s, where string) error) error {
	if a == nil {
		return nil
	}
	keys := make([]string, 0, len(a.Params))
	for k := range a.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := add(a.Params[k], where); err != nil {
			return err
		}
	}
	return nil
}

// scanPlaceholders is the names inside every `{{...}}` in s, in order, with an
// unclosed `{{` ignored -- migrate's interpString is the authority on that and
// reports it with the whole string in the message.
func scanPlaceholders(s string) []string {
	var out []string
	rest := s
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			return out
		}
		body := rest[open+2:]
		end := strings.Index(body, "}}")
		if end < 0 {
			return out
		}
		out = append(out, strings.TrimSpace(body[:end]))
		rest = body[end+2:]
	}
}
