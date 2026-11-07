#!/usr/bin/env bash
# Usage: ./convergence.sh <logdir>
# Summarizes the "g03 flip" lines TestG03WeightFlip logs across the run-*.log
# files hack/screen.sh leaves in logdir. The output line goes into CONCEPT's
# S3 slice 4 result alongside the screen line.
set -uo pipefail
export LC_ALL=C

dir=${1:?usage: ./convergence.sh <logdir>}
lines=$(grep -h -oE 'g03 flip [a-z]+->[a-z]+ .*' "$dir"/run-*.log)
[[ -n $lines ]] || { echo "convergence flips=0" >&2; exit 1; }

# One record per flip line. Durations stay in seconds. An even count's median
# is the mean of the two central values, printed to the same 0.01s as the log.
printf '%s\n' "$lines" | awk '
function field(line, key,    i, n, parts, eq, k, val) {
  n = split(line, parts, /[ \t]+/)
  for (i = 1; i <= n; i++) {
    eq = index(parts[i], "=")
    if (eq == 0) continue
    k = substr(parts[i], 1, eq - 1)
    if (k != key) continue
    val = substr(parts[i], eq + 1)
    sub(/s$/, "", val)
    return val
  }
  return ""
}
function sort_num(a, n,    i, j, tmp) {
  for (i = 2; i <= n; i++) {
    tmp = a[i]
    j = i - 1
    while (j >= 1 && a[j] > tmp) {
      a[j + 1] = a[j]
      j--
    }
    a[j + 1] = tmp
  }
}
function median(a, n) {
  if (n % 2 == 1) return a[(n + 1) / 2]
  return (a[n / 2] + a[n / 2 + 1]) / 2
}
function need(line, key,    v) {
  v = field(line, key)
  if (v == "" || v !~ /^-?[0-9]+(\.[0-9]+)?$/) {
    printf "convergence: line lacks %s=: %s\n", key, line > "/dev/stderr"
    bad = 1
    exit 2
  }
  return v + 0
}
function secs(x) {
  return sprintf("%.2fs", x)
}
{
  st[++ns] = need($0, "status")
  dp[++nd] = need($0, "dataplane")
  e = need($0, "err")
  sw = need($0, "switches")
  h = need($0, "hold")
  if (n == 0 || e > max_err) max_err = e
  if (n == 0 || sw > max_sw) max_sw = sw
  if (n == 0 || h < min_hold) min_hold = h
  n++
}
END {
  if (bad) exit 2
  if (n == 0) {
    print "convergence flips=0" > "/dev/stderr"
    exit 1
  }
  sort_num(st, ns)
  sort_num(dp, nd)
  printf "convergence flips=%d status median=%s max=%s dataplane median=%s max=%s transition_err max=%d switches max=%d hold min=%d\n", \
    n, secs(median(st, ns)), secs(st[ns]), secs(median(dp, nd)), secs(dp[nd]), max_err, max_sw, min_hold
}
'
