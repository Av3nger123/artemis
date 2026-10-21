package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"artemis/pkg/shared/migrate"
	"artemis/pkg/shared/postman"

	"github.com/spf13/cobra"
)

// forceFlag is --force: replace the file -o names when it is already there.
const forceFlag = "force"

// flatFlag is --flat: write one scenario with a step per request, the output
// before collections. Without it, generate writes a collection and a scenario
// that uses it.
const flatFlag = "flat"

// collectionExt is what the collection file is called beside the -o file:
// orders.art gets orders.collection.art.
const collectionExt = ".collection" + artExt

var generateCmd = &cobra.Command{
	Use:   "generate -f <collection.json> -o <file.art> [--force] | -f <collection.json> --flat [-o <file.art>]",
	Short: "Convert a Postman collection to .art files, once",
	Long: `Read a Postman collection and write the same requests as Artemis DSL source.

	artemis generate -f orders.postman_collection.json -o orders.art          # two files
	artemis generate -f orders.postman_collection.json --flat                 # one file, to stdout
	artemis generate -f orders.postman_collection.json --flat -o orders.art   # one file

There are two modes:

  collection (the default)  Writes two files. orders.collection.art holds one
                            'collection' with one 'request' for each Postman
                            request. orders.art imports it and holds one 'use'
                            line for each request. This mode needs -o.
  --flat                    Writes one scenario with one 'step' for each
                            request. This is the output before collections.

In the collection mode, the scenario keeps the 'var' lines. A request gets one
parameter for each var it reads, and the 'use' line passes the var by name. Two
requests with the same name get the suffixes _2, _3 and so on. A Postman file
with no requests is an error, and no file is written.

The output goes through the same printer 'artemis fmt' and 'artemis migrate'
use, so a generated file is already canonically formatted. The collection is
never touched.

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
comment above the scenario.

A construct with no DSL spelling is an error naming the request: a formdata or
file body, an auth type needing a signature, a dynamic variable like
'{{$guid}}'. Nothing is written. Pre-request and test scripts are not read at
all -- the DSL has no spelling for arbitrary JavaScript.

With --flat and without -o, the source goes to stdout and no file is written.
With -o, a file that is already there is an error rather than an overwrite: in
the collection mode this is true for each of the two files. --force replaces
them.`,
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
		return generateFile(cmd, path, out, force, flat)
	},
}

// generateFile is the whole command.
//
// Every step returns on its first error. The version this replaces was a `Run`
// that logged each failure and carried on, so a collection that would not parse
// was followed by a conversion of the zero value -- an empty scenario written
// over whatever file was in the way -- and the process still exited 0.
func generateFile(cmd *cobra.Command, path, out string, force, flat bool) error {
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

	if !flat {
		return generateCollection(path, out, config, comments)
	}

	// The same translator `artemis migrate` uses, so the two commands cannot
	// disagree about quoting, interpolation or layout: what lands on disk is
	// print.Canonical's output either way.
	src, err := migrate.Source(config, comments)
	if err != nil {
		return fmt.Errorf("generate %s: %w", path, err)
	}

	if out == "" {
		_, err := fmt.Fprint(cmd.OutOrStdout(), src)
		return err
	}
	if err := ensureWritable(out); err != nil {
		return err
	}
	return writeFormatted(out, src)
}

// generateCollection writes the collection file beside out, then the scenario
// file at out. Both are built before either is written, so a refused
// collection writes nothing.
func generateCollection(path, out string, config migrate.Config, comments migrate.Comments) error {
	collOut := collectionPath(out)
	files, err := postman.CollectionFiles(config, comments, collOut)
	if errors.Is(err, postman.ErrNoRequests) {
		return fmt.Errorf("generate %s: %w, so no file is written", path, err)
	}
	if err != nil {
		return fmt.Errorf("generate %s: %w", path, err)
	}
	for _, f := range []struct{ path, src string }{{collOut, files.Collection}, {out, files.Scenario}} {
		if err := ensureWritable(f.path); err != nil {
			return err
		}
		if err := writeFormatted(f.path, f.src); err != nil {
			return err
		}
	}
	return nil
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
			return nil
		}
		return fmt.Errorf("artemis generate writes a collection file and a scenario file, so it needs -o <file.art>; "+
			"pass --%s to print one scenario to stdout", flatFlag)
	}
	if !isArtFile(out) {
		return fmt.Errorf("artemis generate writes %s files; -o %s is not one", artExt, out)
	}
	if force {
		return nil
	}
	targets := []string{out}
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
