#!/usr/bin/env bash
# drive.sh <feature-id> [--light] [--replay] <actions.json | inline JSON>
# Runs one tools/drive.mjs session against the built page. Shots in the
# actions are written relative to the run directory (its light/ subdirectory
# for --light, so the two themes never overwrite each other), which this prints.
set -euo pipefail
source "$(dirname "$0")/env.sh"
feature="${1:?feature id}"; shift
theme=""; [[ "${1:-}" == "--light" ]] && { theme="--light"; shift; }
[[ "${1:-}" == "--replay" ]] && { PAGE=kubeharness-replay; shift; }
actions="${1:?actions file or inline JSON}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
out="$ARTIFACTS/$RUN_ID"; mkdir -p "$out"
if [[ -f "$actions" ]]; then cp "$actions" "$out/$feature${theme:+-light}.actions.json"; else printf '%s\n' "$actions" >"$out/$feature${theme:+-light}.actions.json"; fi
url="$(page_url $theme)"
{
  echo "feature=$feature theme=${theme:-dark} url=$url"
  echo "commit=$(git -C "$ROOT" rev-parse --short HEAD) dirty=$(git -C "$ROOT" status --porcelain -- docs/figure | wc -l | tr -d ' ')"
  echo "page_sha=$(shasum -a 256 "${url#file://}" | cut -c1-16) date=$(date -u +%FT%TZ)"
} >"$out/$feature${theme:+-light}.meta.txt"
shots="$out${theme:+/light}"; mkdir -p "$shots"
cd "$shots"
set +e
node "$FIG/tools/drive.mjs" "$url" --actions "$out/$feature${theme:+-light}.actions.json" --height 1300 2>&1 | tee "$out/$feature${theme:+-light}.log"
code=${PIPESTATUS[0]}
set -e
echo "artifacts: $out"
exit "$code"
