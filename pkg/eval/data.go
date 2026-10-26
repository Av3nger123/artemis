package eval

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"artemis/pkg/dsl/ast"
)

// File is a file a scenario named with file(): a path, not its contents.
//
// Where a step sends content -- `body = file(p)`, a multipart part, `stdin` --
// the file's bytes are sent, and read only then, so a large upload is streamed
// rather than held. Everywhere else a file is the absolute path it resolved to:
// in `args`, in an interpolation, in `upload`. That one rule is why a file is a
// value of its own and not a string: the two uses need different things, and a
// string could not say which it was.
type File struct {
	// Abs is the path the file resolved to, absolute.
	Abs string
	// Rel is the path as the scenario wrote it, for a report or a trace that
	// should not print the machine's directory layout.
	Rel string
}

// MarshalJSON refuses: a file has no JSON form, and a file inside a JSON body
// is a mistake that sending `{"Abs": ...}` would hide. The caller that meets
// this error says what to write instead.
func (File) MarshalJSON() ([]byte, error) {
	return nil, ErrFileInJSON
}

// ErrFileInJSON is what writing a file into JSON gives: to_json(), or a body
// object with a file() inside it.
var ErrFileInJSON = errors.New("a file cannot be written as JSON; send it with `body = file(...)` or a `multipart` part, or its contents with base64(file(...))")

// ContainsFile reports whether v is, or holds, a File: what a JSON body has to
// be checked for before it is rendered.
func ContainsFile(v any) bool {
	switch t := v.(type) {
	case File:
		return true
	case []any:
		for _, e := range t {
			if ContainsFile(e) {
				return true
			}
		}
	case map[string]any:
		for _, e := range t {
			if ContainsFile(e) {
				return true
			}
		}
	}
	return false
}

// ResolvePath is where a path written in a file resolves: relative to the
// directory of that file, so a `file()` in a collection is found next to the
// collection and not next to whichever scenario imported it. An absolute path is
// itself. from is a span's File, which is how a call knows where it was written;
// an empty one is the working directory.
func ResolvePath(from, path string) string {
	if !filepath.IsAbs(path) && from != "" {
		path = filepath.Join(filepath.Dir(from), path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// dataCache holds what json() and csv() parsed, keyed by kind, absolute path,
// size and modification time: a data file read by five hundred rows is parsed
// once, and one edited between two runs of a long-lived process -- the MCP
// server -- is read again.
var dataCache sync.Map

type dataKey struct {
	kind, abs string
	size      int64
	mod       int64
}

// evalDataFn evaluates json(), csv() and file(). Each takes a path, resolved
// against the file the call is written in.
func evalDataFn(c *ast.Call, name, path string) (any, error) {
	abs := ResolvePath(c.Span().File, path)
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errorf(c.Span(), "%s(): there is no file %s (looked for %s)", name, path, abs)
		}
		return nil, errorf(c.Span(), "%s(): %v", name, err)
	}
	if info.IsDir() {
		return nil, errorf(c.Span(), "%s(): %s is a directory, not a file", name, path)
	}
	if name == "file" {
		return File{Abs: abs, Rel: path}, nil
	}

	key := dataKey{kind: name, abs: abs, size: info.Size(), mod: info.ModTime().UnixNano()}
	if v, ok := dataCache.Load(key); ok {
		return v, nil
	}
	b, err := os.ReadFile(abs) //nolint:gosec // the path is the one the scenario names
	if err != nil {
		return nil, errorf(c.Span(), "%s(): %v", name, err)
	}
	var v any
	if name == "json" {
		v, err = ParseJSON(b)
	} else {
		v, err = ParseCSV(b)
	}
	if err != nil {
		return nil, errorf(c.Span(), "%s(): %s is not %s: %v", name, path, map[string]string{"json": "JSON", "csv": "CSV"}[name], err)
	}
	dataCache.Store(key, v)
	return v, nil
}

// ParseJSON is a data file's JSON as a value in the evaluator's domain, with
// the line and column of the first fault when it is not JSON.
func ParseJSON(b []byte) (any, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			// Offset counts the byte that was wrong; the column names it.
			line, col := position(b, max(syntax.Offset-1, 0))
			return nil, fmt.Errorf("line %d, column %d: %v", line, col, err)
		}
		return nil, err
	}
	return v, nil
}

// ParseCSV is a CSV file as an array of objects: the first row names the
// columns, and each later row is an object of those names to its cells, every
// cell a string. A row with more or fewer cells than the header is an error
// naming its line, and so is a column name used twice -- either would make a
// row's object quietly lose a value.
func ParseCSV(b []byte) (any, error) {
	r := csv.NewReader(bytes.NewReader(b))
	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return []any{}, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, name := range header {
		if seen[name] {
			return nil, fmt.Errorf("line 1: the column %q is named twice", name)
		}
		seen[name] = true
	}
	rows := []any{}
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(header))
		for i, name := range header {
			row[name] = record[i]
		}
		rows = append(rows, row)
	}
}

// position is the 1-based line and column of a byte offset.
func position(b []byte, offset int64) (line, col int) {
	if offset > int64(len(b)) {
		offset = int64(len(b))
	}
	line, col = 1, 1
	for _, c := range b[:offset] {
		if c == '\n' {
			line, col = line+1, 1
			continue
		}
		col++
	}
	return line, col
}
