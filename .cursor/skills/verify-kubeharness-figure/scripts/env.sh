ROOT="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)"
SKILL="$ROOT/.cursor/skills/verify-kubeharness-figure"
FIG="$ROOT/docs/figure"
ARTIFACTS="$SKILL/artifacts"
page_url() { # page_url [--light] [query]; PAGE=kubeharness-replay picks the replay page
  local suffix="" query=""
  [[ "${1:-}" == "--light" ]] && { suffix="-light"; shift; }
  query="${1:-}"
  echo "file://$FIG/${PAGE:-kubeharness}${suffix}.html${query:+?$query}"
}
