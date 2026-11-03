package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"artemis/pkg/dsl/ast"
	artmigrate "artemis/pkg/dsl/migrate"
	"artemis/pkg/dsl/parser"

	"github.com/spf13/cobra"
)

// migrateArt migrates old-form .art files: the one file src names, or every
// .art file in the folder it names. The new files go under out, at their paths
// relative to the root -- src itself when it is a folder, its folder when it
// is a file -- and nothing is written over an input.
func migrateArt(cmd *cobra.Command, src, out string) error {
	if out == "" {
		return fmt.Errorf("migrating %s writes several files; name a folder for them with -o <dir>", src)
	}
	if info, err := os.Stat(out); err == nil && !info.IsDir() {
		return fmt.Errorf("-o %s is a file; migrating %s files writes a folder of them", out, artExt)
	}
	root, start, err := migrationStart(src, out)
	if err != nil {
		return err
	}
	in, err := migrationInput(root, start)
	if err != nil {
		return err
	}
	files, notes, err := artmigrate.Files(in)
	if err != nil {
		return fmt.Errorf("migrate %s: %w", src, err)
	}

	inputs := map[string]bool{}
	for k := range in {
		inputs[absPath(filepath.Join(root, filepath.FromSlash(k)))] = true
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Every write is checked before the first is made, so a refusal leaves
	// nothing half-written.
	var writes []string
	for _, k := range keys {
		if strings.HasPrefix(k, "../") {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: %s is outside %s, so its new form is not written\n",
				filepath.Join(root, filepath.FromSlash(k)), root)
			continue
		}
		if dst := filepath.Join(out, filepath.FromSlash(k)); inputs[absPath(dst)] {
			return fmt.Errorf("-o %s would write over %s; migrate never changes the originals, so name another folder", out, dst)
		}
		writes = append(writes, k)
	}
	for _, k := range writes {
		dst := filepath.Join(out, filepath.FromSlash(k))
		if err := ensureWritable(dst); err != nil {
			return err
		}
		if err := writeFormatted(dst, files[k]); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), dst)
	}
	for _, n := range notes {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s\n", n)
	}
	return nil
}

// migrationStart is the root the input's paths are relative to, and the .art
// files to migrate. A folder's walk skips hidden folders, and out when it is
// inside the folder, so a second run does not read the first one's output.
func migrationStart(src, out string) (root string, start []string, err error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() {
		return filepath.Dir(src), []string{src}, nil
	}
	skip := absPath(out)
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != src && (strings.HasPrefix(d.Name(), ".") || absPath(p) == skip) {
			return filepath.SkipDir
		}
		if !d.IsDir() && isArtFile(p) {
			start = append(start, p)
		}
		return nil
	})
	return src, start, err
}

// migrationInput is what artmigrate.Files reads: the files to migrate and
// everything they need from disk -- the suites above them, the collections
// they import, and the data their for clauses read rows from -- by slash path
// relative to root.
func migrationInput(root string, start []string) (map[string]string, error) {
	in := map[string]string{}
	queue := append([]string{}, start...)
	seen := map[string]bool{}
	add := func(p string) {
		if _, err := os.Stat(p); err == nil && !seen[filepath.Clean(p)] {
			queue = append(queue, p)
		}
	}
	for len(queue) > 0 {
		p := filepath.Clean(queue[0])
		queue = queue[1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		b, err := os.ReadFile(p) //nolint:gosec // a file the user named, or one it imports
		if err != nil {
			return nil, err
		}
		in[relKey(root, p)] = string(b)
		if !isArtFile(p) {
			continue
		}
		for _, s := range suitesAbove(p) {
			add(s)
		}
		tree, _ := parser.Parse(p, string(b))
		for _, d := range tree.Scenarios {
			switch v := d.(type) {
			case *ast.Import:
				add(filepath.Join(filepath.Dir(p), filepath.FromSlash(v.Path.Value)))
			case *ast.Scenario:
				if v.For != nil {
					for _, data := range dataPaths(v.For.Source) {
						add(filepath.Join(filepath.Dir(p), filepath.FromSlash(data)))
					}
				}
			}
		}
	}
	return in, nil
}

// suitesAbove is where an old suite.art that applies to file could be: one in
// each directory from file's own up to the working directory -- or, for a
// path outside it, up to the root of the filesystem. A suite file is given its
// parents'. Which of them exist is the caller's to find out.
func suitesAbove(file string) []string {
	dir := filepath.Dir(file)
	if filepath.Base(file) == "suite.art" {
		if dir == "." || dir == string(filepath.Separator) {
			return nil
		}
		dir = filepath.Dir(dir)
	}
	stop := "."
	if filepath.IsAbs(dir) {
		stop = string(filepath.Separator)
		if wd, err := os.Getwd(); err == nil && within(dir, wd) {
			stop = wd
		}
	}
	var out []string
	for {
		out = append(out, filepath.Join(dir, "suite.art"))
		if dir == stop || dir == "." || dir == string(filepath.Separator) {
			return out
		}
		dir = filepath.Dir(dir)
	}
}

// within reports whether dir is root or below it.
func within(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// dataPaths is the literal paths json() and csv() read in x.
func dataPaths(x ast.Expr) []string {
	var out []string
	ast.Inspect(x, func(n ast.Node) {
		c, ok := n.(*ast.Call)
		if !ok || c.Callee == nil || (c.Callee.Name() != "json" && c.Callee.Name() != "csv") || len(c.Args) != 1 {
			return
		}
		if lit, ok := c.Args[0].Value.(*ast.Literal); ok && !filepath.IsAbs(lit.Tok.Value) {
			out = append(out, lit.Tok.Value)
		}
	})
	return out
}

// relKey is p as a key of the input: a slash path relative to root, which
// starts "../" for a file above it.
func relKey(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

// absPath is p made absolute, or p when it cannot be.
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}
