package front

import (
	"fmt"
	"path/filepath"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// The old forms -- a scenario that holds its steps, capture, use, an item's
// parameters, secret, suites and for rows -- are read by the parser still,
// because artemis migrate rewrites them from the tree, and a plain scenario
// is the form expansion makes of a scenario file, which the checker and
// lowering read. What a person writes is judged here, over the file as
// written and every file it reads, so run, parse and the MCP server's
// validate all say the same about it.
//
// An old scenario or suite is reported once, as a whole: migrate rewrites
// everything in it, and a line for each capture inside would bury the one
// thing to do. A flow or a collection is the new form, so each old line in
// one is reported on its own.

// suiteFile is the name an old folder's suite had.
const suiteFile = "suite.art"

// oldForms reports each old form in tree, the file named file, into bag, and
// reports whether there was one.
func oldForms(file string, tree *ast.File, bag *diag.Bag) bool {
	if tree == nil {
		return false
	}
	o := &olds{file: file, bag: bag}
	var header, flow, coll bool
	for _, d := range tree.Scenarios {
		switch d.(type) {
		case *ast.FlowRef:
			header = true
		case *ast.Flow:
			flow = true
		case *ast.Collection:
			coll = true
		}
	}
	isSuite := filepath.Base(file) == suiteFile
	suite := false
	for _, d := range tree.Scenarios {
		switch v := d.(type) {
		case *ast.Scenario:
			switch {
			case v.Keyword.Value == "suite":
				suite = true
				o.suite(v.Keyword)
			case isSuite || header || flow || coll:
				// A scenario in a scenario file is data; in a flow file or a
				// collection file the parser and expansion say where it
				// belongs. Either way its name is an id.
				o.quoted("scenario", v.Name, "")
			case v.For != nil:
				o.forClause(v)
			default:
				o.scenario(v)
			}
		case *ast.Flow:
			o.inner(v)
		case *ast.Collection:
			o.quoted("collection", v.Name, collectionID(file))
			for _, it := range v.Items {
				if r, ok := it.(*ast.RequestDecl); ok {
					o.item(r)
				}
			}
		}
	}
	if isSuite && !suite {
		at := tree.EOF
		if toks := tree.Tokens(nil); len(toks) > 0 {
			at = toks[0]
		}
		o.suite(at)
	}
	return o.found
}

// olds is one file's pass.
type olds struct {
	file  string
	bag   *diag.Bag
	found bool
}

// report is one old form at at, with a hint that ends by naming migrate.
func (o *olds) report(at token.Span, code diag.Code, msg, hint string) {
	o.found = true
	o.bag.Error(at, code, "%s", msg).
		Hintf("%s -- artemis migrate -f %s -o <dir> rewrites it", hint, o.file)
}

func (o *olds) scenario(s *ast.Scenario) {
	o.report(s.Keyword.Span, diag.OldScenario,
		fmt.Sprintf("scenario %s holds steps, and a scenario now holds data only", s.Name.Text),
		`put its vars and steps in a flow file, flow <name> { ... }, and the scenario in a scenario file whose first line is flow "<path to the flow>"`)
}

func (o *olds) forClause(s *ast.Scenario) {
	o.report(s.For.Keyword.Span, diag.OldFor,
		fmt.Sprintf("scenario %s reads rows with for, and a scenario file now holds one scenario for each row", s.Name.Text),
		fmt.Sprintf("write each row as a scenario that sets %s, in a scenario file of the flow its steps go in", s.For.Var.Value))
}

func (o *olds) suite(at token.Token) {
	o.report(at.Span, diag.OldSuite,
		"a suite is removed: a folder and --tags select what runs",
		"copy its vars, setup and teardown into each flow under the folder, and its tags onto each scenario")
}

// item reports a collection item's parameters, then its body's old lines.
func (o *olds) item(r *ast.RequestDecl) {
	if r.Params != nil {
		first := "user"
		if len(r.Params.List) > 0 && r.Params.List[0].Name.Value != "" {
			first = r.Params.List[0].Name.Value
		}
		o.report(r.Params.LParen.Span, diag.OldParams,
			fmt.Sprintf("%s %s declares parameters, and an item's inputs are now vars", r.Keyword.Value, r.Name.Text),
			fmt.Sprintf("declare each one as a var above the action, var %s, and drop the parentheses", first))
	}
	o.inner(r)
}

// inner reports the old lines anywhere under n: a capture, a use and
// secret.
func (o *olds) inner(n ast.Node) {
	ast.Inspect(n, func(x ast.Node) {
		switch v := x.(type) {
		case *ast.VarDecl:
			o.secret(v.Secret, "var "+v.Name.Value)
		case *ast.Capture:
			o.secret(v.Secret, "capture "+v.Name.Value)
			if v.Keyword.Value == "capture" {
				o.report(v.Keyword.Span, diag.OldCapture, "capture is now var",
					fmt.Sprintf("write var %s = ... after the step's action", v.Name.Value))
			}
		case *ast.UseDecl:
			if v.Step.Text == "" && v.Keyword.Text != "" {
				o.report(v.Keyword.Span, diag.OldUse,
					fmt.Sprintf(`use is now step <id> = %s`, v.Ref()),
					fmt.Sprintf(`name the step: step <id> = %s { ... }`, v.Ref()))
			}
			if v.Step.Text != "" {
				o.quoted("step", v.Name, "")
			}
		case *ast.StepDecl:
			o.quoted("step", v.Name, "")
		case *ast.Param:
			o.secret(v.Secret, "parameter "+v.Name.Value)
		}
	})
}

// quoted reports a step, scenario or collection named in quotes: the name is
// a snake_case id now, and the text its label, unless it already is the id.
// id is the id to give it, "" for the text made snake_case.
func (o *olds) quoted(what string, name token.Token, id string) {
	if name.Kind != token.String || name.Span.IsZero() {
		return
	}
	if id == "" {
		id = token.SnakeFrom(name.Value)
	}
	form := what + " " + id
	if name.Value != id {
		form += " as " + name.Text
	}
	o.report(name.Span, diag.QuotedName,
		fmt.Sprintf("a %s's name is a snake_case id now: %s", what, form),
		"write "+form)
}

// collectionID is the id a collection in file is given: the name an import
// with no as gives the file, when that is an id, and else its name made one.
func collectionID(file string) string {
	if id, ok := parser.ImportName(file); ok && token.IsSnakeID(id) {
		return id
	}
	base := filepath.Base(file)
	return token.SnakeFrom(strings.TrimSuffix(base, filepath.Ext(base)))
}

func (o *olds) secret(t token.Token, what string) {
	if t.Text == "" {
		return
	}
	o.report(t.Span, diag.OldSecret,
		fmt.Sprintf("secret is removed, from %s", what),
		"remove the word; reports no longer withhold the value")
}
