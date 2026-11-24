#!/usr/bin/env bash
# Screenshots of every profile and scenario in both themes, into shots/.
# Each one freezes the figure on a step that shows the scenario working.
set -euo pipefail
cd "$(dirname "$0")"
node build.mjs >/dev/null
node build.mjs --light >/dev/null
mkdir -p shots
specs=(
  "gateway-api G01 7"
  "gateway-api G02 8"
  "gateway-api G03 7"
  "gateway-agentgateway G01 9"
  "gateway-agentgateway G02 10"
  "gateway-agentgateway G03 7"
  "gateway-agentgateway A01 9"
  "gateway-agentgateway K01 7"
)
for theme in "" "-light"; do
  for spec in "${specs[@]}"; do
    read -r profile scenario step <<<"$spec"
    out="shots/${profile}-${scenario}${theme}.png"
    node tools/capture.mjs "file://$PWD/kubeharness${theme}.html?profile=${profile}&scenario=${scenario}&step=${step}" --out "$out" --wait 400 | grep -E 'readout|console' | sed "s|^|${out}: |"
  done
done
