#!/usr/bin/env bash
# Usage: ./compare.sh [runs per side]
set -uo pipefail
export LC_ALL=C
cd "$(dirname "$0")"

runs=${1:-5}
if [[ ! $runs =~ ^[1-9][0-9]*$ ]]; then
  echo "usage: ./compare.sh [runs per side >= 1]" >&2
  exit 2
fi
go run ./setuplines || exit 1
go test -c -tags e2e -o /tmp/g01.test . || exit 1

declare -a harness baseline
failures=0
run() {
  local test=$1 out secs
  out=$(/tmp/g01.test -test.run "^${test}\$" -test.count=1 -test.timeout=20m -test.v 2>&1)
  if [[ $? -ne 0 ]]; then
    failures=$((failures + 1))
    echo "$test FAILED" >&2
    return
  fi
  secs=$(grep -oE -- "--- PASS: ${test} \(([0-9.]+)s\)" <<<"$out" | grep -oE '[0-9.]+s' | tr -d s)
  if [[ -z $secs ]]; then
    failures=$((failures + 1))
    echo "$test did not run" >&2
    return
  fi
  echo "$test ${secs}s" >&2
  if [[ $test == TestG01Routing ]]; then harness+=("$secs"); else baseline+=("$secs"); fi
}
for i in $(seq 1 "$runs"); do
  if (( i % 2 )); then run TestG01Routing; run TestG01RoutingBaseline
  else run TestG01RoutingBaseline; run TestG01Routing; fi
done

stats() { (( $# )) || { echo "n=0"; return; }; printf '%s\n' "$@" | sort -n | awk '{a[NR]=$1} END {if (NR==0) {print "n=0"; exit}; m=(NR%2)?a[(NR+1)/2]:(a[NR/2]+a[NR/2+1])/2; printf "n=%d median=%.1fs range=%.1f-%.1fs\n", NR, m, a[1], a[NR]}'; }
echo "wall_time kubeharness $(stats ${harness[@]+"${harness[@]}"})"
echo "wall_time baseline    $(stats ${baseline[@]+"${baseline[@]}"})"
echo "failures=$failures"
[[ $failures -eq 0 ]]
