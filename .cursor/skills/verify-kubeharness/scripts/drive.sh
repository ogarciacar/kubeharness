#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
FEATURE="${1:?feature-id required}"
shift
export GOENV_QUIET=1
# shellcheck source=goenv.sh
source "$ROOT/.cursor/skills/verify-kubeharness/scripts/goenv.sh"
cd "$ROOT"

RUN_ID="${VERIFY_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
ART="$ROOT/.cursor/skills/verify-kubeharness/artifacts/$RUN_ID"
mkdir -p "$ART"
LOG="$ART/${FEATURE}.log"
META="$ART/${FEATURE}.meta.txt"

# Nested example modules (examples/gateway, examples/mcp, …) are not in the
# root module. A trailing ./examples/… package with its own go.mod becomes
# `go -C <dir> test … .`, matching CI.
nested_dir=""
forward=()
for a in "$@"; do
  if [[ "$a" == ./* && -f "$ROOT/${a#./}/go.mod" ]]; then
    nested_dir="${a#./}"
  else
    forward+=("$a")
  fi
done
if [[ -n "$nested_dir" ]]; then
  cmd_line="go -C $nested_dir test ${forward[*]} ."
else
  cmd_line="go test $*"
fi

{
  echo "feature=$FEATURE"
  echo "run_id=$RUN_ID"
  echo "cwd=$ROOT"
  echo "command=$cmd_line"
  echo "started=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "GOTOOLCHAIN=$GOTOOLCHAIN"
  go version
} >"$META"

set +e
hack/leaks.sh >"$ART/${FEATURE}.leaks-before.txt" 2>&1
before_rc=$?
if [[ -n "$nested_dir" ]]; then
  go -C "$nested_dir" test "${forward[@]}" . >"$LOG" 2>&1
else
  go test "$@" >"$LOG" 2>&1
fi
test_rc=$?
hack/leaks.sh >"$ART/${FEATURE}.leaks-after.txt" 2>&1
after_rc=$?
set -e

{
  echo "finished=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "go_test_exit=$test_rc"
  echo "leaks_before_exit=$before_rc"
  echo "leaks_after_exit=$after_rc"
  echo "log=$LOG"
} >>"$META"

echo "verify-kubeharness drive: feature=$FEATURE run_id=$RUN_ID go_test_exit=$test_rc artifacts=$ART"
exit "$test_rc"
