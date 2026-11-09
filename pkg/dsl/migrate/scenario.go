package migrate

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// scenarioFile turns a file of old scenarios into flows and scenario files. A
// scenario in a/b.art becomes the flow b in a/flows/b.art and the scenario
// file a/scenarios/b.art; a file of several scenarios makes b_1, b_2, ... in
// the order they are written, one flow and one scenario file for each.
func (m *migration) scenarioFile(key string, tree *ast.File, scenarios []*ast.Scenario) error {
	dir, base := path.Dir(key), stem(key)
	var imports []*ast.Import
	for _, d := range tree.Scenarios {
		if imp, ok := d.(*ast.Import); ok {
			imports = append(imports, imp)
		}
	}
	suites := m.suitesFor(dir)
	for i, s := range scenarios {
		name := base
		if len(scenarios) > 1 {
			name = fmt.Sprintf("%s_%d", base, i+1)
		}
		// The file's closing comments stay at the end of its last flow.
		eof := token.Token{Kind: token.EOF}
		if i == len(scenarios)-1 {
			eof = tree.EOF
		}
		flow, data, err := m.split(key, s, name, imports, suites, eof)
		if err != nil {
			return err
		}
		if err := m.put(path.Join(dir, "flows", name+".art"), flow); err != nil {
			return err
		}
		if err := m.put(path.Join(dir, "scenarios", name+".art"), data); err != nil {
			return err
		}
	}
	return nil
}

// suite is one suite file that applies to a scenario.
type suite struct {
	key   string
	tree  *ast.File
	block *ast.Scenario
}

// suitesFor is the suites that apply to a scenario file in dir, outermost
// first: a suite.art in dir or in any folder above it.
func (m *migration) suitesFor(dir string) []suite {
	var out []suite
	for _, k := range m.keys {
		if path.Base(k) != "suite.art" || !ancestor(path.Dir(k), dir) {
			continue
		}
		if b := suiteOf(m.trees[k]); b != nil {
			out = append(out, suite{key: k, tree: m.trees[k], block: b})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return depth(path.Dir(out[i].key)) < depth(path.Dir(out[j].key)) })
	return out
}

// depth is how far below the root dir is: 0 for the root, 1 for a folder in
// it, -1 for the folder above it.
func depth(dir string) int {
	n := 0
	for _, seg := range strings.Split(path.Clean(dir), "/") {
		switch seg {
		case ".":
		case "..":
			n--
		default:
			n++
		}
	}
	return n
}

// parts is a scenario or suite body sorted by what the flow does with each
// piece.
type parts struct {
	vars     []*ast.VarDecl
	configs  []*ast.ConfigDecl
	setup    *ast.PhaseDecl
	teardown *ast.PhaseDecl
	tags     *ast.TagsDecl
	rest     []ast.Decl
}

func sortBody(body []ast.Decl) parts {
	var p parts
	for _, d := range body {
		switch v := d.(type) {
		case *ast.VarDecl:
			p.vars = append(p.vars, v)
		case *ast.ConfigDecl:
			p.configs = append(p.configs, v)
		case *ast.TagsDecl:
			p.tags = v
		case *ast.PhaseDecl:
			if v.Keyword.Value == ast.PhaseSetup {
				p.setup = v
			} else {
				p.teardown = v
			}
		default:
			p.rest = append(p.rest, d)
		}
	}
	return p
}

// row is one row of a data-driven scenario: the name its scenario had, and
// the value its row var held.
type row struct {
	title string
	value ast.Expr
}

// split is one old scenario as a flow file and a scenario file.
//
// The flow's vars come first, because only the vars at the top of a flow are
// its parameters: the suites' vars, outermost first, then the row var of a
// data-driven scenario, then the scenario's own, wherever it wrote them (a
// var was evaluated before the first step wherever it stood). Then the
// configs, one setup holding the suites' setup steps and then the
// scenario's, the scenario's steps and blocks in order, and one teardown
// holding the scenario's teardown steps and then the suites', innermost
// first: the order they ran in.
func (m *migration) split(key string, orig *ast.Scenario, name string, imports []*ast.Import, suites []suite, eof token.Token) (*ast.File, *ast.File, error) {
	dir := path.Dir(key)
	flowDir := path.Join(dir, "flows")

	var rows []row
	if orig.For != nil {
		var err error
		if rows, err = m.rows(key, orig); err != nil {
			return nil, nil, err
		}
		if orig.For.Secret.Text != "" {
			m.note("%s: secret is removed from the rows of %s, and reports no longer withhold their values",
				at(key, orig.For.Secret), orig.For.Var.Value)
		}
	}

	s := ast.Clone(orig, identity)
	m.convert(s, key, dir, flowDir)
	suiteReads(s)
	own := sortBody(s.Body)

	// What each suite gives the flow, outermost first.
	from := make([]parts, len(suites))
	colls := m.collections(key, imports)
	var imps []ast.Decl
	seen := map[string]bool{}
	addImports := func(file string, list []*ast.Import) {
		for _, imp := range list {
			target := path.Join(path.Dir(file), imp.Path.Value)
			if seen[target] {
				continue
			}
			seen[target] = true
			cp := ast.Clone(imp, identity)
			m.convert(cp, file, path.Dir(file), flowDir)
			imps = append(imps, cp)
		}
	}
	addImports(key, imports)
	for i, su := range suites {
		b := ast.Clone(su.block, identity)
		m.convert(b, su.key, path.Dir(su.key), flowDir)
		suiteReads(b)
		from[i] = sortBody(b.Body)
		suiteImports := importsOf(su.tree)
		addImports(su.key, suiteImports)
		for k, c := range m.collections(su.key, suiteImports) {
			if _, has := colls[k]; !has {
				colls[k] = c
			}
		}
	}

	// Vars: a name two of them declare would be declared twice in one flow.
	var body []ast.Decl
	declared := map[string]string{}
	addVar := func(v *ast.VarDecl, by string) error {
		if prev, dup := declared[v.Name.Value]; dup {
			return fmt.Errorf("%s: var %s is declared by both %s and %s, and a flow declares a name once; rename one before migrating",
				key, v.Name.Value, prev, by)
		}
		declared[v.Name.Value] = by
		body = append(body, v)
		return nil
	}
	for i, p := range from {
		for _, v := range p.vars {
			if err := addVar(v, suites[i].key); err != nil {
				return nil, nil, err
			}
		}
	}
	if len(rows) > 0 {
		v := &ast.VarDecl{
			Keyword: word(token.Ident, "var"),
			Name:    word(token.Ident, orig.For.Var.Value),
			Assign:  word(token.Assign, "="),
			Value:   ast.Clone(rows[0].value, identity),
		}
		if err := addVar(v, "its for clause"); err != nil {
			return nil, nil, err
		}
	}
	for _, v := range own.vars {
		if err := addVar(v, "the scenario"); err != nil {
			return nil, nil, err
		}
	}

	// Configs: the innermost one for each subject, the scenario's first.
	kept := map[string]bool{}
	var configs []*ast.ConfigDecl
	keep := func(cs []*ast.ConfigDecl, file string) {
		for _, c := range cs {
			if kept[c.Subject.Value] {
				m.note("%s: config %s is set closer to the scenario, so this one is not copied into %s",
					at(file, c.Keyword), c.Subject.Value, path.Join(flowDir, name+".art"))
				continue
			}
			kept[c.Subject.Value] = true
			configs = append(configs, c)
		}
	}
	keep(own.configs, key)
	for i := len(from) - 1; i >= 0; i-- {
		keep(from[i].configs, suites[i].key)
	}
	for i := len(configs) - 1; i >= 0; i-- {
		body = append(body, configs[i])
	}

	// Setup runs the suites' steps first; teardown runs them last.
	var setups, teardowns []*ast.PhaseDecl
	for _, p := range from {
		setups = append(setups, p.setup)
	}
	setups = append(setups, own.setup)
	teardowns = append(teardowns, own.teardown)
	for i := len(from) - 1; i >= 0; i-- {
		teardowns = append(teardowns, from[i].teardown)
	}
	if ph := merge(setups, own.setup); ph != nil {
		body = append(body, ph)
	}
	body = append(body, own.rest...)
	if ph := merge(teardowns, own.teardown); ph != nil {
		body = append(body, ph)
	}

	m.pin(key, body, colls)

	nameTok := word(token.Ident, token.SnakeFrom(name))
	nameTok.Trailing = s.Name.Trailing
	flow := &ast.Flow{
		Keyword: carry(s.Keyword, word(token.Ident, "flow")),
		Name:    nameTok,
		LBrace:  s.LBrace,
		Body:    body,
		RBrace:  s.RBrace,
	}
	flowFile := &ast.File{Scenarios: append(imps, flow), EOF: eof}

	var tagSets []*ast.TagsDecl
	for _, p := range from {
		tagSets = append(tagSets, p.tags)
	}
	tagSets = append(tagSets, own.tags)
	tags := mergeTags(tagSets)

	data := &ast.File{
		Scenarios: []ast.Decl{&ast.FlowRef{Keyword: word(token.Ident, "flow"), Path: str("../flows/" + name + ".art")}},
		EOF:       token.Token{Kind: token.EOF},
	}
	if len(rows) == 0 {
		sc := dataScenario(bare(s.Name), tags, nil)
		if s.Label.Text != "" {
			// An old scenario already named by an id keeps its label.
			sc.LabelAs, sc.Label = word(token.Ident, "as"), bare(s.Label)
		}
		data.Scenarios = append(data.Scenarios, sc)
	}
	for _, r := range rows {
		set := &ast.Set{Name: word(token.Ident, orig.For.Var.Value), Assign: word(token.Assign, "="), Value: r.value}
		data.Scenarios = append(data.Scenarios, dataScenario(str(r.title), tags, set))
	}
	return flowFile, data, nil
}

// dataScenario is `scenario "name" { tags = [...]  row = ... }`.
func dataScenario(name token.Token, tags *ast.TagsDecl, set *ast.Set) *ast.Scenario {
	sc := &ast.Scenario{
		Keyword: word(token.Ident, "scenario"),
		Name:    name,
		LBrace:  word(token.LBrace, "{"),
		RBrace:  word(token.RBrace, "}"),
	}
	if tags != nil {
		sc.Body = append(sc.Body, ast.Clone(tags, identity))
	}
	if set != nil {
		sc.Body = append(sc.Body, set)
	}
	return sc
}

// merge is one phase holding the steps of each of phases in turn, nil when
// none has any. It is written with the scenario's own phase's words when it
// has one, so the comments around them stay.
func merge(phases []*ast.PhaseDecl, own *ast.PhaseDecl) *ast.PhaseDecl {
	var out *ast.PhaseDecl
	if own != nil {
		cp := *own
		out = &cp
	}
	var body []ast.Decl
	for _, p := range phases {
		if p == nil {
			continue
		}
		if out == nil {
			cp := *p
			out = &cp
		}
		body = append(body, p.Body...)
	}
	if out != nil {
		out.Body = body
	}
	return out
}

// mergeTags is the tags of each of sets in turn, each once: the suites' then
// the scenario's. One set alone is kept as it was written.
func mergeTags(sets []*ast.TagsDecl) *ast.TagsDecl {
	var present []*ast.TagsDecl
	for _, t := range sets {
		if t != nil {
			present = append(present, t)
		}
	}
	switch len(present) {
	case 0:
		return nil
	case 1:
		return present[0]
	}
	arr := &ast.Array{LBracket: word(token.LBracket, "["), RBracket: word(token.RBracket, "]")}
	seen := map[string]bool{}
	for _, t := range present {
		a, ok := t.Value.(*ast.Array)
		if !ok {
			continue
		}
		for _, el := range a.Elems {
			lit, ok := el.Value.(*ast.Literal)
			if !ok || seen[lit.Tok.Value] {
				continue
			}
			seen[lit.Tok.Value] = true
			if n := len(arr.Elems); n > 0 {
				arr.Elems[n-1].Comma = word(token.Comma, ",")
			}
			arr.Elems = append(arr.Elems, ast.Elem{Value: &ast.Literal{Tok: bare(lit.Tok)}})
		}
	}
	return &ast.TagsDecl{Keyword: word(token.Ident, "tags"), Assign: word(token.Assign, "="), Value: arr}
}

func importsOf(tree *ast.File) []*ast.Import {
	var out []*ast.Import
	for _, d := range tree.Scenarios {
		if imp, ok := d.(*ast.Import); ok {
			out = append(out, imp)
		}
	}
	return out
}
