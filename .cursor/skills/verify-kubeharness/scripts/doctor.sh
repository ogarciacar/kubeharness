#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
export GOENV_QUIET=1
# shellcheck source=goenv.sh
source "$ROOT/.cursor/skills/verify-kubeharness/scripts/goenv.sh"

fail() { echo "verify-kubeharness doctor: FAIL: $*" >&2; exit 1; }
ok() { echo "verify-kubeharness doctor: $*"; }

cd "$ROOT"

go version >/dev/null || fail "go not runnable"
mod="$(go list -m -f '{{.Path}} {{.GoVersion}}' 2>/dev/null)" || fail "go list -m failed in $ROOT"
[[ "$mod" == github.com/ogarciacar/kubeharness* ]] || fail "unexpected module: $mod"
ok "module $mod ($(go version | awk '{print $3}'))"

docker info >/dev/null 2>&1 || fail "docker daemon not reachable (e2e drives need Docker Desktop / a daemon)"
ok "docker $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo ok)"

if ! go test -count=0 ./... >/tmp/kubeharness-doctor-compile.txt 2>&1; then
  tail -20 /tmp/kubeharness-doctor-compile.txt >&2
  fail "go test -count=0 ./... does not compile; see /tmp/kubeharness-doctor-compile.txt"
fi
ok "packages compile"

set +e
leak_out="$(hack/leaks.sh 2>&1)"
leak_rc=$?
set -e
case $leak_rc in
  0) ok "$leak_out" ;;
  1)
    echo "$leak_out"
    echo "verify-kubeharness doctor: WARN: leftover kh-* clusters exist; a drive may still run, but cleanup proof must account for them" >&2
    ;;
  *)
    echo "$leak_out" >&2
    fail "hack/leaks.sh could not list clusters (rc=$leak_rc)"
    ;;
esac

ok "ready"
