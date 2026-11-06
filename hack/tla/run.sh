#!/usr/bin/env bash
# Run the lifecycle TLA+ model. Usage:
#   hack/tla/run.sh              # expect Inv to hold (Mutate=FALSE)
#   hack/tla/run.sh --mutant     # expect Inv to fail (Mutate=TRUE)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DIR="$ROOT/hack/tla"
cd "$DIR"

JAR="$DIR/tla2tools.jar"
PIN_VERSION="1.8.0"
PIN_SHA256="7a0a1e90628ebff2e3e8733375f8b684fa59504d2e02bddc4cb51a7679bb7e04"
URL="https://github.com/tlaplus/tlaplus/releases/download/v${PIN_VERSION}/tla2tools.jar"

if [[ ! -f "$JAR" ]]; then
  echo "hack/tla/run.sh: downloading tla2tools.jar ${PIN_VERSION}" >&2
  curl -fsSL -o "$JAR" "$URL"
fi
got="$(shasum -a 256 "$JAR" | awk '{print $1}')"
if [[ "$got" != "$PIN_SHA256" ]]; then
  echo "hack/tla/run.sh: tla2tools.jar sha256 $got != pinned $PIN_SHA256" >&2
  exit 2
fi

cfg=Lifecycle.cfg
expect_fail=0
if [[ "${1:-}" == "--mutant" ]]; then
  cfg=LifecycleMutant.cfg
  expect_fail=1
fi

out="$(mktemp)"
set +e
java -XX:+UseParallelGC -cp "$JAR" tlc2.TLC -config "$cfg" Lifecycle >"$out" 2>&1
rc=$?
set -e
cat "$out"

if [[ $expect_fail -eq 0 ]]; then
  if grep -q "Model checking completed. No error has been found." "$out"; then
    echo "hack/tla/run.sh: PASS (Inv holds)"
    exit 0
  fi
  echo "hack/tla/run.sh: FAIL (expected Inv to hold)" >&2
  exit 1
fi

if grep -q "Invariant Inv is violated" "$out"; then
  echo "hack/tla/run.sh: PASS (mutant violated Inv as expected)"
  exit 0
fi
echo "hack/tla/run.sh: FAIL (mutant did not violate Inv; rc=$rc)" >&2
exit 1
