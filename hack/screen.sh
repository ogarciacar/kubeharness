#!/usr/bin/env bash
# Usage: hack/screen.sh [runs] [go test -run pattern]
set -uo pipefail
cd "$(dirname "$0")/.."

runs=${1:-20}
pattern=${2:-.}
if [[ ! $runs =~ ^[1-9][0-9]*$ ]]; then
  echo "usage: hack/screen.sh [runs >= 1] [go test -run pattern]" >&2
  exit 2
fi
logdir=$(mktemp -d)
pass=0 fail=0 leak=0 unknown=0

# kh_clusters prints the kh-* clusters leaks.sh sees, one per line. It fails
# when leaks.sh cannot list containers.
kh_clusters() {
  local out
  out=$(hack/leaks.sh 2>/dev/null)
  [[ $? -le 1 ]] || return 1
  sed 1d <<<"$out" | cut -d' ' -f1 | sort -u
}

for i in $(seq 1 "$runs"); do
  log="$logdir/run-$i.log"
  before=$(kh_clusters)
  listed_before=$?
  go test -race -tags e2e -count=1 -timeout 20m -v -run "$pattern" ./... >"$log" 2>&1
  tests=$?
  healthy=$(grep -o 'phase=ready cause=ok cleanup=succeeded remaining=0 create=[^ ]* ready=[^ ]*' "$log" | head -1 | cut -d' ' -f5-)
  if [[ $tests -ne 0 ]]; then
    fail=$((fail + 1)); status=FAIL
  elif ! grep -q -- '--- PASS' "$log" || grep -q 'no tests to run' "$log"; then
    fail=$((fail + 1)); status="FAIL(no test ran)"
  else
    pass=$((pass + 1)); status=pass
  fi
  hack/leaks.sh >>"$log" 2>&1
  if after=$(kh_clusters) && [[ $listed_before -eq 0 ]]; then
    new=$(comm -13 <(echo "$before") <(echo "$after") | sed '/^$/d')
    if [[ -n "$new" ]]; then
      leak=$((leak + 1)); status="$status+LEAK($(tr '\n' ' ' <<<"$new"))"
    fi
  else
    unknown=$((unknown + 1)); status="$status+LEAKS-UNKNOWN"
  fi
  echo "run $i/$runs: $status $healthy"
done

echo "screening: runs=$runs pass=$pass fail=$fail leaked_runs=$leak leaks_unknown=$unknown logs=$logdir"
[[ $fail -eq 0 && $leak -eq 0 && $unknown -eq 0 ]]
