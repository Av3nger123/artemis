#!/bin/sh
# Installs the artemis release binary on macOS:
#
#   curl -fsSL https://raw.githubusercontent.com/Av3nger123/artemis/master/install.sh | sh
#
# It downloads the tarball for this Mac's processor from the latest GitHub
# release, checks it against the release's checksums.txt, and puts artemis in
# /usr/local/bin -- asking for sudo only when that directory is not writable.
#
#   ARTEMIS_VERSION=v0.1.0   a release other than the latest
#   ARTEMIS_INSTALL_DIR=...  somewhere other than /usr/local/bin
#
# To build from a clone instead, with Go installed: make build-full.
set -eu

repo="Av3nger123/artemis"
version="${ARTEMIS_VERSION:-latest}"
dir="${ARTEMIS_INSTALL_DIR:-/usr/local/bin}"

fail() {
	echo "artemis install: $*" >&2
	exit 1
}

[ "$(uname -s)" = "Darwin" ] || fail "only macOS has a release yet; build from source with: make build-full"

case "$(uname -m)" in
arm64) arch=arm64 ;;
x86_64) arch=amd64 ;;
*) fail "no release for a $(uname -m) Mac" ;;
esac

if [ "$version" = "latest" ]; then
	base="https://github.com/$repo/releases/latest/download"
else
	base="https://github.com/$repo/releases/download/$version"
fi
# A mirror, or a local directory served over http when testing this script.
base="${ARTEMIS_DOWNLOAD_URL:-$base}"

tarball="artemis_darwin_$arch.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $tarball ($version)..."
curl -fsSL "$base/$tarball" -o "$tmp/$tarball" || fail "could not download $base/$tarball"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || fail "could not download $base/checksums.txt"

(cd "$tmp" && grep " $tarball\$" checksums.txt | shasum -a 256 -c - >/dev/null) ||
	fail "$tarball does not match checksums.txt; not installing it"

tar -xzf "$tmp/$tarball" -C "$tmp"

# A directory that does not exist yet is made without sudo where its parent
# allows it -- ~/.local/bin on a fresh account -- and sudo is asked for only
# when the directory still cannot be written.
mkdir -p "$dir" 2>/dev/null || true
if [ -d "$dir" ] && [ -w "$dir" ]; then
	sudo=""
else
	echo "$dir needs sudo to write to."
	sudo="sudo"
fi
$sudo mkdir -p "$dir"
$sudo install -m 755 "$tmp/artemis" "$dir/artemis"

echo "Installed $("$dir/artemis" --version) to $dir/artemis"
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "$dir is not on your PATH; add it to run artemis by name." ;;
esac
