package cli

import (
	"fmt"
	"os"

	"artemis/pkg/shared/migrate"
	"artemis/pkg/shared/postman"

	"github.com/spf13/cobra"
)

// forceFlag is --force: replace the file -o names when it is already there.
const forceFlag = "force"

var generateCmd = &cobra.Command{
	Use:   "generate -f <collection.json> [-o <file.art>] [--force]",
	Short: "Convert a Postman collection to a .art file, once",
	Long: `Read a Postman collection and write the same requests as Artemis DSL source.

	artemis generate -f orders.postman_collection.json                 # to stdout
	artemis generate -f orders.postman_collection.json -o orders.art

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

Without -o the source goes to stdout and no file is written. With -o, a file
that is already there is an error rather than an overwrite; --force replaces it.`,
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
		return generateFile(cmd, path, out, force)
	},
}

// generateFile is the whole command.
//
// Every step returns on its first error. The version this replaces was a `Run`
// that logged each failure and carried on, so a collection that would not parse
// was followed by a conversion of the zero value -- an empty scenario written
// over whatever file was in the way -- and the process still exited 0.
func generateFile(cmd *cobra.Command, path, out string, force bool) error {
	if err := checkGenerateArgs(path, out, force); err != nil {
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

// checkGenerateArgs refuses what cannot work before anything is read.
//
// The clobber guard is the reason -o exists at all. The old importer derived
// `utils.Slugify(name) + ".yaml"` and wrote it into the working directory,
// ignoring the path it was handed, so running it twice in a repo replaced a
// file somebody had since edited by hand with no warning and no way to say
// otherwise.
func checkGenerateArgs(path, out string, force bool) error {
	if isArtFile(path) {
		return fmt.Errorf("%s is already %s; artemis generate converts a Postman collection, and artemis fmt formats a %s file",
			path, artExt, artExt)
	}
	if isYAMLFile(path) {
		return fmt.Errorf("%s is YAML, not a Postman collection; artemis migrate converts a YAML scenario", path)
	}
	if out == "" {
		return nil
	}
	if !isArtFile(out) {
		return fmt.Errorf("artemis generate writes %s files; -o %s is not one", artExt, out)
	}
	if force {
		return nil
	}
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists; pass --%s to replace it, or name another file with -o", out, forceFlag)
	}
	return nil
}
