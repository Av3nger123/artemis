package codegen

import (
	"path/filepath"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// dataDir is the directory a json(), csv() or file() call resolves its path
// against: the directory of the file the call is written in, as that file's
// path was given to `artemis build`. The interpreter resolves the same way; a
// generated test finds the file from the directory it runs in, which is what
// each target's header says.
func dataDir(c *ast.Call) string {
	file := c.Span().File
	if file == "" {
		return ""
	}
	dir := filepath.Dir(file)
	if dir == "." {
		return ""
	}
	return filepath.ToSlash(dir)
}

// literalPath is a call's path argument when it is a plain string, already
// joined to dir: a literal is resolved here, once, so the generated line names
// the file it reads.
func literalPath(c *ast.Call, dir string) (string, bool) {
	if len(c.Args) != 1 {
		return "", false
	}
	lit, ok := c.Args[0].Value.(*ast.Literal)
	if !ok || lit == nil || lit.Kind() != token.String {
		return "", false
	}
	if dir == "" || filepath.IsAbs(lit.Tok.Value) {
		return filepath.ToSlash(lit.Tok.Value), true
	}
	return filepath.ToSlash(filepath.Join(dir, lit.Tok.Value)), true
}
