# The MCP server

`artemis mcp` serves the Model Context Protocol on stdin and stdout, so an agent
can author scenarios against the real grammar and the real checker instead of
from memory.

```sh
artemis mcp --workspace /path/to/repo
```

Six tools:

| Tool | What it does | Reads | Writes | Executes |
| --- | --- | --- | --- | --- |
| `artemis_grammar` | Every finite option set in the language | | | |
| `artemis_validate` | Every diagnostic in a piece of source text, with codes and suggestions | | | |
| `artemis_format` | The same source in canonical layout: the bytes `artemis fmt` writes | | | |
| `artemis_list` | The `.art` files in the workspace, in path order, and under `collections` the `collection.item` names each file declares | disk | | |
| `artemis_write` | Checks source, makes it canonical, writes it | disk | disk | |
| `artemis_run` | Runs the scenarios and returns the full JSON report | disk | | **yes** |

The loop is `list` to see what exists, `validate` until `ok` is true, `write`,
then `run`.

`artemis_write` checks before it writes, so it cannot leave a scenario on disk
that the runner would then refuse, and the file it writes is always `fmt`-clean
-- which is what makes the next edit to it a one-line diff instead of a
whole-file reformat. It writes the canonical form rather than the bytes it was
handed, for the same reason.

**`artemis_run` executes the scenario.** A `terminal` step runs a shell command,
a `browser` step drives a real browser, and an `api` step sends real requests to
whatever URL it names. It is annotated `readOnlyHint: false` and
`openWorldHint: true` so a host knows to ask before running it; `artemis_write`
is annotated as changing the workspace but not reaching outside it. Registering
this server grants command execution scoped to the workspace. The four
read-only tools reach nothing and change nothing.

A failing run is a successful call whose report says `passed: false`. The CLI's
non-zero exit has no equivalent here, and a red test arriving as a broken tool
would have an agent trying to repair the runner instead of the test.

Each tool returns its document on two channels. The text block is the document
exactly as artemis wrote it; structured content is the same thing decoded, with
`ok` added where a caller needs one boolean to loop on. The split exists because
the protocol decodes structured content on the way, so key order does not
survive it -- and the point of these tools is that the bytes are the CLI's
bytes. `artemis_validate` returns what `artemis parse --json` prints, byte for
byte, and a test asserts it over every fixture in `pkg/dsl/testdata/invalid`.
`artemis_run` returns what `artemis run --report json` prints, because it is
that command that printed it. A diagnostic an agent reads is the diagnostic a
person reads.

Every path a tool is given is relative to `--workspace`, which defaults to the
directory the server was started in. A path that leaves the workspace is
refused, including one that leaves it through a symlink, and `artemis_write`
will not create a directory to write into. An `import` is held to the same
rule: `artemis_validate`, `artemis_format`, `artemis_write` and `artemis_run` resolve imports
inside the workspace, and an import that leaves it is an error, not a read.
