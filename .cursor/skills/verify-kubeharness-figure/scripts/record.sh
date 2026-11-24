#!/usr/bin/env bash
# record.sh <profile> <scenario> [--light] [--replay] [--speed S] [--setup N] [--body N] [--hold S] [--fps N] [--seconds S]
# Films one full run (new → lamps off) as an MP4. The page runs with
# ?manual, and each frame advances the timeline by exactly 1/fps, so the
# video plays at true figure speed however slow the capture is. --replay
# films kubeharness-replay*.html, a recorded trace whose setup plays --setup
# times faster (default 10, 1 for real time), whose test body plays --body
# times slower (default: the scenario's rate in model.mjs REPLAY) with a
# --hold second minimum per step (default 1.2), and everything divided by
# --speed.
set -euo pipefail
source "$(dirname "$0")/env.sh"
profile="${1:?profile}"; scenario="${2:?scenario}"; shift 2
theme=""; fps=24; seconds=""; speed=""; setup=""; body=""; hold=""; tag=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --light) theme="--light" ;;
    --replay) PAGE=kubeharness-replay; tag="-replay" ;;
    --speed) speed="$2"; shift ;;
    --setup) setup="$2"; shift ;;
    --body) body="$2"; shift ;;
    --hold) hold="$2"; shift ;;
    --fps) fps="$2"; shift ;;
    --seconds) seconds="$2"; shift ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac
  shift
done
command -v ffmpeg >/dev/null || { echo "record.sh needs ffmpeg" >&2; exit 1; }
RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
out="$ARTIFACTS/$RUN_ID"; name="$profile-$scenario$tag${speed:+-x$speed}${setup:+-setup$setup}${body:+-body$body}${hold:+-hold$hold}${theme:+-light}"
frames="$out/.recording-$name"; mkdir -p "$frames"
url="$(page_url $theme "manual${speed:+&speed=$speed}${setup:+&setup=$setup}${body:+&body=$body}${hold:+&hold=$hold}")"
if [[ -z "$seconds" ]]; then
  seconds="$(node "$FIG/tools/drive.mjs" "$url" "[[\"eval\",\"kubeharnessFigure.select('$profile','$scenario'),kubeharnessFigure.runSeconds()\"]]" 2>&1 | sed -n 's/^eval: //p' | tail -1)"
fi
count="$(node -p "Math.ceil($seconds * $fps)")"
node -e '
const [profile, scenario, fps, count, frames] = process.argv.slice(1);
const a = [["eval", `kubeharnessFigure.select("${profile}","${scenario}")`], ["wait", 300]];
for (let i = 0; i < +count; i++) {
  a.push(["eval", `kubeharnessFigure.advance(${1 / +fps}).phase`]);
  a.push(["shot", `${frames}/${String(i).padStart(5, "0")}.png`, null, 2]);
}
require("fs").writeFileSync(`${frames}.actions.json`, JSON.stringify(a));
' "$profile" "$scenario" "$fps" "$count" "$frames"
echo "recording $name: ${seconds}s at ${fps} fps = $count frames"
node "$FIG/tools/drive.mjs" "$url" --actions "$frames.actions.json" --height 1300 >"$out/$name.record.log" 2>&1 || { tail -5 "$out/$name.record.log"; exit 1; }
grep -E 'console|exception' "$out/$name.record.log" | tail -3
ffmpeg -loglevel error -y -framerate "$fps" -i "$frames/%05d.png" -vf "scale=trunc(iw/2)*2:trunc(ih/2)*2" -c:v libx264 -pix_fmt yuv420p "$out/$name.mp4"
cp "$frames/$(printf '%05d' $((count * 3 / 4))).png" "$out/$name.poster.png"
grep -o 'eval: "[^"]*"' "$out/$name.record.log" | uniq -c | sed 's/eval: //' >"$out/$name.phases.txt"
rm -rf "$frames" "$frames.actions.json"
echo "video: $out/$name.mp4"
