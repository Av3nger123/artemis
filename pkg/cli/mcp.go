package cli

import (
	"fmt"
	"os"

	mcpserver "artemis/pkg/mcp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// workspaceFlag is --workspace: the directory every relative path a tool is
// given resolves against.
const workspaceFlag = "workspace"

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Serve the Model Context Protocol on stdin and stdout",
	Long: `Serve MCP on stdin and stdout, so an agent can author .art files.

The server runs until the client disconnects. It is the same binary and the
same front end as every other command, so a diagnostic an agent reads here is
the diagnostic ` + "`artemis parse`" + ` prints.

Register it with a host that starts servers itself, for example:

	artemis mcp --workspace /path/to/repo

Every path a tool is given is relative to the workspace, which defaults to the
directory the server was started in. A path that leaves the workspace is
refused, including one that leaves it through a symlink.

The tools this version serves read; none of them writes a file.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := cmd.Flags().GetString(workspaceFlag)
		if err != nil {
			return fmt.Errorf("reading --%s: %w", workspaceFlag, err)
		}
		if dir == "" {
			dir, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("finding the working directory: %w", err)
			}
		}
		// Nothing is written to stdout here: stdout is the protocol.
		server := mcpserver.New(mcpserver.Options{Workspace: dir})
		return server.Run(cmd.Context(), &mcp.StdioTransport{})
	},
}
