package print

import (
	"fmt"
	"strings"
)

// gen generates *valid* .art files from a byte string.
//
// The gate the issue owns is "any valid file", and a corpus of fixtures is a
// corpus of files someone thought to write. A generator driven by the fuzzer's
// bytes reaches the combinations nobody would: a comment between a field and
// its separator, a block whose inline form is one byte over the margin, a
// trailing comma in a call inside an interpolation inside a header value.
//
// Everything it emits parses and checks clean, which is what makes the strong
// claims assertable on the output -- canonical idempotence, the token stream,
// zero diagnostics after formatting. The faults are pkg/dsl/testdata/invalid's
// job and FuzzPreserving's; this one generates files that are *right* and
// laid out arbitrarily.
type gen struct {
	src []byte
	i   int
	b   strings.Builder

	// vars and caps are what is in scope, so a generated expression names
	// something that resolves: the checker is part of what the generated file
	// is asserted against, and a file full of unknown-identifier would assert
	// nothing about the printer.
	vars []string
	caps []string

	// n makes every generated name unique. Random names collide, and a
	// redeclared var or a step named twice is a question about the checker
	// rather than about the printer.
	n int
}

// next is one byte of the input, cycling when it runs out so a short seed still
// produces a whole file rather than a truncated one.
func (g *gen) next() byte {
	if len(g.src) == 0 {
		return 0
	}
	c := g.src[g.i%len(g.src)]
	g.i++
	return c
}

// pick is a number in [0,n).
func (g *gen) pick(n int) int {
	if n <= 1 {
		return 0
	}
	return int(g.next()) % n
}

// odds reports true about one time in n.
func (g *gen) odds(n int) bool { return g.pick(n) == 0 }

func (g *gen) write(s string) { g.b.WriteString(s) }

// generate is the whole grammar, laid out at random.
func generate(src []byte) string {
	g := &gen{src: src}
	g.lead(0)
	for n := g.pick(2) + 1; n > 0; n-- {
		g.scenario()
		g.write(g.gap())
	}
	if g.odds(3) {
		g.write("# a comment at the end of the file\n")
	}
	return g.b.String()
}

// gap is the layout between two items: a line break, sometimes with blank lines
// around it, which is what the printer's blank-line handling is held to.
func (g *gen) gap() string {
	switch g.pick(6) {
	case 0:
		return "\n\n"
	case 1:
		return "\n\n\n"
	case 2:
		return " \n"
	case 3:
		return "\t\n"
	default:
		return "\n"
	}
}

// lead is the trivia above an item: blank lines and own-line comments at the
// given indentation.
func (g *gen) lead(depth int) {
	if g.odds(4) {
		g.write("\n")
	}
	for n := g.pick(3); n > 0; n-- {
		g.write(g.indent(depth) + "# " + g.words() + "\n")
	}
}

// trail is a comment at the end of a line, the other half of the lexer's trivia
// split.
func (g *gen) trail() {
	if g.odds(4) {
		g.write("  # " + g.words())
	}
}

func (g *gen) words() string {
	words := []string{"why", "this is here", "todo", "see ART-34", "a note -- with dashes"}
	return words[g.pick(len(words))]
}

// indent is the file's layout, deliberately not canonical: tabs, odd widths and
// none at all all have to round trip.
func (g *gen) indent(depth int) string {
	switch g.pick(5) {
	case 0:
		return strings.Repeat("\t", depth)
	case 1:
		return strings.Repeat("    ", depth)
	case 2:
		return strings.Repeat(" ", depth)
	default:
		return strings.Repeat("  ", depth)
	}
}

func (g *gen) scenario() {
	// Scope is per scenario: a var declared in the one above is not in scope
	// here, so carrying the list over would generate files the checker is right
	// to reject.
	g.vars, g.caps = nil, nil

	g.lead(0)
	g.write(`scenario "` + g.name("s") + `" {`)
	g.trail()
	g.write(g.gap())

	if g.odds(3) {
		g.lead(1)
		g.write(g.indent(1) + "config browser " + g.settings(1, []string{"headless = true", `viewport = "1280x720"`}))
		g.trail()
		g.write(g.gap())
	}
	for n := g.pick(3); n > 0; n-- {
		name := g.name("v")
		g.lead(1)
		g.write(g.indent(1) + "var " + name + g.spaces() + "=" + g.spaces() + g.varValue())
		g.trail()
		g.write(g.gap())
		g.vars = append(g.vars, name)
	}
	for n := g.pick(3) + 1; n > 0; n-- {
		g.step()
		g.write(g.gap())
	}
	g.lead(1)
	g.write(g.indent(0) + "}")
	g.trail()
}

// step is one step of a random type, with statements drawn from that type's
// scope -- which is what keeps the generated file clean through the checker.
func (g *gen) step() {
	kind := g.pick(3) // 0 api, 1 terminal, 2 browser
	g.lead(1)
	g.write(g.indent(1) + `step "` + g.name("step") + `" {`)
	g.trail()
	g.write(g.gap())

	// Captures a step writes are in scope only for later steps, so they are
	// collected after the body is written.
	var wrote []string

	g.lead(2)
	g.write(g.indent(2))
	switch kind {
	case 0:
		g.request()
	case 1:
		g.run()
	case 2:
		g.browser()
	}
	g.trail()
	g.write(g.gap())

	if g.odds(3) {
		g.lead(2)
		g.write(g.indent(2) + `timeout = "` + g.duration() + `"`)
		g.trail()
		g.write(g.gap())
	}
	if g.odds(3) {
		g.lead(2)
		g.write(g.indent(2) + "retry " + g.settings(2, []string{"times = 3", `delay = "` + g.duration() + `"`}))
		g.trail()
		g.write(g.gap())
	}
	for n := g.pick(3) + 1; n > 0; n-- {
		g.lead(2)
		g.write(g.indent(2) + "expect " + g.predicate(kind))
		if g.odds(4) {
			g.write(` within "` + g.duration() + `"`)
		}
		g.trail()
		g.write(g.gap())
	}
	if g.odds(2) {
		name := g.name("c")
		g.lead(2)
		g.write(g.indent(2) + "capture " + name + " = " + g.value(kind))
		g.trail()
		g.write(g.gap())
		wrote = append(wrote, name)
	}

	g.lead(2)
	g.write(g.indent(1) + "}")
	g.trail()
	g.caps = append(g.caps, wrote...)
}

func (g *gen) request() {
	methods := []string{"get", "post", "put", "patch", "delete", "head", "options"}
	g.write(methods[g.pick(len(methods))] + " " + g.str("/orders"))
	if g.odds(2) {
		return
	}
	fields := []string{
		`header "Accept" = "application/json"`,
		`query "limit" = 10`,
		`body = ` + g.object(),
	}
	// A request block takes no separator in the grammar, so its fields are one
	// per line -- which is also the shape canonical mode prints them in when
	// there is more than one.
	g.block(3, fields[:g.pick(len(fields))+1], "\n")
}

func (g *gen) run() {
	g.write("run " + g.str("psql"))
	if g.odds(2) {
		return
	}
	fields := []string{
		`args = ["-f", "seed.sql"]`,
		`cwd = "db"`,
		`stdin = "select 1"`,
		`env ` + g.settings(4, []string{`PGPASSWORD = "x"`}),
	}
	g.block(3, fields[:g.pick(len(fields))+1], "\n")
}

func (g *gen) browser() {
	acts := []string{
		`goto ` + g.str("/settings"),
		`click "#go"`,
		`fill "#email" = "alice@example.com"`,
		`press "Enter"`,
		`hover ".row"`,
		// A duration and not a selector: ART-46 made `wait`'s argument the
		// duration every other table in the repo already spelled it as, and a
		// generated file has to check clean for TestGeneratedFilesAreValid to
		// mean anything.
		`wait "250ms"`,
		`select "#plan" = "pro"`,
		`upload "#file" = "a.csv"`,
	}
	g.write("browser ")
	g.block(3, acts[:g.pick(len(acts))+1], "\n")
}

// block writes `{ ... }` with the given items, laid out one per line or all on
// one, and sep between them.
func (g *gen) block(depth int, items []string, sep string) {
	if len(items) == 0 {
		g.write("{}")
		return
	}
	if sep == ", " && g.odds(2) {
		g.write("{ " + strings.Join(items, ", ") + " }")
		return
	}
	g.write("{")
	for _, it := range items {
		g.write("\n")
		if g.odds(5) {
			g.write(g.indent(depth) + "# " + g.words() + "\n")
		}
		g.write(g.indent(depth) + it)
		if sep == ", " && g.odds(3) {
			g.write(",")
		}
		if g.odds(5) {
			g.write("  # " + g.words())
		}
	}
	g.write("\n" + g.indent(depth-1) + "}")
}

// settings is a config, env or retry block: the three the grammar gives a
// comma separator, so they are the ones generated with one.
func (g *gen) settings(depth int, items []string) string {
	sub := &gen{src: g.src, i: g.i}
	sub.block(depth, items, ", ")
	g.i = sub.i
	return sub.b.String()
}

// predicate is an expression that reads as an assertion in the given step's
// scope.
func (g *gen) predicate(kind int) string {
	left := g.value(kind)
	switch g.pick(9) {
	case 0:
		return left + " == " + g.literal()
	case 1:
		return left + " != " + g.literal()
	case 2:
		return left + " > 0"
	case 3:
		return left + ` contains "x"`
	case 4:
		return left + " matches /.+@.+/"
	case 5:
		return left + " exists"
	case 6:
		return left + " is " + []string{"string", "number", "boolean", "object", "array", "null"}[g.pick(6)]
	case 7:
		return "not " + left + " exists"
	default:
		return "(" + left + " exists) and " + g.value(kind) + " exists"
	}
}

// value is something in scope in the given step type, sometimes reached through
// a member, an index or a call.
func (g *gen) value(kind int) string {
	var roots []string
	switch kind {
	case 0:
		roots = []string{"status", "body", "raw", "headers"}
	case 1:
		roots = []string{"exit_code", "stdout", "stderr"}
	default:
		roots = []string{"page.url", "page.title", `text(".x")`, `value("#f")`, `attr(".a", "href")`, `count(".row")`, `visible(".ok")`}
	}
	root := roots[g.pick(len(roots))]
	if len(g.vars) > 0 && g.odds(6) {
		root = g.vars[g.pick(len(g.vars))]
	}
	if len(g.caps) > 0 && g.odds(6) {
		root = g.caps[g.pick(len(g.caps))]
	}
	switch {
	case strings.HasPrefix(root, "body") && g.odds(2):
		return root + ".data.count"
	case strings.HasPrefix(root, "body") && g.odds(2):
		return root + ".items[0].id"
	case root == "headers":
		return root + `["content-type"]`
	default:
		return root
	}
}

func (g *gen) varValue() string {
	switch g.pick(5) {
	case 0:
		return `env("API_URL")`
	case 1:
		return g.object()
	case 2:
		return `["a", "b"` + g.maybeComma() + `]`
	case 3:
		if len(g.vars) > 0 {
			return `"${` + g.vars[g.pick(len(g.vars))] + `}/orders"`
		}
		return g.literal()
	default:
		return g.literal()
	}
}

func (g *gen) object() string {
	return `{"a": 1, "b": ` + g.literal() + g.maybeComma() + `}`
}

func (g *gen) maybeComma() string {
	if g.odds(4) {
		return ","
	}
	return ""
}

func (g *gen) literal() string {
	switch g.pick(8) {
	case 0:
		return "42"
	case 1:
		return "3.5"
	case 2:
		return "-7"
	case 3:
		return "true"
	case 4:
		return "null"
	case 5:
		return `"plain"`
	case 6:
		return `"costs \$5"`
	default:
		return `"a ${` + g.envCall() + `} b"`
	}
}

func (g *gen) envCall() string { return `env("REGION")` }

// str is a string literal, sometimes interpolated with a var that is in scope.
func (g *gen) str(s string) string {
	if len(g.vars) > 0 && g.odds(2) {
		return `"${` + g.vars[g.pick(len(g.vars))] + `}` + s + `"`
	}
	return `"` + s + `"`
}

func (g *gen) duration() string {
	return []string{"1s", "250ms", "2m", "1h30m"}[g.pick(4)]
}

// spaces is the whitespace around an `=`: zero, one, or the aligned run the
// design document's own example uses.
func (g *gen) spaces() string {
	return []string{" ", " ", "  ", "   ", " \t"}[g.pick(5)]
}

func (g *gen) name(prefix string) string {
	g.n++
	if prefix == "step" {
		return fmt.Sprintf("%s %d", prefix, g.n)
	}
	return fmt.Sprintf("%s%d", prefix, g.n)
}
