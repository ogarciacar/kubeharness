#!/usr/bin/env bash
# Health check: is the figure worth driving? Rebuilds the generated pages
# (that is the figure's launch), then checks them.
set -uo pipefail
source "$(dirname "$0")/env.sh"
fail=0
ok() { echo "$1 ready"; }
bad() { echo "$1 FAIL: $2"; fail=1; }

v="$(node -p 'process.versions.node' 2>/dev/null || echo 0)"
node -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>20||(a===20&&b>=10)?0:1)' 2>/dev/null && ok "node $v" || bad "node" "need Node.js 20.10+, have $v"

out="$(cd "$FIG" && node build.mjs --audit 2>&1)"
[[ "$out" == *"audit passed"* ]] && ok "build + audit" || bad "build + audit" "$(echo "$out" | grep -v ': 0 ' | tail -8)"
out="$(cd "$FIG" && node build.mjs --light 2>&1)"
[[ "$out" == wrote* ]] && ok "light build" || bad "light build" "$out"

out="$(cd "$FIG" && node tools/lines.mjs kubeharness.html 2>&1)"
[[ "$out" == *"line ends: 0 of"* ]] && ok "line ends" || bad "line ends" "$(echo "$out" | tail -3)"

out="$(node "$FIG/tools/drive.mjs" "$(page_url)" '[["eval","typeof kubeharnessFigure.select + \" \" + kubeharnessFigure.state().profile"]]' 2>&1)"
if [[ "$out" == *'eval: "function gateway-api"'* ]] && [[ "$out" == *"console: clean"* ]]; then ok "page + kubeharnessFigure"; else bad "page + kubeharnessFigure" "$(echo "$out" | tail -4)"; fi

golden="$ROOT/internal/trace/testdata/golden.jsonl"
if [[ -f "$golden" ]]; then
  out="$(cd "$FIG" && node --input-type=module -e '
import { readFileSync } from "node:fs";
import { KINDS, parseTrace } from "./replay.mjs";
const f = process.argv[1];
const kinds = new Set(parseTrace(readFileSync(f, "utf8"), f).map((e) => e.ev));
const missing = Object.keys(KINDS).filter((k) => !kinds.has(k));
if (missing.length) throw new Error(`golden has no ${missing.join(", ")} line; replay.mjs KINDS and Go disagree`);
console.log(`${kinds.size} kinds`);
' "$golden" 2>&1)"
  [[ $? -eq 0 ]] && ok "trace golden ($out)" || bad "trace golden" "$(echo "$out" | grep -m1 -E 'Error|golden')"
fi

out="$(cd "$FIG" && node tools/replay-check.mjs 2>&1)"
[[ "$out" == "replay check passed" ]] && ok "replay reducer check" || bad "replay reducer check" "$(echo "$out" | grep -m1 -E 'Error|assert')"

if compgen -G "$FIG/traces/*.jsonl" >/dev/null; then
  for f in "$FIG"/traces/*.jsonl; do
    line="$(grep -o '"ev":"report".*"line":"[^"]*"' "$f" | sed 's/.*"line":"//; s/"$//')"
    grep -qF "$line" "$SKILL/journeys/replay.json" || bad "replay journey" "$(basename "$f")'s report line is not in journeys/replay.json; run scripts/replay-journey.mjs"
  done
  out="$(cd "$FIG" && node build.mjs --traces traces --audit 2>&1)"
  [[ "$out" == *"audit passed"* ]] && ok "replay build + audit" || bad "replay build + audit" "$(echo "$out" | grep -v ': 0 ' | tail -4)"
  out="$(cd "$FIG" && node build.mjs --traces traces --light 2>&1)"
  [[ "$out" == *"wrote kubeharness-replay-light.html"* ]] && ok "replay light build" || bad "replay light build" "$out"
  out="$(cd "$FIG" && node tools/lines.mjs kubeharness-replay.html 2>&1)"
  [[ "$out" == *"line ends: 0 of"* ]] && ok "replay line ends" || bad "replay line ends" "$(echo "$out" | tail -3)"
  out="$(PAGE=kubeharness-replay; node "$FIG/tools/drive.mjs" "$(page_url)" '[["eval","kubeharnessFigure.state().profile + \" \" + document.querySelector(\"[data-part=plate]\").textContent"]]' 2>&1)"
  if [[ "$out" == *'eval: "gateway-api kh-'* ]] && [[ "$out" == *"console: clean"* ]]; then ok "replay page"; else bad "replay page" "$(echo "$out" | tail -4)"; fi
fi

command -v ffmpeg >/dev/null && ok "ffmpeg (record.sh)" || echo "ffmpeg WARN: missing; record.sh cannot make video, drive.sh still works"
exit $fail
