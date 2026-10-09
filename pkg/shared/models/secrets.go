package models

// Secrets names the parts of a step whose values came from a secret binding.
//
// This is reporting metadata and nothing else. The request Artemis sends is the
// same with it and without it, and only pkg/report reads it. ART-54.
//
// The marks are kept beside the values rather than inside them. The values are
// in the evaluator's domain -- see pkg/eval/value.go, "the value domain is
// JSON's" -- and a wrapper type there would reach every switch in pkg/eval.
//
// A map is keyed the way the value it describes is keyed, so a reader needs no
// second index: Headers by header name, as Request.Headers is, Env by variable
// name, as Exec.Env is, and Acts by position in Browser.Acts.
type Secrets struct {
	// URL is true when the URL of an api step, or the command of a terminal
	// step, came from a secret binding.
	//
	// A secret `query` value also sets this, and that is not an approximation:
	// Request.Model folds the query into the URL, so the URL string is where
	// the value actually ends up and there is no separate field to mark.
	URL bool

	// Headers marks one header value each, by the name it was sent under.
	Headers map[string]bool

	// Body is true when the whole body must be withheld. A body that is an
	// object literal gets BodyPaths instead, so one secret field does not blank
	// a body a reader needs.
	Body bool

	// BodyPaths are RFC 6901 JSON pointers into an object-literal body, one per
	// field that read a secret binding. Empty when Body is true, and empty when
	// nothing in the body is secret.
	BodyPaths []string

	// Env marks one environment setting each, by name: `env { PGPASSWORD = pw }`.
	Env map[string]bool

	// Args is true when a terminal step's argument list read a secret binding.
	// It is one flag and not a set of positions, because the list is a single
	// expression -- `args = argv` is legal -- so a position is not always a
	// thing that exists.
	Args bool

	// Stdin is true when a terminal step's stdin read a secret binding.
	Stdin bool

	// Acts marks one browser act's value each, by its position in Browser.Acts:
	// `fill "#password" = pw`.
	Acts map[int]bool

	// ObservedRoots names roots a trace must withhold whole, by the name the
	// step type binds them under: "body", "raw", "stdout".
	//
	// This is the half of the problem the request marks above do not reach. A
	// `secret capture token = body.data.access_token` withholds every later
	// `Bearer ${token}`, but the token's *origin* is this step's response body,
	// and a trace that printed that body would make the marking theatre.
	//
	// A root is withheld whole when the capture that read it is not addressable
	// -- `match(raw, /tok=(\w+)/)` names no location.
	ObservedRoots map[string]bool

	// ObservedPaths names what to withhold inside a root that is otherwise
	// readable: root name to RFC 6901 pointers within it. A capture written as
	// a path -- `body.data.access_token` -- is addressable, so the rest of the
	// response survives, which matters because a token response is usually the
	// one worth reading.
	ObservedPaths map[string][]string
}

// Any reports whether anything at all is secret, so a caller can skip the work
// of redaction for the common step that declares nothing.
func (s Secrets) Any() bool {
	return s.URL || s.Body || s.Args || s.Stdin ||
		len(s.Headers) > 0 || len(s.Env) > 0 ||
		len(s.Acts) > 0 || len(s.BodyPaths) > 0 ||
		len(s.ObservedRoots) > 0 || len(s.ObservedPaths) > 0
}
