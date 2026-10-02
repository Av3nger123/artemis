package shared

import (
	"artemis/pkg/shared/models"

	"gopkg.in/yaml.v3"
)

// annotateLines stamps onto an already-decoded config the line of the scenario
// file each part of it was written on, so a failed assertion can say which line
// to go and edit (ART-12).
//
// It is a second pass over the same document rather than an UnmarshalYAML on
// models.Step, because a hand-written unmarshaller would have to reimplement the
// strict known-fields check -- the decoder's KnownFields setting does not reach a
// node decoded by hand, which is the hole models.Retry and models.Capture already
// paper over -- and Step has eight fields across two step types. The walk cannot
// weaken strict decoding because it does no decoding.
//
// Nothing here returns an error and nothing here panics. Every lookup gives up
// quietly on a shape it does not recognise, which leaves the line at zero, and a
// zero line is already the value for "artemis does not know": a step built in Go,
// a scenario converted from a Postman collection. That is the whole reason this
// is a walk and not a parser -- the worst it can do is tell a reader less.
func annotateLines(config *models.Config, doc *yaml.Node) {
	steps := seqValue(mappingValue(document(doc), "steps"))
	if steps == nil {
		return
	}
	for i := range config.Steps {
		node := child(steps, i)
		if node == nil {
			continue
		}
		annotateStep(&config.Steps[i], node)
	}
}

// annotateStep stamps one step and everything addressable inside it.
//
// The step's own line is the mapping's, which yaml.v3 reports as the line of its
// first key -- the `- name: ...` a reader's eye lands on, not the blank line
// before it.
func annotateStep(step *models.Step, node *yaml.Node) {
	step.Line = node.Line

	if response := mappingValue(node, "response"); response != nil {
		step.Response.StatusCodeLine = keyLine(response, "status_code")
		annotateBodyChecks(step.Response.Body, seqValue(mappingValue(response, "body")))
	}
	if expect := mappingValue(node, "expect"); expect != nil {
		step.Expect.ExitCodeLine = keyLine(expect, "exit_code")
		annotateTextChecks(step.Expect.Stdout, seqValue(mappingValue(expect, "stdout")))
		annotateTextChecks(step.Expect.Stderr, seqValue(mappingValue(expect, "stderr")))
	}
	annotateCaptures(step.Capture, mapValue(mappingValue(node, "capture")))
}

// annotateBodyChecks walks a `body:` sequence index for index against the checks
// decoded from it. A check's line is its own mapping's, which is the `path:` it
// opens with in every spelling anyone writes.
func annotateBodyChecks(checks []models.BodyCheck, seq *yaml.Node) {
	for i := range checks {
		if node := child(seq, i); node != nil {
			checks[i].Line = node.Line
		}
	}
}

// annotateTextChecks is annotateBodyChecks for an exec step's stdout and stderr.
func annotateTextChecks(checks []models.TextCheck, seq *yaml.Node) {
	for i := range checks {
		if node := child(seq, i); node != nil {
			checks[i].Line = node.Line
		}
	}
}

// annotateCaptures stamps each capture with the line of its key, not of its
// value: the key is the name a scenario goes and fixes, and for the block form
//
//	capture:
//	  token:
//	    json: "$.token"
//
// the value's line is the `json:` below it.
//
// A map of structs is not addressable, so each value is read out, stamped and
// put back. models.Capture's compiled pattern rides along in the copy.
func annotateCaptures(captures map[string]models.Capture, node *yaml.Node) {
	if len(captures) == 0 || node == nil {
		return
	}
	// Content alternates key, value, key, value.
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		c, ok := captures[key.Value]
		if !ok {
			continue
		}
		c.Line = key.Line
		captures[key.Value] = c
	}
}

// document unwraps a decoded document node to the value inside it, since
// yaml.Decode into a yaml.Node gives back the document wrapper.
func document(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		return node.Content[0]
	}
	return node
}

// mappingValue is the value node of key in a mapping, or nil when node is not a
// mapping or has no such key.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if i := keyIndex(node, key); i >= 0 {
		return node.Content[i+1]
	}
	return nil
}

// keyLine is the line key sits on in a mapping, or zero when it is not there.
func keyLine(node *yaml.Node, key string) int {
	if i := keyIndex(node, key); i >= 0 {
		return node.Content[i].Line
	}
	return 0
}

// keyIndex is the index of key's own node in a mapping's Content, or -1.
func keyIndex(node *yaml.Node, key string) int {
	if node == nil || node.Kind != yaml.MappingNode {
		return -1
	}
	// Content alternates key, value, key, value.
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// seqValue is node when it is a sequence, and nil otherwise -- including for a
// `body:` written as a mapping, which the decoder would already have rejected.
func seqValue(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.SequenceNode {
		return node
	}
	return nil
}

// mapValue is node when it is a mapping, and nil otherwise.
func mapValue(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.MappingNode {
		return node
	}
	return nil
}

// child is the i'th element of a sequence, or nil when the sequence is shorter
// than the decoded slice -- which an anchor or a merge key could make true.
func child(seq *yaml.Node, i int) *yaml.Node {
	if seq == nil || i < 0 || i >= len(seq.Content) {
		return nil
	}
	return seq.Content[i]
}
