#!/usr/bin/env bash
# Usage: hack/leaks.sh [cluster-name regex, default ^kh-]
set -euo pipefail
pattern=${1:-^kh-}

if ! containers=$(docker ps -a --filter label=io.x-k8s.kind.cluster \
  --format '{{.Label "io.x-k8s.kind.cluster"}} {{.Names}} {{.Status}}'); then
  echo "kubeharness leaks: unknown, docker ps failed" >&2
  exit 2
fi
rc=0
leaks=$(grep -E "$pattern" <<<"$containers") || rc=$?
if [[ $rc -gt 1 ]]; then
  echo "kubeharness leaks: unknown, bad pattern $pattern" >&2
  exit 2
fi

if [[ -z "$leaks" ]]; then
  echo "kubeharness leaks: 0"
  exit 0
fi
echo "kubeharness leaks: $(wc -l <<<"$leaks" | tr -d ' ')"
echo "$leaks"
exit 1
