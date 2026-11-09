package postman

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/encode"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
	"artemis/pkg/shared/migrate"
)

// ErrNoRequests is what CollectionFiles returns for a Postman file that holds
// no request: there is nothing to put in a collection.
var ErrNoRequests = errors.New("the collection holds no requests")

// Files is what generate writes: a collection, the flow that calls it, and the
// scenario file that runs the flow. Collection is empty for the flat output,
// whose flow writes each step in place.
type Files struct {
	Collection string // `collection "<name>" { request ... }`, one request per Postman request
	Flow       string // `flow <name> { var ... step ... }`: the Postman variables, then one step per request
	Scenario   string // `flow "<flow file>"` and one empty scenario titled as the collection is
}

// CollectionFiles is config as a collection file, a flow that calls it, and a
// scenario file that runs the flow.
//
// Each step becomes one `request`, in the order the steps are in. A request
// declares each Postman variable it reads as a `var` with no value, an input
// the flow supplies: the flow holds one `var` for each Postman variable, and
// an item's input with no value of its own is read from the flow's names. The
// request body is the step body that migrate.Source writes, so a run of the
// three files sends what a run of the flat output sends.
//
// collectionFile and flowFile are the names the collection and the flow will
// be written under, side by side with the scenario file. The collection is
// titled as the Postman collection is, and the flow imports it by its base
// name and calls it by the file's name, or by an `as` when that name cannot be
// one. The scenario file names the flow by its base name.
//
// Two steps may share a name in the flat file; two requests of one collection
// may not, so the second gets the suffix _2, the third _3, and so on.
//
// The text is written in the canonical form `artemis fmt` gives, rather than
// printed from a tree, so that it does not wait on the printer.
func CollectionFiles(config migrate.Config, comments migrate.Comments, collectionFile, flowFile string) (Files, error) {
	if len(config.Steps) == 0 {
		return Files{}, ErrNoRequests
	}

	vars := map[string]bool{}
	for _, v := range config.Variables {
		vars[v.Name] = true
	}

	title := config.Name
	coll, ok := parser.ImportName(collectionFile)
	if title == "" {
		title = coll
	}
	importLine := fmt.Sprintf("import %q", filepath.Base(collectionFile))
	if !ok || !token.IsSnakeID(coll) {
		// The collection's id is the name the flow calls it by, and an id is
		// snake_case: a file name that is not one is imported as the id.
		coll = token.SnakeFrom(coll)
		importLine += " as " + coll
	}

	var items []string
	var steps []string
	taken := map[string]bool{}
	for _, st := range config.Steps {
		name := requestName(st.Name, taken)
		text, err := stepText(st, vars)
		if err != nil {
			return Files{}, err
		}
		var item strings.Builder
		fmt.Fprintf(&item, "  request %s {\n", name)
		args := make([]string, 0, len(text.reads))
		for _, r := range text.reads {
			in := r
			if token.IsUseLine(r) {
				// A step block reads these words as overrides, so an
				// input with one of them could never be passed, nor read
				// from the flow under its own name: the step passes it.
				in = r + "_value"
				args = append(args, in+" = "+r)
			}
			fmt.Fprintf(&item, "    var %s\n", in)
		}
		if len(text.reads) > 0 {
			item.WriteString("\n")
		}
		item.WriteString(text.body)
		item.WriteString("  }\n")
		items = append(items, item.String())

		call := fmt.Sprintf("  step %s = %s.%s {", text.name, coll, name)
		if len(args) > 0 {
			call += " " + strings.Join(args, ", ") + " "
		}
		steps = append(steps, call+"}")
	}

	head := "collection " + coll
	if title != coll {
		head += " as " + quoteTitle(title)
	}
	collSrc := fmt.Sprintf("%s {\n%s}\n", head, strings.Join(items, "\n"))
	if c := firstLine(comments.File); c != "" {
		collSrc = c + "\n" + collSrc
	}

	head, err := migrate.Source(migrate.Config{Name: title, Type: config.Type, Variables: config.Variables}, comments)
	if err != nil {
		return Files{}, err
	}
	flow, scenarioLine, err := asFlow(head, flowName(title), steps, false)
	if err != nil {
		return Files{}, err
	}
	return Files{
		Collection: collSrc,
		Flow:       importLine + "\n\n" + flow,
		Scenario:   scenarioFile(firstLine(comments.File), flowFile, scenarioLine),
	}, nil
}

// FlatFiles is config as a flow that writes each request in place as a step,
// and a scenario file that runs it. flowFile is the name the flow will be
// written under, beside the scenario file. Files.Collection is empty.
func FlatFiles(config migrate.Config, comments migrate.Comments, flowFile string) (Files, error) {
	src, err := migrate.Source(config, comments)
	if err != nil {
		return Files{}, err
	}
	title := config.Name
	if title == "" {
		title, _ = parser.ImportName(flowFile)
		title = strings.TrimSuffix(title, ".flow")
	}
	flow, scenarioLine, err := asFlow(src, flowName(title), nil, true)
	if err != nil {
		return Files{}, err
	}
	return Files{Flow: flow, Scenario: scenarioFile(firstLine(comments.File), flowFile, scenarioLine)}, nil
}

// asFlow turns the scenario migrate.Source wrote into a flow named name: the
// `scenario "<title>" {` line becomes `flow <name> {`, and steps go below
// what the scenario held, after a blank line. The scenario line comes back
// with an empty body, `scenario "<title>" {}`, for the scenario file.
//
// spaced is a blank line between the steps, which the steps written in place
// already have.
func asFlow(src, name string, steps []string, spaced bool) (flow, scenario string, err error) {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	at := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "scenario ") {
			at = i
			break
		}
	}
	if at < 0 {
		return "", "", fmt.Errorf("the scenario has no scenario line, which is a bug in artemis:\n%s", src)
	}
	head := lines[at]
	var body []string
	switch {
	case strings.HasSuffix(head, " {}") && at == len(lines)-1:
		head = strings.TrimSuffix(head, " {}")
	case strings.HasSuffix(head, " {") && lines[len(lines)-1] == "}":
		head = strings.TrimSuffix(head, " {")
		body = lines[at+1 : len(lines)-1]
	default:
		return "", "", fmt.Errorf("the scenario has an unexpected layout, which is a bug in artemis:\n%s", src)
	}
	scenario = head + " {}"

	if len(body) > 0 && len(steps) > 0 {
		body = append(body, "")
	}
	for i, s := range steps {
		if spaced && i > 0 {
			body = append(body, "")
		}
		body = append(body, s)
	}

	var b strings.Builder
	for _, l := range lines[:at] {
		b.WriteString(l + "\n")
	}
	if len(body) == 0 {
		fmt.Fprintf(&b, "flow %s {}\n", name)
		return b.String(), scenario, nil
	}
	fmt.Fprintf(&b, "flow %s {\n", name)
	for _, l := range body {
		b.WriteString(l + "\n")
	}
	b.WriteString("}\n")
	return b.String(), scenario, nil
}

// scenarioFile is the scenario file that runs the flow at flowFile: its header,
// then one scenario that sets nothing, so every var keeps its default.
func scenarioFile(comment, flowFile, scenario string) string {
	var b strings.Builder
	if comment != "" {
		b.WriteString(comment + "\n")
	}
	fmt.Fprintf(&b, "flow %q\n\n%s\n", filepath.Base(flowFile), scenario)
	return b.String()
}

// flowName is the collection's title as a flow's name: "Orders API" is
// orders_api.
func flowName(title string) string {
	name := identifier(strings.ToLower(title))
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	if name == "" {
		return "flow"
	}
	return name
}

// quoteTitle is title as a DSL string literal.
func quoteTitle(title string) string { return encode.QuoteString(title) }

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

// step is one request's text, read off the step block migrate.Source writes.
type step struct {
	name  string   // the step's name as a DSL string, quotes included
	body  string   // the lines inside the step block, indented for a request
	reads []string // the vars those lines read, sorted
}

// stepText is the step block migrate.Source writes for st, as the parts a
// request and the step that calls it need.
func stepText(st migrate.Step, vars map[string]bool) (step, error) {
	src, err := migrate.Source(migrate.Config{Name: "step", Steps: []migrate.Step{st}}, migrate.Comments{})
	if err != nil {
		return step{}, err
	}
	tree, bag := parser.Parse("step.art", src)
	if bag.HasErrors() {
		return step{}, fmt.Errorf("request %q does not parse: %v", st.Name, bag.All()[0])
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
	var block []string
	for i, l := range lines {
		if i == 0 || i == len(lines)-1 {
			continue
		}
		block = append(block, l)
	}
	open := ""
	if len(block) > 0 {
		open = strings.TrimSpace(block[0])
	}
	if len(block) < 2 || !strings.HasPrefix(open, "step ") || !strings.HasSuffix(open, " {") {
		return step{}, fmt.Errorf("request %q: unexpected step layout:\n%s", st.Name, src)
	}
	var b strings.Builder
	for _, l := range block[1 : len(block)-1] {
		b.WriteString(l + "\n")
	}
	name := strings.TrimSuffix(strings.TrimPrefix(open, "step "), " {")
	return step{name: name, body: b.String(), reads: names}, nil
}

// firstLine is the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
