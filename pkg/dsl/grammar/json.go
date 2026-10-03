package grammar

import (
	"bytes"
	"encoding/json"
	"io"
)

// JSON is the choice-point document as bytes: what `artemis grammar --json`
// writes.
//
// Indented with two spaces and newline-terminated, the same shape `artemis
// ast` and `artemis parse --json` write, because a document of this kind is
// read by people at least as often as by programs and its golden has to be
// reviewable. The output is fully determined by the tables -- Go marshals map
// keys in sorted order, and every values list keeps its table's order -- so
// the same binary always writes the same bytes.
func JSON() ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	// An unescaped "<" in a hint or a description is noise in a file a person
	// reads and is nothing a JSON parser needs escaped.
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(Choices()); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// WriteJSON is JSON onto w.
func WriteJSON(w io.Writer) error {
	b, err := JSON()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
