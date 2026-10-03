// Package codegen turns a checked .art tree into source for another test
// runner: `artemis build --lang=python` and, from ART-50, `--lang=js`.
//
// # Why this is additive
//
// It hangs off the same tree the interpreter walks. pkg/dsl/lower gives a
// *lower.Scenario whose steps, actions, expects and captures are in source
// order and whose every value position is still an ast.Expr -- lowering
// evaluates nothing until Step.Model(env), which this package never calls. So
// the interpreter and a target share the whole front end and the whole lowering,
// and differ only in what they do with an expression. There is no second reader
// of the language here.
//
// # One-way export
//
// Nothing in artemis reads generated code back. A target's output is a file a
// person then owns, and every generated file says so in its header. That is why
// the output carries no version string and no timestamp: regenerating a
// scenario that did not change produces the same bytes, so `artemis build` is
// safe to run in a loop and a diff means the scenario moved.
//
// # The surface
//
//	tgt, err := codegen.Lookup("python")
//	files, err := tgt.Generate(tree)
//
// Lookup has three answers rather than two. A name may be implemented, or
// *reserved* -- `go` is reserved by the design's non-goals and `js` is not built
// yet -- or unknown, which lists what exists. A reserved name getting a
// did-you-mean against the implemented ones would be the wrong answer to a
// question the project has already decided.
package codegen

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"artemis/pkg/dsl/ast"
)

// GeneratedFile is one file a target produced.
//
// Path is relative to the output directory and always uses forward slashes, so
// a target decides the layout and the caller decides where it lands. Content is
// the whole file including its trailing newline.
type GeneratedFile struct {
	Path    string
	Content string
}

// Target is one language artemis can export a scenario to.
//
// Generate takes the tree and nothing else, which is the signature the design
// document specifies. It is enough because the tree carries its own provenance:
// every token's span holds the file it was read from, so a target derives the
// output's name and its header from the tree it was handed. A tree built in Go,
// or decoded by `artemis ast --from-json`, has no spans and gets the fallback
// name -- see SourceName.
//
// Generate is handed a tree pkg/dsl/check accepted; `artemis build` runs the
// front end first and refuses a file with an error diagnostic. A step with no
// action, or an ast.Bad in a value position, is reported as an error naming the
// step rather than emitted as something that would not compile.
type Target interface {
	Name() string
	Generate(*ast.File) ([]GeneratedFile, error)
}

// ErrReserved is what Lookup answers for a language name the project knows and
// does not implement. Wrapped rather than returned bare so the message can say
// which name and why; errors.Is still matches it.
var ErrReserved = errors.New("target is reserved")

// ErrUnknownTarget is what Lookup answers for a name that is not a language
// artemis has an opinion about.
var ErrUnknownTarget = errors.New("unknown target")

// targets are the implemented ones, by the name --lang takes.
var targets = map[string]Target{
	"python": Python{},
}

// reserved are the names the project has decided about and does not implement,
// each with the sentence that is the whole answer.
//
// `go` is reserved by the design's non-goals: artemis will not generate Go,
// because the eject path exists for teams whose tests live in another language
// and Go is the one artemis is already written in. `js` is specified and not
// built yet.
var reserved = map[string]string{
	"go": "the go target is reserved and artemis does not generate it; " +
		"the .art file is the Go-side source of truth, and `artemis run` is how Go runs it",
	"js": "the javascript target is specified and not built yet; " +
		"today `artemis build` generates python",
}

// Lookup resolves a --lang value.
//
// The three answers are the three things that can be true of a name, and they
// are kept apart because the help each one needs is different: a target to use,
// a decision to read, or a typo to fix.
func Lookup(name string) (Target, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if t, ok := targets[key]; ok {
		return t, nil
	}
	if why, ok := reserved[key]; ok {
		return nil, fmt.Errorf("%w: %s", ErrReserved, why)
	}
	return nil, fmt.Errorf("%w %q; artemis build generates %s", ErrUnknownTarget, name, list(Names()))
}

// Names are the implemented target names, sorted. This is what --lang's help
// text lists, so the flag's documentation and the registry cannot disagree.
func Names() []string { return keys(targets) }

// Reserved are the names Lookup refuses with a reason, sorted.
func Reserved() []string { return keys(reserved) }

// Why is the sentence Lookup gives for a reserved name, and whether name is
// one. Exported so `artemis build`'s help can print the decisions rather than
// restate them.
func Why(name string) (string, bool) {
	why, ok := reserved[strings.ToLower(strings.TrimSpace(name))]
	return why, ok
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// list joins names the way a sentence does: "python", or "python and js".
func list(names []string) string {
	switch len(names) {
	case 0:
		return "nothing"
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// SourceName is the .art file a tree was read from, taken off the spans the
// parser set, and "" for a tree that has none.
//
// The first scenario's keyword is asked before the EOF token because a file
// whose only token is EOF still carries its name there, and a tree assembled in
// Go carries it nowhere -- so the two are tried in that order and the caller
// decides what an unnamed tree is called.
func SourceName(tree *ast.File) string {
	if tree == nil {
		return ""
	}
	for _, d := range tree.Scenarios {
		if f := d.Span().File; f != "" {
			return f
		}
	}
	return tree.EOF.Span.File
}
