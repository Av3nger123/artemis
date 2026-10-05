package cli

import (
	"fmt"
	"log/slog"
	"strings"

	"artemis/pkg/codegen"
	"artemis/pkg/steps/browserstep"

	"github.com/spf13/cobra"
)

func Init() {
	// A failed test run is reported by RunE as an error; cobra must not answer
	// it with usage text, and main prints the message itself.
	RootCmd.SilenceUsage = true
	RootCmd.SilenceErrors = true

	// Parse command: check a scenario file without running any of it.
	RootCmd.AddCommand(parseCmd)
	parseCmd.Flags().StringP("file", "f", "", "Path to the .art scenario file")
	parseCmd.Flags().Bool(jsonFlag, false, "Write the diagnostics to stdout as one JSON document, for an editor or a UI, instead of a report on stderr")
	if err := parseCmd.MarkFlagRequired("file"); err != nil {
		slog.Error("Error marking flag as required", "error", err)
	}

	// MCP command: the same front end over stdin and stdout, for an agent.
	// Its tools read; none of them writes a file.
	RootCmd.AddCommand(mcpCmd)
	mcpCmd.Flags().String(workspaceFlag, "", "The directory tool paths resolve against (default: the working directory)")

	// Grammar command: the language, for whatever is about to write some. No
	// file and no arguments -- everything it prints is in the binary.
	RootCmd.AddCommand(grammarCmd)
	grammarCmd.Flags().Bool(jsonFlag, false, "Emit the enumerable choice points -- methods, browser actions, operators, type names, block fields -- instead of the EBNF")

	// Ast command: the tree as JSON, and back. No required flag, because
	// --from-json reads stdin and -f is the alternative to it rather than a
	// companion.
	RootCmd.AddCommand(astCmd)
	astCmd.Flags().StringP("file", "f", "", "The .art file to read, or -- with --from-json -- the tree to read instead of stdin")
	astCmd.Flags().Bool(fromJSONFlag, false, "Read a tree on stdin and write .art source, instead of the other way round")

	// Fmt command: canonical .art formatting, to stdout or over the file.
	RootCmd.AddCommand(fmtCmd)
	fmtCmd.Flags().BoolP(writeFlag, "w", false, "Rewrite the file in place instead of printing to stdout")

	// Run command: the way to run scenarios -- a .art file or a folder of them.
	RootCmd.AddCommand(runCmd)
	runCmd.Flags().StringP("log", "l", "", "Write a JSON log of the run to this file (default: no log file)")
	runCmd.Flags().StringP("env", "e", ".env", "Path to the env file")
	// Repeatable, and its value is format[=path], so a second format is a value
	// rather than a second flag: --report json --report junit=junit.xml.
	runCmd.Flags().StringArray(reportFlag, nil,
		"Write a machine-readable report: "+strings.Join(reportFormats(), "|")+"[=path] (default: stdout, which moves the console report to stderr). Repeatable")
	runCmd.Flags().String(screenshotsFlag, browserstep.DefaultDir,
		"Write a screenshot of the page for each browser step that fails, into this folder. Empty turns them off; the folder is created only when one fails")

	// Build command: a .art file out as tests in another language. --lang is
	// required rather than defaulted, so an invocation that means python today
	// cannot come to mean something else when a second target lands.
	RootCmd.AddCommand(buildCmd)
	buildCmd.Flags().String(langFlag, "",
		"The target language: "+strings.Join(codegen.Names(), "|")+". Required")
	if err := buildCmd.MarkFlagRequired(langFlag); err != nil {
		slog.Error("Error marking flag as required", "error", err)
	}
	buildCmd.Flags().StringP(outFlag, "o", "", "Write the generated files into this directory instead of printing to stdout")

	// Deprecated: superseded by run, kept so the old invocation keeps working.
	RootCmd.AddCommand(testCmd)
	testCmd.Flags().StringP("file", "f", "", "Path to the .art scenario file")
	if err := testCmd.MarkFlagRequired("file"); err != nil {
		slog.Error("Error marking flag as required", "error", err)
	}
	testCmd.Flags().StringP("log", "l", "", "Write a JSON log of the run to this file (default: no log file)")
	testCmd.Flags().StringP("env", "e", ".env", "Path to the env file")
	// On the deprecated command too, so a CI job that has not moved to `run`
	// yet still gets the screenshots rather than discovering the flag does not
	// exist here.
	testCmd.Flags().String(screenshotsFlag, browserstep.DefaultDir,
		"Write a screenshot of the page for each browser step that fails, into this folder. Empty turns them off")

	// Migrate command: YAML in, .art out, once. The one remaining YAML reader
	// in the binary, now that ART-40 has taken the format off the run path.
	RootCmd.AddCommand(migrateCmd)
	migrateCmd.Flags().StringP("file", "f", "", "Path to the YAML scenario to convert")
	if err := migrateCmd.MarkFlagRequired("file"); err != nil {
		slog.Error("Error marking flag as required", "error", err)
	}
	migrateCmd.Flags().StringP(outFlag, "o", "", "Write the .art file here instead of printing it to stdout")

	// Generate command: a Postman collection in, .art out, once -- the other
	// adoption path, onto the same printer as migrate (ART-41).
	RootCmd.AddCommand(generateCmd)
	generateCmd.Flags().StringP("file", "f", "", "Path to the Postman collection JSON file")
	// On generateCmd, not testCmd: marking it on the wrong command left
	// `artemis generate` with no required flag at all, so it passed validation
	// and then failed opening "".
	if err := generateCmd.MarkFlagRequired("file"); err != nil {
		slog.Error("Error marking flag as required", "error", err)
	}
	generateCmd.Flags().StringP(outFlag, "o", "", "Write the .art file here instead of printing it to stdout")
	generateCmd.Flags().Bool(forceFlag, false, "Replace the file named by -o when it already exists")
}

var RootCmd = &cobra.Command{
	Use:   "artemis",
	Short: "Artemis is an API testing tool",
	Long: `Artemis is a comprehensive CLI tool for API testing.
    Scenarios are written in the Artemis DSL, in .art files: artemis parses,
    checks and runs them, and converts an old YAML scenario with migrate.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "Use 'artemis run <path>' to run a scenario file or a folder of them, 'artemis parse -f <file>' to check one without calling anything, 'artemis fmt [-w] <file.art>' to format it, and 'artemis ast -f <file.art>' for its syntax tree as JSON, and 'artemis migrate -f <file.yaml>' to convert an old YAML scenario to one, 'artemis build --lang=python <file.art>' to export it as pytest, and 'artemis grammar' for the language itself.")
	},
}
