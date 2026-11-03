package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"artemis/pkg/shared/postman"

	"github.com/spf13/cobra"
)

// forceFlag is --force: replace the file -o names when it is already there.
const forceFlag = "force"

// flatFlag is --flat: write a flow with each request in place as a step, and
// the scenario file that runs it. Without it, generate also writes a
// collection, and the flow calls its items.
const flatFlag = "flat"

// collectionExt and flowExt are what the collection and the flow are called
// beside the -o file, which is the scenario file: orders.art gets
// orders.collection.art and orders.flow.art.
const (
	collectionExt = ".collection" + artExt
	flowExt       = ".flow" + artExt
)

var generateCmd = &cobra.Command{
	Use:   "generate -f <collection.json> -o <file.art> [--flat] [--force]",
	Short: "Convert a Postman collection to .art files, once",
	Long: `Read a Postman collection and write the same requests as Artemis DSL source.

	artemis generate -f orders.postman_collection.json -o orders.art          # three files
	artemis generate -f orders.postman_collection.json --flat -o orders.art   # two files

There are two modes:

  collection (the default)  Writes three files. orders.collection.art holds one
                            'collection', titled as the Postman collection is,
                            with one 'request' for each Postman request.
                            orders.flow.art imports it and holds the flow: one
                            'var' for each Postman variable, then one
                            'step "<name>" = orders.<request> {}' for each
                            request. orders.art is the scenario file: it names
                            the flow and holds one scenario, titled as the
                            collection is, that sets nothing.
  --flat                    Writes two files: the flow, with one 'step' written
                            in place for each request, and the scenario file.

Both modes need -o, which names the scenario file; the others are written
beside it. In the collection mode, a request declares each variable it reads
as a 'var' with no value, which the flow's var of that name supplies. Two
requests with the same name get the suffixes _2, _3 and so on. A Postman file
with no requests is an error in the collection mode, and no file is written.

The flow is named after the collection: "Orders API" is orders_api. The output
is written in the form 'artemis fmt' gives, so a generated file is already
canonically formatted. The collection is never touched.

Folders nest to any depth, and every request in the tree becomes one step named
by its folder path: "Orders / Admin / list orders". Request headers, 'url.query'
parameters and the body's mode are read as the collection wrote them -- a raw
JSON body becomes an object literal, a urlencoded body a string -- and a
disabled header or parameter is left out, because the author switched it off.

Auth at the collection and at the request both translate to a header: bearer,
basic with literal credentials, and apikey in a header or the query. A request
with a saved example response asserts that example's status; one without
asserts 'expect status < 400', which is the strongest thing the collection
actually says.

A Postman variable whose key is not a DSL name is renamed -- 'base-url' becomes
base_url -- and every placeholder is rewritten to match. A '{{name}}' no
variable defines becomes 'var name = env("NAME")'. Both are noted in the
comment above the flow.

A construct with no DSL spelling is an error naming the request: a formdata or
file body, an auth type needing a signature, a dynamic variable like
'{{$guid}}'. Nothing is written. Pre-request and test scripts are not read at
all -- the DSL has no spelling for arbitrary JavaScript.

A file that is already there is an error rather than an overwrite, for each of
the files a mode writes. --force replaces them.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := cmd.Flags().GetString("file")
		if err != nil {
			return fmt.Errorf("reading --file flag: %w", err)
		}
		out, err := cmd.Flags().GetString(outFlag)
		if err != nil {
			return fmt.Errorf("reading --%s flag: %w", outFlag, err)
		}
		force, err := cmd.Flags().GetBool(forceFlag)
		if err != nil {
			return fmt.Errorf("reading --%s flag: %w", forceFlag, err)
		}
		flat, err := cmd.Flags().GetBool(flatFlag)
		if err != nil {
			return fmt.Errorf("reading --%s flag: %w", flatFlag, err)
		}
		return generateFile(path, out, force, flat)
	},
}

// generateFile is the whole command.
//
// Every step returns on its first error. The version this replaces was a `Run`
// that logged each failure and carried on, so a collection that would not parse
// was followed by a conversion of the zero value -- an empty scenario written
// over whatever file was in the way -- and the process still exited 0.
func generateFile(path, out string, force, flat bool) error {
	if err := checkGenerateArgs(path, out, force, flat); err != nil {
		return err
	}

	collection, err := postman.Parse(path)
	if err != nil {
		return err
	}

	config, comments, err := postman.Translate(collection, path)
	if err != nil {
		return fmt.Errorf("generate %s: %w", path, err)
	}

	var files postman.Files
	if flat {
		files, err = postman.FlatFiles(config, comments, flowPath(out))
	} else {
		files, err = postman.CollectionFiles(config, comments, collectionPath(out), flowPath(out))
	}
	if errors.Is(err, postman.ErrNoRequests) {
		return fmt.Errorf("generate %s: %w, so no file is written", path, err)
	}
	if err != nil {
		return fmt.Errorf("generate %s: %w", path, err)
	}
	return writeGenerated(out, files)
}

// writeGenerated writes the collection (when there is one) and the flow beside
// out, then the scenario file at out. Every file is built before any is
// written, so a refused generation writes nothing.
func writeGenerated(out string, files postman.Files) error {
	targets := []struct{ path, src string }{{flowPath(out), files.Flow}, {out, files.Scenario}}
	if files.Collection != "" {
		targets = append([]struct{ path, src string }{{collectionPath(out), files.Collection}}, targets...)
	}
	for _, f := range targets {
		if err := ensureWritable(f.path); err != nil {
			return err
		}
		if err := writeFormatted(f.path, f.src); err != nil {
			return err
		}
	}
	return nil
}

// flowPath is the flow file written beside out.
func flowPath(out string) string {
	return strings.TrimSuffix(out, filepath.Ext(out)) + flowExt
}

// collectionPath is the collection file written beside out.
func collectionPath(out string) string {
	return strings.TrimSuffix(out, filepath.Ext(out)) + collectionExt
}

// checkGenerateArgs refuses what cannot work before anything is read.
//
// The clobber guard is the reason -o exists at all. The old importer derived
// `utils.Slugify(name) + ".yaml"` and wrote it into the working directory,
// ignoring the path it was handed, so running it twice in a repo replaced a
// file somebody had since edited by hand with no warning and no way to say
// otherwise.
func checkGenerateArgs(path, out string, force, flat bool) error {
	if isArtFile(path) {
		return fmt.Errorf("%s is already %s; artemis generate converts a Postman collection, and artemis fmt formats a %s file",
			path, artExt, artExt)
	}
	if isYAMLFile(path) {
		return fmt.Errorf("%s is YAML, not a Postman collection; artemis migrate converts a YAML scenario", path)
	}
	if out == "" {
		if flat {
			return errors.New("artemis generate --flat writes a flow file and a scenario file, so it needs -o <file.art>, the scenario file")
		}
		return fmt.Errorf("artemis generate writes a collection file, a flow file and a scenario file, so it needs -o <file.art>, "+
			"the scenario file; --%s writes no collection, and needs -o too", flatFlag)
	}
	if !isArtFile(out) {
		return fmt.Errorf("artemis generate writes %s files; -o %s is not one", artExt, out)
	}
	if force {
		return nil
	}
	targets := []string{out, flowPath(out)}
	if !flat {
		targets = append(targets, collectionPath(out))
	}
	for _, t := range targets {
		if _, err := os.Stat(t); err == nil {
			return fmt.Errorf("%s already exists; pass --%s to replace it, or name another file with -o", t, forceFlag)
		}
	}
	return nil
}
