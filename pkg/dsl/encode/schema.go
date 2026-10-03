package encode

// Schema is the UI contract, written down.
//
// testdata/schema.json is its golden, so a change to the encoding that nobody
// meant fails CI with a diff somebody has to read -- which is the whole point:
// the UI ships and upgrades independently of the CLI, and a field that quietly
// changed shape would be found by a user rather than by a test.
//
// A golden on its own would not be enough, because a table nothing reads goes
// stale and then pins the wrong thing confidently. So schema_test.go validates
// the encoding of the whole fixture corpus *against* this table: every field
// the encoder emits has to be declared here with the right type, and every
// field declared not optional has to actually be there. Adding a field to a
// node without adding it here is a test failure, not a silent divergence.
//
// What the table says about each field:
//
//	type           string, string[], integer, boolean, span, comments, name,
//	               comment[], segment[], entry[], node, node[]
//	accepts        for a node or node[], the group of kinds legal there
//	optional       artemis may leave it out -- an absent child, an empty list,
//	               a false flag
//	inputRequired  Decode demands it; leaving it out is an error, not a default

// fieldSchema is one field of one node or object.
type fieldSchema struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Accepts       string `json:"accepts,omitempty"`
	Optional      bool   `json:"optional,omitempty"`
	InputRequired bool   `json:"inputRequired,omitempty"`
	Doc           string `json:"doc,omitempty"`
}

// nodeSchema is one node kind.
type nodeSchema struct {
	Kind   string        `json:"kind"`
	Doc    string        `json:"doc,omitempty"`
	Fields []fieldSchema `json:"fields"`
}

// objectSchema is one of the shapes that is not a node: a span, a comment
// group, a quoted name, an interpolation segment, an object-literal entry.
type objectSchema struct {
	Name   string        `json:"name"`
	Doc    string        `json:"doc,omitempty"`
	Fields []fieldSchema `json:"fields"`
}

// Schema returns the encoding's own description, as JSON.
//
// Indented and newline-terminated like a document, because it is read by people
// and reviewed as a golden.
func Schema() ([]byte, error) { return marshalIndent(schema()) }

// SchemaString is Schema into a string.
func SchemaString() (string, error) {
	b, err := Schema()
	return string(b), err
}

func schema() *obj {
	o := newObj()
	o.set("schemaVersion", SchemaVersion)
	o.set("outputOnlyKinds", []string{kindBad})
	o.set("groups", groups())
	o.set("document", documentSchema)
	o.set("objects", objectSchemas)
	o.set("nodes", nodeSchemas)
	return o
}

// groups are the kinds legal in each node position, which is what a form's
// "add" menu is: the three actions, the three things a scenario body holds, the
// three statements of a step.
//
// They are the sets build.go dispatches on, so a group here and the position it
// describes cannot disagree -- schema_test.go asserts that every kind named in
// a group is one Decode accepts in that position.
//
// `bad` is in none of them. It appears in output wherever a node may appear, for
// source that did not parse, and Decode accepts it nowhere; outputOnlyKinds says
// so.
func groups() *obj {
	g := newObj()
	for _, s := range groupSchemas {
		g.set(s.Name, s.Kinds)
	}
	return g
}

// groupSchema is one node position and the kinds legal in it.
type groupSchema struct {
	Name  string
	Kinds []string
}

var groupSchemas = []groupSchema{
	{"declaration", []string{kindScenario}},
	{"scenarioBody", []string{kindConfig, kindVar, kindStep}},
	{"action", []string{kindRequest, kindRun, kindBrowser}},
	{"stepStatement", []string{kindExpect, kindCapture, kindField}},
	{"blockField", []string{kindField}},
	{"browserAction", []string{kindBrowserAct}},
	{"block", []string{kindBlock}},
	{"expression", []string{
		kindIdent, kindLiteral, kindInterp, kindUnary, kindBinary, kindExists,
		kindIsType, kindMember, kindIndex, kindCall, kindObject, kindArray, kindParen,
	}},
}

// documentSchema is the top-level object.
var documentSchema = objectSchema{
	Name: "document",
	Doc:  "the whole tree: what `artemis ast -f x.art` writes and `artemis ast --from-json` reads",
	Fields: []fieldSchema{
		{Name: "schemaVersion", Type: "integer", InputRequired: true,
			Doc: "the version of this encoding; artemis refuses a document from the future"},
		{Name: "file", Type: "string",
			Doc: "the file every span in the document refers to; empty for a tree with no positioned token in it"},
		{Name: "scenarios", Type: "node[]", Accepts: "declaration", Optional: true},
		{Name: "comments", Type: "comments", Optional: true,
			Doc: "the comments at the end of the file, which belong to no scenario"},
	},
}

var objectSchemas = []objectSchema{
	{
		Name: "span",
		Doc:  "where a node is, in the file the document names; the same six fields and the same names `artemis parse --json` uses",
		Fields: []fieldSchema{
			{Name: "file", Type: "string"},
			{Name: "line", Type: "integer", Doc: "1-based"},
			{Name: "col", Type: "integer", Doc: "1-based, counted in bytes"},
			{Name: "endLine", Type: "integer", Doc: "exclusive: just past the last byte"},
			{Name: "endCol", Type: "integer", Doc: "exclusive"},
			{Name: "offset", Type: "integer", Doc: "0-based byte offset of the start"},
		},
	},
	{
		Name: "name",
		Doc:  "a quoted name -- a scenario's or a step's -- as source text and as its decoded value",
		Fields: []fieldSchema{
			{Name: "text", Type: "string", InputRequired: true, Doc: "the source, quotes and escapes included"},
			{Name: "value", Type: "string", Doc: "the decoded name; output only, artemis re-reads it from text"},
		},
	},
	{
		Name: "comment",
		Doc:  "one own-line comment, and whether the author left a blank line above it",
		Fields: []fieldSchema{
			{Name: "text", Type: "string", InputRequired: true, Doc: "the comment including its `#`"},
			{Name: "blank", Type: "boolean", Optional: true},
		},
	},
	{
		Name: "comments",
		Doc:  "the five places canonical layout can put a comment; absent when a node carries none",
		Fields: []fieldSchema{
			{Name: "above", Type: "comment[]", Optional: true, Doc: "own-line comments before the node"},
			{Name: "blankAbove", Type: "boolean", Optional: true, Doc: "a blank line between those and the node"},
			{Name: "after", Type: "string[]", Optional: true, Doc: "comments at the end of the node's line"},
			{Name: "open", Type: "string[]", Optional: true, Doc: "a comment at the end of the line the `{` opens"},
			{Name: "beforeClose", Type: "comment[]", Optional: true, Doc: "own-line comments above the `}`"},
			{Name: "afterClose", Type: "string[]", Optional: true, Doc: "a comment at the end of the `}`'s line"},
		},
	},
	{
		Name: "segment",
		Doc:  "one piece of an interpolated string: the literal text up to a hole, and the expression in it",
		Fields: []fieldSchema{
			{Name: "delim", Type: "string", InputRequired: true, Doc: "`\"a${` for the first segment, `}b${` for a later one"},
			{Name: "expr", Type: "node", Accepts: "expression", Optional: true, InputRequired: true,
				Doc: "absent only in a file the parser recovered past: `\"${}\"` has a hole with nothing in it"},
		},
	},
	{
		Name: "entry",
		Doc:  "one `\"key\": value` pair of an object literal",
		Fields: []fieldSchema{
			{Name: "key", Type: "node", Accepts: "expression", Optional: true, InputRequired: true,
				Doc: "absent only in a file the parser recovered past"},
			{Name: "value", Type: "node", Accepts: "expression", Optional: true, InputRequired: true,
				Doc: "absent only in a file the parser recovered past: `{\"a\":}` is one"},
		},
	},
}

// span and comments are on so many nodes that spelling them out each time would
// bury the fields that differ.
var (
	spanField     = fieldSchema{Name: "span", Type: "span"}
	commentsField = fieldSchema{Name: "comments", Type: "comments", Optional: true}

	// recoveredName is a quoted name the parser had to recover past. It is the
	// one field that is optional in output and required on input, and the
	// combination is the point: `artemis ast` describes a broken file, and
	// Decode will not accept one.
	recoveredName = fieldSchema{Name: "name", Type: "name", Optional: true, InputRequired: true,
		Doc: "absent only in a file the parser recovered past; Decode requires it"}

	// child is a node-valued field that is absent in a recovered parse and
	// required on the way in.
	child = func(name, accepts string) fieldSchema {
		return fieldSchema{Name: name, Type: "node", Accepts: accepts, Optional: true, InputRequired: true}
	}
	// optChild is a node-valued field the grammar makes optional.
	optChild = func(name, accepts string) fieldSchema {
		return fieldSchema{Name: name, Type: "node", Accepts: accepts, Optional: true}
	}
	// list is a node list, absent when empty.
	list = func(name, accepts string) fieldSchema {
		return fieldSchema{Name: name, Type: "node[]", Accepts: accepts, Optional: true}
	}
	word = func(name, doc string) fieldSchema {
		return fieldSchema{Name: name, Type: "string", Doc: doc}
	}
)

var nodeSchemas = []nodeSchema{
	{Kind: kindScenario, Doc: "`scenario \"name\" { ... }`", Fields: []fieldSchema{
		recoveredName,
		list("body", "scenarioBody"),
		spanField, commentsField,
	}},
	{Kind: kindConfig, Doc: "`config browser { headless = true }`", Fields: []fieldSchema{
		word("subject", "what is being configured; only `browser` has settings today"),
		child("block", "block"),
		spanField, commentsField,
	}},
	{Kind: kindVar, Doc: "`var url = env(\"API_URL\")`", Fields: []fieldSchema{
		word("name", "the identifier being bound"),
		child("value", "expression"),
		spanField, commentsField,
	}},
	{Kind: kindStep, Doc: "`step \"login\" { <action> <statement>... }`", Fields: []fieldSchema{
		recoveredName,
		child("action", "action"),
		list("body", "stepStatement"),
		spanField,
		{Name: "stepType", Type: "string",
			Doc: "api, terminal, browser, or unknown: inferred from the action and nothing else. Output only"},
		{Name: "scope", Type: "string[]", Optional: true,
			Doc: "every name resolvable in this step, in did-you-mean order: the step type's roots, the scenario's vars, then earlier steps' captures. A path picker is this list. Output only"},
		commentsField,
	}},
	{Kind: kindRequest, Doc: "an api step's action: a verb, a URL, an optional block", Fields: []fieldSchema{
		word("method", "the HTTP verb, lower case; its presence is what makes the step an api step"),
		child("url", "expression"),
		optChild("block", "block"),
		spanField, commentsField,
	}},
	{Kind: kindRun, Doc: "a terminal step's action: `run \"psql\" { args = [...] }`", Fields: []fieldSchema{
		child("command", "expression"),
		optChild("block", "block"),
		spanField, commentsField,
	}},
	{Kind: kindBrowser, Doc: "a browser step's action: `browser { goto ... click ... }`", Fields: []fieldSchema{
		list("acts", "browserAction"),
		spanField, commentsField,
	}},
	{Kind: kindBrowserAct, Doc: "one statement inside a browser block", Fields: []fieldSchema{
		word("name", "goto, click, fill, select, press, hover, upload or wait"),
		child("target", "expression"),
		optChild("value", "expression"),
		spanField, commentsField,
	}},
	{Kind: kindBlock, Doc: "a brace-delimited field list: a request's, a `run`'s, a `retry`'s, a `config`'s, an `env`'s", Fields: []fieldSchema{
		list("fields", "blockField"),
		spanField, commentsField,
	}},
	{Kind: kindField, Doc: "`name = value`, `name \"key\" = value`, or `name { ... }`", Fields: []fieldSchema{
		word("name", "header, query, body, args, cwd, stdin, env, times, delay, timeout, retry, headless, viewport"),
		optChild("key", "expression"),
		{Name: "value", Type: "node", Accepts: "expression", Optional: true,
			Doc: "exactly one of value and block"},
		optChild("block", "block"),
		spanField, commentsField,
	}},
	{Kind: kindExpect, Doc: "`expect <expr>` with an optional `within \"10s\"`; one expect is one assertion", Fields: []fieldSchema{
		child("value", "expression"),
		optChild("budget", "expression"),
		spanField,
		{Name: "class", Type: "string",
			Doc: "simple (`<path> <op> <literal>`, `<path> exists`, `<path> is <type>`, or the `not` form of any) " +
				"renders as a path picker, an operator dropdown and a value field; complex renders as one raw " +
				"expression field. Output only"},
		commentsField,
	}},
	{Kind: kindCapture, Doc: "`capture token = body.data.access_token`", Fields: []fieldSchema{
		word("name", "the identifier bound for every later step in the scenario"),
		child("value", "expression"),
		spanField, commentsField,
	}},
	{Kind: kindBad, Doc: "source that did not parse, kept verbatim. Output only: Decode refuses a tree holding one", Fields: []fieldSchema{
		word("source", "the exact bytes, trivia included"),
		spanField,
	}},
	{Kind: kindIdent, Doc: "a name: a variable, a capture, or a bound root like `status`", Fields: []fieldSchema{
		word("name", ""),
		spanField,
	}},
	{Kind: kindLiteral, Doc: "a number, a string with no interpolation, a regex, a boolean, or null", Fields: []fieldSchema{
		{Name: "literal", Type: "string", InputRequired: true,
			Doc: "number, string, regex, boolean or null"},
		{Name: "text", Type: "string", InputRequired: true,
			Doc: "the source, delimiters included: `\"a\\nb\"` with its quotes, `/re/` with its slashes"},
		word("value", "the decoded content: escapes resolved. Output only, artemis re-reads it from text"),
		spanField,
	}},
	{Kind: kindInterp, Doc: "a double-quoted string with at least one `${...}` in it", Fields: []fieldSchema{
		{Name: "segments", Type: "segment[]", Optional: true, InputRequired: true},
		word("end", "the closing piece, `}c\"`"),
		spanField,
	}},
	{Kind: kindUnary, Doc: "`not x` or `-x`", Fields: []fieldSchema{
		word("op", "not or -"),
		child("x", "expression"),
		spanField,
	}},
	{Kind: kindBinary, Doc: "an infix operator", Fields: []fieldSchema{
		word("op", "and, or, ==, !=, <, <=, >, >=, contains or matches"),
		child("x", "expression"),
		child("y", "expression"),
		spanField,
	}},
	{Kind: kindExists, Doc: "the postfix predicate `body.x exists`", Fields: []fieldSchema{
		child("x", "expression"),
		spanField,
	}},
	{Kind: kindIsType, Doc: "the postfix predicate `body.count is number`", Fields: []fieldSchema{
		child("x", "expression"),
		word("type", "string, number, boolean, object, array or null"),
		spanField,
	}},
	{Kind: kindMember, Doc: "`body.data`", Fields: []fieldSchema{
		child("x", "expression"),
		word("name", ""),
		spanField,
	}},
	{Kind: kindIndex, Doc: "`body.items[0]` or `headers[\"content-type\"]`", Fields: []fieldSchema{
		child("x", "expression"),
		child("index", "expression"),
		spanField,
	}},
	{Kind: kindCall, Doc: "`env(\"API_URL\")` or `text(\"[role=status]\")`; there are no first-class functions", Fields: []fieldSchema{
		word("callee", "env, text, value, attr, count or visible"),
		list("args", "expression"),
		spanField,
	}},
	{Kind: kindObject, Doc: "an object literal, serialised to JSON by the lowerer rather than spliced as text", Fields: []fieldSchema{
		{Name: "entries", Type: "entry[]", Optional: true},
		spanField,
	}},
	{Kind: kindArray, Doc: "an array literal, `[\"-f\", \"seed.sql\"]`", Fields: []fieldSchema{
		list("elems", "expression"),
		spanField,
	}},
	{Kind: kindParen, Doc: "`( expr )`. Kept as a node: canonical layout adds no parentheses, so a client that means them must send this", Fields: []fieldSchema{
		child("x", "expression"),
		spanField,
	}},
}
