#!/usr/bin/env bash
# Packs the Claude Desktop bundle from binaries goreleaser has already built.
#
# Called from the universal_binaries post hook in .goreleaser.yaml — the last
# point in the pipeline where every binary exists and archives, checksums and
# publish have not yet run, so the .mcpb it writes is picked up by
# checksum.extra_files and release.extra_files like any other asset.
#
#   scripts/mcpb-pack.sh <version> <darwin-universal-binary>
#
# One bundle serves every platform Claude Desktop runs on: server/pgmcp is the
# darwin universal binary (arm64 + amd64 in one Mach-O), server/pgmcp.exe is
# windows/amd64 (Windows on ARM runs it under x64 emulation). The MCPB manifest
# selects between them by OS only; it has no notion of architecture.
set -euo pipefail
cd "$(dirname "$0")/.."

# @anthropic-ai/mcpb validates the manifest and writes the archive every
# Desktop user installs, so it is pinned the way release.yml pins
# mcp-publisher rather than pulled as "latest". There is no package.json in
# this repo, so Dependabot does not see this pin: bump it by hand
# (`npm view @anthropic-ai/mcpb version`), then re-run the snapshot release
# check in AGENTS.md.
MCPB_VERSION="${MCPB_VERSION:-2.1.2}"

usage="usage: $0 <version> <darwin-universal-binary>"
version="${1:?$usage}"
darwin="${2:?$usage}"

test -f "$darwin" || { echo "mcpb-pack: darwin binary not found: $darwin" >&2; exit 1; }

# goreleaser lays builds out as dist/<build-id>_<goos>_<goarch>[_<goamd64>]/.
windows=(dist/pgmcp_windows_amd64*/pgmcp.exe)
if [ "${#windows[@]}" -ne 1 ] || [ ! -f "${windows[0]}" ]; then
  echo "mcpb-pack: expected exactly one windows/amd64 binary under dist/, found: ${windows[*]}" >&2
  exit 1
fi

stage="dist/mcpb"
out="dist/pgmcp_${version}.mcpb"

rm -rf "$stage"
mkdir -p "$stage/server"
jq --arg v "$version" '.version = $v' mcpb/manifest.json > "$stage/manifest.json"
install -m 0755 "$darwin" "$stage/server/pgmcp"
install -m 0755 "${windows[0]}" "$stage/server/pgmcp.exe"

mcpb() { npx --yes "@anthropic-ai/mcpb@${MCPB_VERSION}" "$@"; }

mcpb validate "$stage/manifest.json"
mcpb pack "$stage" "$out"
mcpb info "$out"
