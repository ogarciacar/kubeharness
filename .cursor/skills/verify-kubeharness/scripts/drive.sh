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

{
  echo "feature=$FEATURE"
  echo "run_id=$RUN_ID"
  echo "cwd=$ROOT"
  echo "command=go test $*"
  echo "started=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "GOTOOLCHAIN=$GOTOOLCHAIN"
  go version
} >"$META"

set +e
hack/leaks.sh >"$ART/${FEATURE}.leaks-before.txt" 2>&1
before_rc=$?
go test "$@" >"$LOG" 2>&1
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
