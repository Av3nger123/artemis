package migrate

import (
	"bytes"
	"os"

	"gopkg.in/yaml.v3"
)

// ParseYAMLFile reads a YAML scenario and returns it only if artemis
// understands every part of it.
//
// Decoding is strict -- KnownFields(true) -- so a typo'd key is an error naming
// the field and its line instead of a key that is quietly dropped, leaving a
// scenario that asserts nothing and "migrates" into one that checks nothing.
// The parsed config is then validated against knownTypes, which is StepTypes
// for every caller that is not a test: a `type:` this package cannot translate
// is refused here rather than at the point where Source has nothing to write.
//
// The file is then read a second time as a plain node tree, to stamp onto the
// config the line each part of it was written on (ART-12). The bytes are read
// once and decoded twice rather than decoded once into a node and converted,
// because KnownFields is a property of the decoder and does not survive being
// handed a node: strictness is worth more than a pass over the file.
//
// This is the whole of what artemis can still do with a YAML file. It was
// `artemis run`'s loader until ART-40 took the format off the run path; now
// `artemis migrate` is its only caller that matters, with `artemis generate`
// reaching it only to check its own output.
func ParseYAMLFile(filePath string, knownTypes []string) (Config, error) {
	var config Config

	raw, err := os.ReadFile(filePath) //nolint:gosec // the path is the one the user named
	if err != nil {
		return config, err
	}

	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}

	if err := config.Validate(knownTypes); err != nil {
		return config, err
	}

	// Past the point of no return: the scenario is good. A second decode of
	// bytes the first one accepted cannot fail, and if it somehow did, the
	// scenario still migrates -- with no line numbers.
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(raw)).Decode(&doc); err == nil {
		annotateLines(&config, &doc)
	}

	return config, nil
}
