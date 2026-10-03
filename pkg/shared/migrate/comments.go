package migrate

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"
)

// Comments are the YAML comments migration carries into the .art file.
//
// Two positions carry: the block above the first key of the document, which is
// what every scenario in pkg/cli/testdata uses to say what the file is for, and
// the block above a step. Those are the two a .art file has an unambiguous home
// for -- above `scenario` and above `step` -- and canonical layout places both.
//
// Everything else is dropped and counted. A comment on one body check has no
// home: the check may become two expect lines or none, and attaching the
// author's sentence to a line migration invented is worse than saying how many
// were lost. Dropped is what `artemis migrate` reports on stderr.
type Comments struct {
	// File is the block above the document's first key, as written, including
	// each line's leading "#".
	File string

	// Steps is the block above each step, indexed by the step's position in
	// `steps:`. A step with no comment holds the empty string.
	Steps []string

	// Dropped is how many comments were found somewhere neither of the above.
	Dropped int
}

// ReadComments reads raw as a plain node tree and picks the two blocks out.
//
// It is a second decode of bytes ParseYAMLFile has already accepted, which is
// the shape that function itself uses for line numbers: KnownFields is a
// property of the decoder and does not survive being handed a node, so
// strictness and this pass cannot be the same read. A failure here is not an
// error -- the scenario still migrates, without its comments -- so the only
// return is the struct.
func ReadComments(raw []byte) Comments {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(raw)).Decode(&doc); err != nil {
		return Comments{}
	}
	root := mapping(&doc)
	if root == nil {
		return Comments{}
	}

	var c Comments
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i], root.Content[i+1]
		if i == 0 {
			// yaml.v3 hangs a document's leading block on its first key.
			c.File = key.HeadComment
		} else {
			c.Dropped += count(key.HeadComment)
		}
		c.Dropped += count(key.FootComment) + count(key.LineComment)
		if key.Value == "steps" {
			c.Steps = stepComments(val, &c)
			continue
		}
		c.Dropped += countTree(val)
	}
	return c
}

// stepComments is the block above each step, with everything deeper counted as
// dropped.
func stepComments(seq *yaml.Node, c *Comments) []string {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	c.Dropped += count(seq.HeadComment) + count(seq.LineComment) + count(seq.FootComment)

	out := make([]string, 0, len(seq.Content))
	for _, step := range seq.Content {
		out = append(out, step.HeadComment)
		c.Dropped += count(step.LineComment) + count(step.FootComment)
		for _, child := range step.Content {
			c.Dropped += countTree(child)
		}
	}
	return out
}

// mapping is the document's top-level mapping, or nil when the file is not one.
func mapping(doc *yaml.Node) *yaml.Node {
	n := doc
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// countTree is every comment anywhere in n.
func countTree(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	total := count(n.HeadComment) + count(n.LineComment) + count(n.FootComment)
	for _, child := range n.Content {
		total += countTree(child)
	}
	return total
}

// count is how many comment lines a yaml.v3 comment field holds. The field is
// the block joined by newlines, so an empty one is no comment at all rather
// than one empty line.
func count(block string) int {
	if block == "" {
		return 0
	}
	return len(strings.Split(block, "\n"))
}

// stepComment is the block above step i, or the empty string when there is
// none. It is a method so a Comments with a short Steps -- which a scenario
// built in Go has -- never panics.
func (c Comments) stepComment(i int) string {
	if i < 0 || i >= len(c.Steps) {
		return ""
	}
	return c.Steps[i]
}
