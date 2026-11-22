#!/usr/bin/env bash
# Removes scratch left by interrupted drives and recordings: frame
# directories under artifacts/, and headless Chrome processes and profiles
# that tools/drive.mjs started (user-data-dir iso-drive-*). Keeps every
# video, log, PNG and meta file. Do not run while another drive is live.
set -uo pipefail
source "$(dirname "$0")/env.sh"
find "$ARTIFACTS" -maxdepth 2 -name '.recording-*' -prune -print -exec rm -rf {} + 2>/dev/null
tmp="${TMPDIR:-/tmp}"; tmp="${tmp%/}"
pids="$(pgrep -f -- "--user-data-dir=$tmp/iso-drive-" || true)"
[[ -n "$pids" ]] && { echo "killing drive Chrome: $pids"; kill $pids 2>/dev/null; sleep 1; kill -9 $pids 2>/dev/null; }
for d in "$tmp"/iso-drive-*; do [[ -d "$d" ]] && { echo "removing $d"; rm -rf "$d"; }; done
echo "evidence kept in $ARTIFACTS"
