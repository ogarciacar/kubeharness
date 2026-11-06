#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
cd "$ROOT"

remedy_rm() {
  local name=$1
  if command -v kind >/dev/null 2>&1; then
    kind delete cluster --name "$name" >/dev/null 2>&1 || true
  fi
  local -a ids=()
  mapfile -t ids < <(docker ps -aq --filter "label=io.x-k8s.kind.cluster=$name" || true)
  if ((${#ids[@]})); then
    docker rm -f "${ids[@]}" >/dev/null 2>&1 || true
  fi
}

if [[ $# -gt 0 ]]; then
  for name in "$@"; do
    [[ "$name" == kh-* ]] || { echo "cleanup refuses non-kh cluster: $name" >&2; exit 2; }
    echo "verify-kubeharness cleanup: removing $name"
    remedy_rm "$name"
  done
else
  set +e
  out="$(hack/leaks.sh 2>&1)"
  rc=$?
  set -e
  if [[ $rc -eq 0 ]]; then
    echo "verify-kubeharness cleanup: no kh-* clusters"
    exit 0
  fi
  if [[ $rc -gt 1 ]]; then
    echo "$out" >&2
    exit 2
  fi
  while read -r name _; do
    [[ -z "$name" ]] && continue
    echo "verify-kubeharness cleanup: removing $name"
    remedy_rm "$name"
  done < <(sed 1d <<<"$out" | awk '{print $1}' | sort -u)
fi

hack/leaks.sh || true
