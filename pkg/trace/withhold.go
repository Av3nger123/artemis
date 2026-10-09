package trace

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"artemis/pkg/shared/models"
)

// This file is everything a trace refuses to write down, and the one rule it
// follows: a value the scenario declared `secret`, or that a secret binding was
// read out of, is replaced by Redacted rather than omitted. Replaced, because a
// reader has to be able to tell "withheld" from "absent" -- a missing
// Authorization header and a withheld one are different bugs.

// sent is what the step did, in the shape its type gives it, already withheld.
//
// The three arms are the three action shapes models.Step has. A fourth step type
// adds an arm here and nothing else in this package.
func sent(step models.Step) map[string]any {
	s := step.Secrets
	switch step.Type {
	case "api":
		out := map[string]any{
			"method": step.Request.Method,
			"url":    hide(step.Request.URL, s.URL),
		}
		if len(step.Request.Headers) > 0 {
			headers := make(map[string]any, len(step.Request.Headers))
			for name, value := range step.Request.Headers {
				headers[name] = hide(value, s.Headers[name])
			}
			out["headers"] = headers
		}
		if step.Request.Body != "" {
			out["body"] = body(step.Request.Body, s)
		}
		return out

	case "terminal":
		out := map[string]any{"command": hide(step.Exec.Command, s.URL)}
		if len(step.Exec.Args) > 0 {
			out["args"] = hideList(step.Exec.Args, s.Args)
		}
		if step.Exec.Cwd != "" {
			out["cwd"] = hide(step.Exec.Cwd, s.URL)
		}
		if step.Exec.Stdin != "" {
			out["stdin"] = hide(step.Exec.Stdin, s.Stdin)
		}
		if len(step.Exec.Env) > 0 {
			env := make(map[string]any, len(step.Exec.Env))
			for name, value := range step.Exec.Env {
				env[name] = hide(value, s.Env[name])
			}
			out["env"] = env
		}
		return out

	case "browser":
		acts := make([]any, 0, len(step.Browser.Acts))
		for i, a := range step.Browser.Acts {
			act := map[string]any{"action": a.Name}
			if a.Target != "" {
				act["target"] = a.Target
			}
			if a.Value != "" {
				act["value"] = hide(a.Value, s.Acts[i])
			}
			acts = append(acts, act)
		}
		return map[string]any{"acts": acts}
	}
	return map[string]any{}
}

// body is the request body, withheld whole or field by field.
//
// A body marked whole is one string's worth of Redacted. A body with paths is
// decoded, the named pointers are replaced, and it is re-encoded -- so the
// readable fields stay readable, which is the point of having paths at all. A
// body that will not decode after all is withheld whole: it was supposed to be
// an object literal, and guessing is not worth the risk.
func body(raw string, s models.Secrets) any {
	if s.Body {
		return Redacted
	}
	if len(s.BodyPaths) == 0 {
		return raw
	}
	decoded, ok := decodeJSON(raw)
	if !ok {
		return Redacted
	}
	for _, pointer := range s.BodyPaths {
		decoded = replaceAt(decoded, pointer)
	}
	return decoded
}

// withheldRoots is the observation with the secret-capture marks applied: a root
// named whole becomes Redacted, and a root with pointers keeps everything the
// pointers do not name.
//
// This is the half ART-54 alone does not cover. A `secret capture token =
// body.data.access_token` withholds every later `Bearer ${token}`, and the token
// is still in this step's response body -- so without this the trace would hand
// back the credential the marking was about.
func withheldRoots(roots map[string]any, s models.Secrets) map[string]any {
	if len(s.ObservedRoots) == 0 && len(s.ObservedPaths) == 0 {
		return roots
	}
	out := make(map[string]any, len(roots))
	for name, value := range roots {
		switch {
		case s.ObservedRoots[name]:
			out[name] = Redacted
		case len(s.ObservedPaths[name]) > 0:
			withheld := value
			for _, pointer := range s.ObservedPaths[name] {
				withheld = replaceAt(withheld, pointer)
			}
			out[name] = withheld
		default:
			out[name] = value
		}
	}
	// `raw` and `body` are the same bytes read two ways, so withholding one and
	// printing the other would withhold nothing at all.
	if s.ObservedRoots["body"] || len(s.ObservedPaths["body"]) > 0 {
		if _, has := out["raw"]; has {
			out["raw"] = Redacted
		}
	}
	if s.ObservedRoots["raw"] || len(s.ObservedPaths["raw"]) > 0 {
		if _, has := out["body"]; has {
			out["body"] = Redacted
		}
	}
	return out
}

// replaceAt puts Redacted at an RFC 6901 pointer inside v, and returns v
// unchanged when the pointer addresses nothing.
//
// A pointer that does not resolve is not an error: the response is a run-time
// fact, and a capture that failed to read is already reported as an errored
// assertion. Withholding nothing is the right answer when there was nothing
// there to withhold.
func replaceAt(v any, pointer string) any {
	if pointer == "" {
		return Redacted
	}
	segments := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	return replaceSegments(v, segments)
}

func replaceSegments(v any, segments []string) any {
	if len(segments) == 0 {
		return Redacted
	}
	key := unescapePointer(segments[0])
	switch node := v.(type) {
	case map[string]any:
		child, has := node[key]
		if !has {
			return v
		}
		node[key] = replaceSegments(child, segments[1:])
		return node
	case []any:
		i, err := strconv.Atoi(key)
		if err != nil || i < 0 || i >= len(node) {
			return v
		}
		node[i] = replaceSegments(node[i], segments[1:])
		return node
	default:
		// The pointer went deeper than the value does.
		return v
	}
}

// unescapePointer reverses RFC 6901: `~1` is a slash and `~0` a tilde, in that
// order, because doing it the other way turns `~01` into a slash.
func unescapePointer(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	return strings.ReplaceAll(s, "~0", "~")
}

// hide returns Redacted when the mark says so, and the value otherwise.
func hide(value string, secret bool) any {
	if secret {
		return Redacted
	}
	return value
}

// hideList withholds a whole argument list, because models.Secrets.Args is one
// flag: `args = argv` is a single expression, so there is no position to name.
func hideList(values []string, secret bool) any {
	if secret {
		return Redacted
	}
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, v)
	}
	return out
}

// clamp cuts every string in a tree down to MaxBytes and names what it cut.
//
// The names are paths into the record -- "observed.raw" -- so a reader can tell
// which value is short and which is clipped. The cut is on bytes and then backed
// off to a rune boundary, so a clipped value is still valid UTF-8 and still
// valid JSON.
func clamp(m map[string]any, prefix string) (map[string]any, []string) {
	if m == nil {
		return nil, nil
	}
	var cut []string
	out := make(map[string]any, len(m))
	for k, v := range m {
		clamped, names := clampValue(v, prefix+"."+k)
		out[k] = clamped
		cut = append(cut, names...)
	}
	return out, cut
}

func clampValue(v any, path string) (any, []string) {
	switch node := v.(type) {
	case string:
		if len(node) <= MaxBytes {
			return node, nil
		}
		return truncate(node), []string{path}
	case map[string]any:
		var cut []string
		for k, child := range node {
			clamped, names := clampValue(child, path+"."+k)
			node[k] = clamped
			cut = append(cut, names...)
		}
		return node, cut
	case []any:
		var cut []string
		for i, child := range node {
			clamped, names := clampValue(child, fmt.Sprintf("%s[%d]", path, i))
			node[i] = clamped
			cut = append(cut, names...)
		}
		return node, cut
	}
	return v, nil
}

// truncate cuts s to MaxBytes on a rune boundary and says so in the value, so
// the file is readable without cross-referencing Truncated.
func truncate(s string) string {
	b := []byte(s)[:MaxBytes]
	for len(b) > 0 && !utf8ValidEnd(b) {
		b = b[:len(b)-1]
	}
	return string(b) + fmt.Sprintf("... [truncated, %d bytes total]", len(s))
}

// MinScrubbed is the shortest secret value Scrub will act on.
//
// A secret that resolved to "1" or to "" appears in almost any document, and
// replacing every occurrence of it would destroy the trace instead of protecting
// anything. Four bytes is the point below which a value is not distinctive
// enough to be worth chasing, and a credential short enough to fall under it has
// a bigger problem than its trace.
const MinScrubbed = 4

// scrub replaces every occurrence of each secret value in the record.
//
// This is the second of the two withholding layers, and it covers what the first
// cannot. A mark says where *this scenario* read a value from; it cannot say
// everywhere the value appears, because a response is run-time data. A service
// that echoes a token back in a later response has put the credential in a place
// no capture named, and only comparing against the value itself finds it.
//
// Imprecise on purpose, and that is affordable here: a trace is evidence rather
// than a comparison, so withholding a little too much costs a reader far less
// than leaking a credential. The report writers deliberately do not work this
// way -- there, blanking the wrong operand would hide the comparison being
// reported.
func scrub(rec Record, values []string) Record {
	keep := keepDistinct(values)
	if len(keep) == 0 {
		return rec
	}
	rec.Sent = scrubMap(rec.Sent, keep)
	rec.Observed = scrubMap(rec.Observed, keep)
	rec.Error = scrubString(rec.Error, keep)
	return rec
}

// keepDistinct drops the values too short to chase and sorts the rest longest
// first, so a value that contains another is replaced before its substring and
// the shorter one does not cut the longer in half.
func keepDistinct(values []string) []string {
	var out []string
	for _, v := range values {
		if len(v) >= MinScrubbed {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func scrubMap(m map[string]any, values []string) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = scrubValue(v, values)
	}
	return out
}

func scrubValue(v any, values []string) any {
	switch node := v.(type) {
	case string:
		return scrubString(node, values)
	case map[string]any:
		return scrubMap(node, values)
	case []any:
		for i, child := range node {
			node[i] = scrubValue(child, values)
		}
		return node
	}
	return v
}

func scrubString(s string, values []string) string {
	for _, v := range values {
		s = strings.ReplaceAll(s, v, Redacted)
	}
	return s
}
