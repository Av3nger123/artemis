package migrate

import (
	"path"
	"reflect"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// word is a token that was never in a file: an identifier, a keyword or an
// operator, with no span and no trivia, which is what marks it as new to the
// printer.
func word(kind token.Kind, text string) token.Token {
	t := token.Token{Kind: kind, Text: text}
	if kind == token.Ident {
		t.Value = text
	}
	return t
}

// str is a string literal's token for s, its text quoted with the escapes the
// lexer reads back.
func str(s string) token.Token {
	return token.Token{Kind: token.String, Text: quote(s), Value: s}
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// retext is t spelled text, keeping its trivia: the comment above `capture`
// stays above the `var` that replaces it.
func retext(t token.Token, text string) token.Token {
	t.Text, t.Value = text, text
	return t
}

// carry is to with from's leading trivia in front of its own, so that a word
// removed from the start of a line does not take the comment above it along.
func carry(from, to token.Token) token.Token {
	if len(from.Leading) == 0 {
		return to
	}
	to.Leading = append(append([]token.Trivia{}, from.Leading...), to.Leading...)
	return to
}

// bare is t with no trivia and no span: the same word, written somewhere new.
func bare(t token.Token) token.Token {
	return token.Token{Kind: t.Kind, Text: t.Text, Value: t.Value}
}

func identity(t token.Token) token.Token { return t }

var (
	exprType  = reflect.TypeOf((*ast.Expr)(nil)).Elem()
	tokenType = reflect.TypeOf(token.Token{})
)

// rewriteExprs replaces every expression under n with f of it, children
// first, so f sees a member whose object has already been rewritten. It is
// reflective, like ast.Clone, so a field added to a node is reached without an
// edit here. Only a field whose type is ast.Expr is replaced; a Call's callee,
// which is an *ast.Ident, is a builtin's name and never a value.
func rewriteExprs(n ast.Node, f func(ast.Expr) ast.Expr) {
	rewriteValue(reflect.ValueOf(n), f)
}

func rewriteValue(v reflect.Value, f func(ast.Expr) ast.Expr) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			rewriteValue(v.Elem(), f)
		}
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		rewriteValue(v.Elem(), f)
		if v.Type() == exprType && v.CanSet() {
			if x := f(v.Interface().(ast.Expr)); x != nil {
				v.Set(reflect.ValueOf(x))
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			rewriteValue(v.Index(i), f)
		}
	case reflect.Struct:
		if v.Type() == tokenType {
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				rewriteValue(v.Field(i), f)
			}
		}
	}
}

// reads reports whether x reads a name: an identifier anywhere in it that is
// not a call's callee.
func reads(x ast.Expr) bool {
	callee := map[*ast.Ident]bool{}
	found := false
	ast.Inspect(x, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.Call:
			callee[n.Callee] = true
		case *ast.Ident:
			if !callee[n] {
				found = true
			}
		}
	})
	return found
}

// rebase is p, a path written in a file in from, as a file in to would write
// it. Both directories are slash paths relative to the input root. An
// absolute path is itself.
func rebase(p, from, to string) string {
	if path.IsAbs(p) {
		return p
	}
	target := strings.Split(path.Join(from, p), "/")
	base := strings.Split(path.Clean(to), "/")
	if base[0] == "." {
		base = nil
	}
	if len(target) == 1 && target[0] == "." {
		target = nil
	}
	i := 0
	for i < len(target) && i < len(base) && target[i] == base[i] && target[i] != ".." {
		i++
	}
	parts := make([]string, 0, len(base)-i+len(target)-i)
	for range base[i:] {
		parts = append(parts, "..")
	}
	parts = append(parts, target[i:]...)
	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

// ancestor reports whether above is dir or a folder above it, both slash
// paths relative to the input root. The root, and a folder above it such as
// "../..", is above every file the input holds.
func ancestor(above, dir string) bool {
	above, dir = path.Clean(above), path.Clean(dir)
	up := true
	for _, seg := range strings.Split(above, "/") {
		up = up && (seg == ".." || seg == ".")
	}
	if up {
		return true
	}
	return dir == above || strings.HasPrefix(dir, above+"/")
}
