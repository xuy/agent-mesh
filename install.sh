#!/bin/sh
# Install the mesh binary for this platform.
#
#   curl -fsSL https://raw.githubusercontent.com/xuy/agent-mesh/main/install.sh | sh
#
# Downloads the latest release for this OS and architecture, verifies it runs,
# and puts it on PATH. Set MESH_INSTALL_DIR to choose where.
#
# Add --join to also join a mesh and register it with the agents installed on
# this machine. Everything after --join is passed to `mesh join`:
#
#   ... | sh -s -- --join --name mac --mesh home
#   ... | sh -s -- --join --lan --code M5TQ6692
set -eu

REPO="xuy/agent-mesh"
DIR="${MESH_INSTALL_DIR:-$HOME/.local/bin}"

join=no
if [ $# -gt 0 ] && [ "$1" = "--join" ]; then
	join=yes
	shift
fi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) echo "mesh: no build for $arch -- build from source with: go install github.com/$REPO/cmd/mesh@latest" >&2; exit 1 ;;
esac
case "$os" in
	darwin | linux) ;;
	*) echo "mesh: use the Windows release from https://github.com/$REPO/releases" >&2; exit 1 ;;
esac

tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
[ -n "$tag" ] || { echo "mesh: could not find the latest release" >&2; exit 1; }

url="https://github.com/$REPO/releases/download/$tag/mesh-$os-$arch"
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

echo "downloading mesh $tag for $os/$arch"
curl -fsSL "$url" -o "$tmp" || { echo "mesh: $url is not available" >&2; exit 1; }
chmod +x "$tmp"

# Prove it runs before putting it on PATH, so a bad download fails here rather
# than the first time someone needs it.
"$tmp" version >/dev/null 2>&1 || { echo "mesh: the downloaded binary does not run" >&2; exit 1; }

mkdir -p "$DIR"
# Replace by rename, never in place: macOS invalidates the code signature of a
# binary modified in place and kills it on exec, silently.
mv -f "$tmp" "$DIR/mesh"
trap - EXIT

echo "installed $DIR/mesh"
case ":$PATH:" in
	*":$DIR:"*) ;;
	*) on_path=no ;;
esac

if [ "$join" = no ]; then
	[ "${on_path:-yes}" = yes ] || { echo; echo "$DIR is not on your PATH. Add it:"; echo "    export PATH=\"$DIR:\$PATH\""; }
	echo
	echo "Next:  mesh join --name \$(hostname -s)"
	exit 0
fi

# --join: come back with a node that is already running and already registered
# with the agents on this machine, so the caller has nothing left to run.
echo
"$DIR/mesh" join "$@"
echo
"$DIR/mesh" connect || true
echo
[ "${on_path:-yes}" = yes ] || { echo "$DIR is not on your PATH. Add it:"; echo "    export PATH=\"$DIR:\$PATH\""; echo; }
echo "Next:  mesh service install    # keep this node up across reboots"
