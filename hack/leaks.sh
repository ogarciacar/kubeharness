#!/usr/bin/env bash
set -euo pipefail

if ! containers=$(docker ps -a --filter label=io.x-k8s.kind.cluster \
  --format '{{.Label "io.x-k8s.kind.cluster"}} {{.Names}} {{.Status}}'); then
  echo "kubeharness leaks: unknown, docker ps failed" >&2
  exit 2
fi
leaks=$(grep '^kh-' <<<"$containers" || true)

if [[ -z "$leaks" ]]; then
  echo "kubeharness leaks: 0"
  exit 0
fi
echo "kubeharness leaks: $(wc -l <<<"$leaks" | tr -d ' ')"
echo "$leaks"
exit 1
