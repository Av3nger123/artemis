# Security

## Reporting a vulnerability

Report it privately, through GitHub: the repository's **Security** tab, then
**Report a vulnerability**
(https://github.com/Av3nger123/artemis/security/advisories/new). Please do not
open a public issue for it.

You will hear back within a week. A confirmed vulnerability is fixed in a new
release, and the advisory is published with it, crediting you unless you would
rather it did not.

Only the latest release is supported: a fix ships as a new release, not as a
patch to an old one.

## What a scenario can do

An `.art` file is a program, and running one is like running a script: it acts
with your permissions.

- A `terminal` step runs any command it names, with no sandbox.
- An `api` step sends requests to any URL it names, with whatever credentials
  the scenario reads from your environment.
- A `db` step runs SQL or commands against any database it can reach.
- A `browser` step drives a real Chromium.

Read a scenario from someone else before you run it, as you would a shell
script.

`artemis mcp` gives an agent the same reach. Its file tools are held to the
workspace, and a path or an import that leaves it is refused, but a scenario it
runs can still run any command and reach any URL. Register it only with a host
that asks before calling `artemis_run`.

## What artemis keeps out of its output

A `secret` var or capture is printed as `***` in the console, the reports and
the trace, and a trace replaces a database URL's password with `***` whether or
not the URL is secret. Any other value is printed as it is, so mark anything
sensitive `secret`.

## Verifying a release

The installer checks each download against the release's `checksums.txt`. To
check that a tarball was built by this repository's release workflow, from the
tagged commit, verify its provenance with the GitHub CLI:

```sh
gh attestation verify artemis_darwin_arm64.tar.gz --repo Av3nger123/artemis
```
