package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list <path>",
	Short: "List the scenarios a run of a file or a folder would run, with their tags",
	Long: `List the scenarios artemis finds at <path>, and run nothing.

Each scenario is printed under its file with its tags -- its own and its
suites' -- and, for a data-driven scenario, the row it was made from. --tags and
--grep select as they do for run, so a selection can be tried before a run:

	artemis list suite/ --tags "smoke and not slow"

With --json the list is one document on stdout instead:

	{"scenarios": [{"file": "suite/a.art", "name": "checkout", "line": 3,
	                "tags": ["smoke", "api"], "row": null}]}

A file that does not compile is reported on stderr with its diagnostics, and
makes the command exit non-zero after listing the rest.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return listScenarios(cmd, args[0])
	},
}

type listedScenario struct {
	File string   `json:"file"`
	Name string   `json:"name"`
	Line int      `json:"line"`
	Tags []string `json:"tags"`
	Row  *jsonRow `json:"row"`
}

type jsonRow struct {
	Source string `json:"source"`
	Index  int    `json:"index"`
}

func listScenarios(cmd *cobra.Command, path string) error {
	tagsValue, _ := cmd.Flags().GetString(tagsFlag)
	grepValue, _ := cmd.Flags().GetString(grepFlag)
	sel, err := newSelector(tagsValue, grepValue)
	if err != nil {
		return err
	}
	files, err := discover(path)
	if err != nil {
		return err
	}

	stderr := cmd.ErrOrStderr()
	rt := &runtimeEnv{sel: sel}
	var broken []string
	var loaded []compiled
	for _, file := range files {
		c, err := rt.load(file, stderr)
		if err != nil {
			broken = append(broken, file)
			continue
		}
		c.file = file
		loaded = append(loaded, c)
	}
	rt.planSuites(filesOf(loaded), io.Discard)

	known := map[string]bool{}
	listed := []listedScenario{}
	for _, c := range loaded {
		for _, sc := range c.scenarios {
			tags := rt.tagsOf(c.file, sc)
			for _, t := range tags {
				known[t] = true
			}
			if !sel.selects(sc.Name, tags) {
				continue
			}
			item := listedScenario{File: c.file, Name: sc.Name, Line: sc.Line, Tags: tags}
			if item.Tags == nil {
				item.Tags = []string{}
			}
			if sc.Row != nil {
				item.Row = &jsonRow{Source: sc.Row.Source, Index: sc.Row.Index}
			}
			listed = append(listed, item)
		}
	}
	if sel != nil {
		sel.warnUnknownTags(stderr, known)
	}

	out := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"scenarios": listed}); err != nil {
			return err
		}
	} else {
		printListed(out, listed)
	}
	if len(broken) > 0 {
		return fmt.Errorf("%d file(s) did not compile: %s", len(broken), strings.Join(broken, ", "))
	}
	if sel != nil && len(listed) == 0 {
		return errors.New("no scenario is " + strings.TrimPrefix(sel.reason(), "not "))
	}
	return nil
}

// printListed writes each file once, then its scenarios under it with their
// tags, aligned so a long list scans down one column.
func printListed(w io.Writer, listed []listedScenario) {
	width := 0
	for _, s := range listed {
		width = max(width, len(s.Name))
	}
	file := ""
	for _, s := range listed {
		if s.File != file {
			file = s.File
			fmt.Fprintln(w, file)
		}
		line := fmt.Sprintf("  %-*s", width, s.Name)
		if len(s.Tags) > 0 {
			line += "  [" + strings.Join(s.Tags, ", ") + "]"
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}

func filesOf(cs []compiled) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.file)
	}
	return out
}
