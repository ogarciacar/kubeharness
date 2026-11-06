#!/usr/bin/env bash
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
cd "$ROOT"

wanted="$(awk '/^toolchain / {print $2; exit}' go.mod)"
if [[ -z "${wanted:-}" ]]; then
  wanted="$(awk '/^go / {print "go"$2; exit}' go.mod)"
fi
# Capture the install GOROOT before GOTOOLCHAIN may retarget `go`.
installed_root="$(go env GOROOT 2>/dev/null || true)"
installed_ver=""
if [[ -n "$installed_root" && -x "$installed_root/bin/go" ]]; then
  installed_ver="$("$installed_root/bin/go" version 2>/dev/null | awk '{print $3}')"
fi
export GOTOOLCHAIN="$wanted"
tool_ver="$(go version 2>/dev/null | awk '{print $3}')"
if [[ -n "$installed_ver" && -n "$tool_ver" && "$installed_ver" != "$tool_ver" ]]; then
  export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/kubeharness-go-build-$tool_ver}"
  mkdir -p "$GOCACHE"
fi

if [[ -z "${GOENV_QUIET:-}" ]]; then
  echo "verify-kubeharness goenv: GOTOOLCHAIN=$GOTOOLCHAIN GOCACHE=${GOCACHE:-default} $(go version)" >&2
fi
