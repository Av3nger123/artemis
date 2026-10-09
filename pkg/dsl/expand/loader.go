package expand

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// Loader reads the files an import names.
type Loader interface {
	// Load reads path, written in the file named from. name is the canonical
	// name used in spans, diagnostics and cycle detection.
	Load(from, path string) (name, src string, err error)
}

// MapLoader serves files from memory, keyed by slash-separated name. Tests
// use it, and so do the doc tests that compile SPEC.md's examples.
type MapLoader map[string]string

func (m MapLoader) Load(from, p string) (string, string, error) {
	name := path.Clean(path.Join(path.Dir(from), p))
	src, ok := m[name]
	if !ok {
		return "", "", fmt.Errorf("no file %s", name)
	}
	return name, src, nil
}

type dirLoader struct{}

// DirLoader reads from disk, relative to the importing file's directory.
func DirLoader() Loader { return dirLoader{} }

func (dirLoader) Load(from, p string) (string, string, error) {
	name := filepath.Clean(filepath.Join(filepath.Dir(from), p))
	b, err := os.ReadFile(name) //nolint:gosec // the path is the one the file names
	if err != nil {
		return "", "", err
	}
	return name, string(b), nil
}
