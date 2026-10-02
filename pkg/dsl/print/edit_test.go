package print

import (
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// These are R6, the payoff. A UI loads a file, changes one field, writes it
// back; the diff has to touch only the lines the user changed, because that is
// what keeps a .art file reviewable in a pull request.
//
// Each case asserts the *diff*, not just that the output re-parses. A test that
// only checked the output were still valid would pass on a file the printer had
// reformatted from top to bottom, which is exactly the failure this issue
// exists to prevent.

// edit is one case: a mutation of a parsed tree, and the lines the printed file
// is allowed to differ on.
type edit struct {
	name    string
	mutate  func(t *testing.T, f *ast.File)
	added   []string // lines in the output that were not in the input
	removed []string // lines in the input that are not in the output
}

func TestAnEditTouchesOnlyItsOwnLines(t *testing.T) {
	src := read(t, filepath.Join("..", "parser", "testdata", "checkout.art"))

	cases := []edit{{
		name: "a field's value",
		mutate: func(t *testing.T, f *ast.File) {
			field(t, f, "timeout").Value = &ast.Literal{Tok: Synthetic(token.String, `"30s"`)}
		},
		removed: []string{`    timeout = "5s"`},
		added:   []string{`    timeout = "30s"`},
	}, {
		name: "a step's name",
		mutate: func(t *testing.T, f *ast.File) {
			step(t, f, `"login"`).Name = Synthetic(token.String, `"sign in"`)
		},
		removed: []string{`  step "login" {`},
		added:   []string{`  step "sign in" {`},
	}, {
		name: "an expect's expression",
		mutate: func(t *testing.T, f *ast.File) {
			s := step(t, f, `"login"`)
			for _, st := range s.Body {
				e, ok := st.(*ast.Expect)
				if !ok {
					continue
				}
				e.Value = &ast.Binary{
					X:  &ast.Ident{Tok: Synthetic(token.Ident, "status")},
					Op: Synthetic(token.Eq, "=="),
					Y:  &ast.Literal{Tok: Synthetic(token.Number, "201")},
				}
				return
			}
			t.Fatal("no expect in the login step")
		},
		removed: []string{"    expect status == 200"},
		added:   []string{"    expect status == 201"},
	}, {
		name: "a new field appended to a block",
		mutate: func(t *testing.T, f *ast.File) {
			b := block(t, f, "query")
			b.Fields = append(b.Fields, &ast.Field{
				Name:   Synthetic(token.Ident, "query"),
				Key:    &ast.Literal{Tok: Synthetic(token.String, `"offset"`)},
				Assign: Synthetic(token.Assign, "="),
				Value:  &ast.Literal{Tok: Synthetic(token.Number, "20")},
			})
		},
		added: []string{`      query "offset" = 20`},
	}, {
		name: "a new statement appended to a step",
		mutate: func(t *testing.T, f *ast.File) {
			s := step(t, f, `"login"`)
			s.Body = append(s.Body, &ast.Capture{
				Keyword: Synthetic(token.Ident, "capture"),
				Name:    Synthetic(token.Ident, "refresh"),
				Assign:  Synthetic(token.Assign, "="),
				Value: &ast.Member{
					X:    &ast.Ident{Tok: Synthetic(token.Ident, "body")},
					Dot:  Synthetic(token.Dot, "."),
					Name: Synthetic(token.Ident, "refresh_token"),
				},
			})
		},
		added: []string{"    capture refresh = body.refresh_token"},
	}, {
		name: "a new step appended to a scenario",
		mutate: func(t *testing.T, f *ast.File) {
			sc := f.Scenarios[0].(*ast.Scenario)
			sc.Body = append(sc.Body, &ast.StepDecl{
				Keyword: Synthetic(token.Ident, "step"),
				Name:    Synthetic(token.String, `"log out"`),
				LBrace:  Synthetic(token.LBrace, "{"),
				Action: &ast.Request{
					Method: Synthetic(token.Ident, "post"),
					URL:    &ast.Literal{Tok: Synthetic(token.String, `"/logout"`)},
				},
				Body: []ast.Stmt{&ast.Expect{
					Keyword: Synthetic(token.Ident, "expect"),
					Value: &ast.Binary{
						X:  &ast.Ident{Tok: Synthetic(token.Ident, "status")},
						Op: Synthetic(token.Eq, "=="),
						Y:  &ast.Literal{Tok: Synthetic(token.Number, "204")},
					},
				}},
				RBrace: Synthetic(token.RBrace, "}"),
			})
		},
		added: []string{
			`  step "log out" {`,
			`    post "/logout"`,
			"    expect status == 204",
			"  }",
		},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree, bag := parser.Parse("checkout.art", src)
			if bag.Len() > 0 {
				t.Fatalf("the fixture does not parse clean: %v", bag.All())
			}
			c.mutate(t, tree)

			out := Preserving(tree)
			added, removed := lineDiff(src, out)
			assertLines(t, "added", added, c.added)
			assertLines(t, "removed", removed, c.removed)

			// The edited file must still be a file: an edit that printed
			// something unparseable would have a small diff and be useless.
			outTree, outBag := parser.Parse("edited.art", out)
			if outBag.Len() > 0 {
				t.Errorf("the edited file does not parse:\n%s\n%v", out, outBag.All())
			}
			if _, checked := check.Check(outTree); checked.Len() > 0 {
				for _, d := range checked.All() {
					t.Errorf("the edited file does not check: %d:%d %s %s", d.Span.Line, d.Span.Col, d.Code, d.Message)
				}
			}
		})
	}
}

// TestAnEditedTreeStillPrintsEverythingElseVerbatim is the same claim from the
// other side: the bytes outside the edited line are not merely *equal* line by
// line, they are the same bytes. `var pw  = env("API_PASSWORD")` keeps its two
// spaces, the aligned `args  =` keeps its alignment, and the comment at the top
// of the file stays where it was.
func TestAnEditedTreeStillPrintsEverythingElseVerbatim(t *testing.T) {
	src := read(t, filepath.Join("..", "parser", "testdata", "checkout.art"))
	tree, _ := parser.Parse("checkout.art", src)
	field(t, tree, "timeout").Value = &ast.Literal{Tok: Synthetic(token.String, `"30s"`)}

	out := Preserving(tree)
	before, after, ok := strings.Cut(src, `    timeout = "5s"`)
	if !ok {
		t.Fatal("the fixture no longer holds the timeout line this test edits")
	}
	if want := before + `    timeout = "30s"` + after; out != want {
		t.Errorf("an edited file differs outside the edited line\n got:\n%s\nwant:\n%s", out, want)
	}
}

// TestSyntheticMarksANode pins the contract ART-43's decoder and ART-51's UI are
// built against: a token built by Synthetic reads as an edit, and one read from
// a file does not.
func TestSyntheticMarksANode(t *testing.T) {
	tok := Synthetic(token.Ident, "status")
	if !synthetic(tok) {
		t.Error("a token from Synthetic must read as synthetic")
	}
	if tok.Value != "status" {
		t.Errorf("Synthetic(Ident).Value = %q, want the text", tok.Value)
	}
	if s := Synthetic(token.String, `"30s"`); s.Value != "" {
		t.Errorf("Synthetic(String).Value = %q; decoding a string literal is the lexer's job", s.Value)
	}

	src := "scenario \"s\" {\n  var a = 1\n}\n"
	tree, _ := parser.Parse("s.art", src)
	if !intact(tree) {
		t.Error("a parsed tree must not read as edited")
	}
	v := tree.Scenarios[0].(*ast.Scenario).Body[0].(*ast.VarDecl)
	v.Value = &ast.Literal{Tok: Synthetic(token.Number, "2")}
	if intact(tree) {
		t.Error("a tree holding a synthetic token must read as edited")
	}
	if got, want := Preserving(tree), "scenario \"s\" {\n  var a = 2\n}\n"; got != want {
		t.Errorf("Preserving = %q, want %q", got, want)
	}
}

// TestANewNodeIsIndentedLikeItsNeighbours is why preserving mode reads the
// indentation off the file rather than using this package's constants: a block
// indented with tabs gets a tab.
func TestANewNodeIsIndentedLikeItsNeighbours(t *testing.T) {
	src := "scenario \"s\" {\n\tstep \"one\" {\n\t\tget \"/a\"\n\t\texpect status == 200\n\t}\n}\n"
	tree, _ := parser.Parse("tabs.art", src)
	s := tree.Scenarios[0].(*ast.Scenario).Body[0].(*ast.StepDecl)
	s.Body = append(s.Body, &ast.Field{
		Name:   Synthetic(token.Ident, "timeout"),
		Assign: Synthetic(token.Assign, "="),
		Value:  &ast.Literal{Tok: Synthetic(token.String, `"5s"`)},
	})

	out := Preserving(tree)
	if !strings.Contains(out, "\n\t\ttimeout = \"5s\"\n") {
		t.Errorf("a new statement in a tab-indented file must be indented with tabs:\n%q", out)
	}
}

// TestANewStatementGoesAfterTheAction is the ordering trap ast.StepDecl.items
// sets: it orders a step's items by byte offset, and a synthetic node's offset
// is zero, so an appended statement would print *above* the action block --
// where it is an action-not-first diagnostic rather than the line the user
// asked for.
func TestANewStatementGoesAfterTheAction(t *testing.T) {
	src := "scenario \"s\" {\n  step \"one\" {\n    get \"/a\"\n  }\n}\n"
	tree, _ := parser.Parse("order.art", src)
	s := tree.Scenarios[0].(*ast.Scenario).Body[0].(*ast.StepDecl)
	s.Body = append(s.Body, &ast.Expect{
		Keyword: Synthetic(token.Ident, "expect"),
		Value: &ast.Binary{
			X:  &ast.Ident{Tok: Synthetic(token.Ident, "status")},
			Op: Synthetic(token.Eq, "=="),
			Y:  &ast.Literal{Tok: Synthetic(token.Number, "200")},
		},
	})

	out := Preserving(tree)
	want := "scenario \"s\" {\n  step \"one\" {\n    get \"/a\"\n    expect status == 200\n  }\n}\n"
	if out != want {
		t.Errorf("an appended statement must follow the action\n got: %q\nwant: %q", out, want)
	}
	if _, bag := parser.Parse("order.art", out); bag.Len() > 0 {
		t.Errorf("the edited file does not parse: %v", bag.All())
	}
}

// field finds the first ast.Field with the given name, which is how these tests
// reach into the fixture without hard-coding a path through it.
func field(t *testing.T, f *ast.File, name string) *ast.Field {
	t.Helper()
	var found *ast.Field
	ast.Inspect(f, func(n ast.Node) {
		if v, ok := n.(*ast.Field); ok && found == nil && v.Name.Value == name {
			found = v
		}
	})
	if found == nil {
		t.Fatalf("no field named %q in the fixture", name)
	}
	return found
}

// block finds the block holding the first field with the given name.
func block(t *testing.T, f *ast.File, fieldName string) *ast.Block {
	t.Helper()
	var found *ast.Block
	ast.Inspect(f, func(n ast.Node) {
		b, ok := n.(*ast.Block)
		if !ok || found != nil {
			return
		}
		for _, fl := range b.Fields {
			if v, ok := fl.(*ast.Field); ok && v.Name.Value == fieldName {
				found = b
			}
		}
	})
	if found == nil {
		t.Fatalf("no block holding a %q field in the fixture", fieldName)
	}
	return found
}

func step(t *testing.T, f *ast.File, name string) *ast.StepDecl {
	t.Helper()
	var found *ast.StepDecl
	ast.Inspect(f, func(n ast.Node) {
		if v, ok := n.(*ast.StepDecl); ok && found == nil && v.Name.Text == name {
			found = v
		}
	})
	if found == nil {
		t.Fatalf("no step named %s in the fixture", name)
	}
	return found
}

// lineDiff is the lines added and removed between two files, as a multiset
// difference. It is not a unified diff and does not need to be: the claim is
// about *which* lines moved, and a case naming none of them asserts the file is
// identical apart from the lines it lists.
func lineDiff(before, after string) (added, removed []string) {
	count := map[string]int{}
	for _, l := range strings.Split(before, "\n") {
		count[l]++
	}
	for _, l := range strings.Split(after, "\n") {
		count[l]--
	}
	for l, n := range count {
		for ; n > 0; n-- {
			removed = append(removed, l)
		}
		for ; n < 0; n++ {
			added = append(added, l)
		}
	}
	return added, removed
}

func assertLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	left := append([]string(nil), got...)
	for _, w := range want {
		if !removeOne(&left, w) {
			t.Errorf("expected the line %q to be %s; it was not", w, what)
		}
	}
	for _, extra := range left {
		if strings.TrimSpace(extra) == "" {
			continue // a split artefact at the end of the file
		}
		t.Errorf("the edit also %s the line %q, which it had no business touching", what, extra)
	}
}
