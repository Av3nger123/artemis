package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/encode"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/executor"

	"github.com/spf13/cobra"
)

// The flags that run one collection item rather than a file's scenarios. A
// collection is never run on its own -- only scenarios run -- so a client that
// wants to send one request, the way an API client does, needed a scenario
// written for it. --item writes that scenario.
const (
	itemFlag     = "item"
	argFlag      = "arg"
	argsFileFlag = "args"
	capturesFlag = "captures"
)

// itemRun is a run of one collection item, or of every item of one collection.
//
// The scenario is written in a file of its own, in memory, beside the
// collection file and importing it, as any scenario would: a collection file
// holds only its collection (SPEC.md, A collection file). The collection's own
// imports resolve from where they always did, and every line of it keeps its
// number -- a diagnostic or a failed expect points at the file the person has
// open. The run is reported under the collection file's path. Nothing is
// written to disk.
type itemRun struct {
	file string // the collection file, as the run reports it
	name string // the in-memory scenario file's name, beside it
	src  string
}

// itemCaptures is what --captures writes: every value a capture got, and which
// of them are secret. It is the one place a secret's value leaves the run, so
// it goes only to a file the caller named, readable by its owner alone. A
// report or a trace never holds it.
type itemCaptures struct {
	Values map[string]any `json:"values"`
	Secret []string       `json:"secret"`
}

// captureSink collects what an item run's captures got, for --captures.
type captureSink struct {
	path   string
	values map[string]any
	secret map[string]bool
}

// itemFromFlags answers nil when --item was not given, after refusing an --arg,
// --args or --captures that came without it.
func itemFromFlags(cmd *cobra.Command, path string) (*itemRun, *captureSink, error) {
	ref, _ := cmd.Flags().GetString(itemFlag)
	rawArgs, _ := cmd.Flags().GetStringArray(argFlag)
	argsFile, _ := cmd.Flags().GetString(argsFileFlag)
	capturesPath, _ := cmd.Flags().GetString(capturesFlag)
	if ref == "" {
		if len(rawArgs) > 0 || argsFile != "" || capturesPath != "" {
			return nil, nil, fmt.Errorf("--arg, --args and --captures go with --item")
		}
		return nil, nil, nil
	}
	if info, err := os.Stat(path); err != nil {
		return nil, nil, err
	} else if info.IsDir() {
		return nil, nil, fmt.Errorf("--item runs an item of a collection file; %s is a folder", path)
	}

	args := map[string]any{}
	if argsFile != "" {
		b, err := os.ReadFile(argsFile) //nolint:gosec // the path is the one the user named
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(b, &args); err != nil {
			return nil, nil, fmt.Errorf("%s: --args takes a JSON object of parameter names to values: %w", argsFile, err)
		}
	}
	// After the file, so a --arg given on the command line wins over it.
	for _, raw := range rawArgs {
		name, value, ok := strings.Cut(raw, "=")
		if !ok || name == "" {
			return nil, nil, fmt.Errorf("--arg %q: write it as name=value", raw)
		}
		args[name] = value
	}

	src, err := os.ReadFile(path) //nolint:gosec // the path is the one the user named
	if err != nil {
		return nil, nil, err
	}
	scenario, err := itemScenario(frontEnd(path, string(src)).Tree, ref, args)
	if err != nil {
		return nil, nil, err
	}
	base := filepath.Base(path)
	run := &itemRun{
		file: path,
		name: filepath.Join(filepath.Dir(path), strings.TrimSuffix(base, filepath.Ext(base))+".item.art"),
		src:  "import " + encode.QuoteString(base) + "\n\n" + scenario,
	}
	var sink *captureSink
	if capturesPath != "" {
		sink = &captureSink{path: capturesPath, values: map[string]any{}, secret: map[string]bool{}}
	}
	return run, sink, nil
}

// itemScenario is the scenario that runs ref: `orders.create` is one use of it,
// `orders` a use of each of its requests in the order they are written,
// captures passing down as they do in any scenario.
//
// Each use is passed the arguments its item has a parameter for. For a single
// item that is all of them, so a name it does not take is the checker's
// unknown-argument error, with its did-you-mean; a required parameter left out
// is the checker's error too. Both stop the run before anything is sent.
func itemScenario(tree *ast.File, ref string, args map[string]any) (string, error) {
	collName, itemName, _ := strings.Cut(ref, ".")
	var coll *ast.Collection
	var names []string
	for _, d := range tree.Scenarios {
		if c, ok := d.(*ast.Collection); ok {
			names = append(names, c.Name.Value)
			if c.Name.Value == collName {
				coll = c
			}
		}
	}
	if coll == nil {
		return "", fmt.Errorf("the file has no collection %s (it has: %s)", collName, listOrNone(names))
	}

	type item struct {
		name   string
		params map[string]bool
	}
	var items []item
	var itemNames []string
	for _, d := range coll.Items {
		x, ok := d.(*ast.RequestDecl)
		if !ok {
			continue
		}
		name, params := x.Name.Text, x.Params
		itemNames = append(itemNames, name)
		if itemName != "" && name != itemName {
			continue
		}
		taken := map[string]bool{}
		if params != nil {
			for _, p := range params.List {
				taken[p.Name.Text] = true
			}
		}
		items = append(items, item{name: name, params: taken})
	}
	if itemName != "" && len(items) == 0 {
		return "", fmt.Errorf("%s has no item %s (it has: %s)", collName, itemName, listOrNone(itemNames))
	}

	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	fmt.Fprintf(&b, "scenario %s {\n", encode.QuoteString(ref))
	for _, it := range items {
		fmt.Fprintf(&b, "  use %s.%s {\n", collName, it.name)
		for _, k := range keys {
			if itemName == "" && !it.params[k] {
				continue
			}
			fmt.Fprintf(&b, "    %s = %s\n", k, argLiteral(args[k]))
		}
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	return b.String(), nil
}

// argLiteral writes a value as the literal that stands for it, so an argument
// is a value and never source: a string is quoted with every `${` escaped.
func argLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return encode.QuoteString(x)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = argLiteral(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = encode.QuoteString(k) + ": " + argLiteral(x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	// json.Unmarshal into any yields nothing else.
	return encode.QuoteString(fmt.Sprint(v))
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// take records what one scenario's captures got. A capture is secret when it
// was written `secret capture`, or when a later step counts it among the
// secret bindings it can see -- a capture off a secret value is secret without
// saying so (ART-54).
func (c *captureSink) take(sc *lower.Scenario, scope executor.Scope) {
	if c == nil {
		return
	}
	vars := scope.Vars()
	for _, st := range sc.Steps {
		for _, name := range st.SecretNames() {
			c.secret[name] = true
		}
		for _, cp := range st.Captures {
			if v, ok := vars[cp.Name]; ok {
				c.values[cp.Name] = v
			}
			if cp.Declared {
				c.secret[cp.Name] = true
			}
		}
	}
}

// write puts the captures in the file --captures named, for its owner only.
func (c *captureSink) write() error {
	if c == nil {
		return nil
	}
	out := itemCaptures{Values: c.values, Secret: []string{}}
	for name := range c.values {
		if c.secret[name] {
			out.Secret = append(out.Secret, name)
		}
	}
	sort.Strings(out.Secret)
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("writing --captures: %w", err)
	}
	if err := os.WriteFile(c.path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	// WriteFile keeps the mode of a file that was already there.
	return os.Chmod(c.path, 0o600)
}
