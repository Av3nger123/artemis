package postman

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"
	"artemis/pkg/shared/migrate"
)

// ErrNoRequests is what CollectionFiles returns for a Postman file that holds
// no request: there is nothing to put in a collection.
var ErrNoRequests = errors.New("the collection holds no requests")

// Files is a generated collection file and the scenario file that uses it.
type Files struct {
	Collection string // `collection "<name>" { request ... }`, one request per Postman request
	Scenario   string // the scenario's vars, then one `use` per request
}

// CollectionFiles is config as a collection file and a scenario that uses it.
//
// Each step becomes one `request`, in the order the steps are in. A collection
// has no `var`, so each scenario var a request reads becomes a parameter of
// that request, and the scenario passes the var in by name. The request body is
// the step body that migrate.Source writes, so a run of the two files sends
// what a run of the flat file sends.
//
// collectionFile is the name the collection will be written under, and names
// the collection: orders.collection.art holds orders. The scenario imports it
// by its base name, so the two files must be written side by side.
//
// Two steps may share a name in the flat file; two requests of one collection
// may not, so the second gets the suffix _2, the third _3, and so on.
func CollectionFiles(config migrate.Config, comments migrate.Comments, collectionFile string) (Files, error) {
	if len(config.Steps) == 0 {
		return Files{}, ErrNoRequests
	}

	vars := map[string]bool{}
	for _, v := range config.Variables {
		vars[v.Name] = true
	}

	// A collection is named after its file: orders.collection.art holds orders.
	coll := strings.TrimSuffix(filepath.Base(collectionFile), ".collection.art")

	var items strings.Builder
	var uses []string
	taken := map[string]bool{}
	for _, st := range config.Steps {
		name := requestName(st.Name, taken)
		body, reads, err := stepText(st, vars)
		if err != nil {
			return Files{}, err
		}
		params := make([]string, len(reads))
		args := make([]string, len(reads))
		for i, r := range reads {
			p := r
			if token.IsUseLine(r) {
				// A use block reads these words as overrides, so a
				// parameter with one of them could never be passed.
				p = r + "_value"
			}
			params[i], args[i] = p, p+" = "+r
		}
		fmt.Fprintf(&items, "  request %s(%s) {\n%s  }\n\n", name, strings.Join(params, ", "), body)
		use := "  use " + coll + "." + name
		if len(args) > 0 {
			use += " { " + strings.Join(args, ", ") + " }"
		}
		uses = append(uses, use)
	}

	collSrc := fmt.Sprintf("%s\ncollection %q {\n\n%s}\n", firstLine(comments.File), coll, strings.TrimRight(items.String(), "\n")+"\n")

	head, err := migrate.Source(migrate.Config{Name: config.Name, Type: config.Type, Variables: config.Variables}, comments)
	if err != nil {
		return Files{}, err
	}
	i := strings.LastIndex(head, "}")
	if i < 0 {
		return Files{}, fmt.Errorf("the scenario head has no closing brace:\n%s", head)
	}
	scenSrc := fmt.Sprintf("import %q\n\n%s\n%s\n}\n", filepath.Base(collectionFile), strings.TrimRight(head[:i], "\n"), strings.Join(uses, "\n"))

	collOut, err := canonical("collection", collSrc)
	if err != nil {
		return Files{}, err
	}
	scenOut, err := canonical("scenario", scenSrc)
	if err != nil {
		return Files{}, err
	}
	return Files{Collection: collOut, Scenario: scenOut}, nil
}

// requestName is a step name as a request name that no earlier request in
// taken has, and records it.
func requestName(step string, taken map[string]bool) string {
	base := identifier(strings.ToLower(step))
	// "Orders / list" becomes orders___list; one underscore reads better.
	for strings.Contains(base, "__") {
		base = strings.ReplaceAll(base, "__", "_")
	}
	if base == "" {
		base = "request"
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s_%d", base, n)
	}
	taken[name] = true
	return name
}

// stepText is the lines inside the `step` block migrate.Source writes for st,
// indented for a request, and the vars those lines read, sorted.
func stepText(st migrate.Step, vars map[string]bool) (string, []string, error) {
	src, err := migrate.Source(migrate.Config{Name: "step", Steps: []migrate.Step{st}}, migrate.Comments{})
	if err != nil {
		return "", nil, err
	}
	tree, bag := parser.Parse("step.art", src)
	if bag.HasErrors() {
		return "", nil, fmt.Errorf("request %q does not parse: %v", st.Name, bag.All()[0])
	}

	reads := map[string]bool{}
	callee := map[*ast.Ident]bool{}
	ast.Inspect(tree, func(n ast.Node) {
		switch v := n.(type) {
		case *ast.Call:
			callee[v.Callee] = true
		case *ast.Ident:
			if !callee[v] && vars[v.Tok.Value] {
				reads[v.Tok.Value] = true
			}
		}
	})
	names := make([]string, 0, len(reads))
	for r := range reads {
		names = append(names, r)
	}
	sort.Strings(names)

	// The printed source is `scenario "step" {`, the step block, then `}`.
	// The step block's own lines are the ones between its first and last line.
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	var step []string
	for i, l := range lines {
		if i == 0 || i == len(lines)-1 {
			continue
		}
		step = append(step, l)
	}
	if len(step) < 2 || !strings.HasPrefix(strings.TrimSpace(step[0]), "step ") {
		return "", nil, fmt.Errorf("request %q: unexpected step layout:\n%s", st.Name, src)
	}
	var b strings.Builder
	for _, l := range step[1 : len(step)-1] {
		b.WriteString("  " + l + "\n")
	}
	return b.String(), names, nil
}

// canonical parses src and prints it the way `artemis fmt` does. A parse error
// is a bug in this package, so it shows the source.
func canonical(what, src string) (string, error) {
	tree, bag := parser.Parse(what+".art", src)
	if bag.HasErrors() {
		return "", fmt.Errorf("generated %s does not parse, which is a bug in artemis: %v\n%s", what, bag.All()[0], src)
	}
	return print.Canonical(tree), nil
}

// firstLine is the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
