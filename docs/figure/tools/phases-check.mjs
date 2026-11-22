// node tools/phases-check.mjs <trace.jsonl> <scenario> <video.phases.txt> [fps] [setup]
// Checks a replay video's per-phase frame counts against its trace, two ways:
// provisioning and wait-usable against the trace's own phase events at real
// time divided by the setup factor, and every phase against the figure's time
// map played frame by frame. The setup factor is the argument, else the
// `-setupN` in the video's file name (record.sh --setup N), else 10.
import { readFileSync } from "node:fs";
import { bodyOf, parseTrace, stepsFromTrace, timeMap, timed } from "../replay.mjs";
import { LAMPS, REPLAY } from "../model.mjs";

const [tracePath, scenario, phasesPath, fpsArg, setupArg] = process.argv.slice(2);
const fps = Number(fpsArg ?? 24);
const setup = Number(setupArg ?? /-setup([\d.]+)/.exec(phasesPath)?.[1] ?? 10);
const events = parseTrace(readFileSync(tracePath, "utf8"), tracePath);
const facts = REPLAY[scenario];
const steps = timed(stepsFromTrace(events, facts), timeMap(bodyOf(events), { ...facts.body, setup }));
const recorded = readFileSync(phasesPath, "utf8").trim().split("\n").map((l) => {
  const [, n, phase] = l.match(/^\s*(\d+)\s+"([^"]+)"/);
  return { phase, n: +n };
});

const runs = (labels) => labels.reduce((out, p) => (out.at(-1)?.phase === p ? out.at(-1).n++ : out.push({ phase: p, n: 1 }), out), []);
const total = recorded.reduce((a, r) => a + r.n, 0);
const ends = [];
steps.reduce((t, s) => (ends.push(t + s.hold), t + s.hold), 0);
const phaseAt = (t) => {
  const i = ends.findIndex((e) => t < e - 1e-9);
  const s = steps[i < 0 ? steps.length - 1 : i];
  return s.lamp >= 0 ? LAMPS[s.lamp] : "off";
};
const expected = runs(Array.from({ length: total }, (_, k) => phaseAt((k + 1) / fps)));

const problems = [];
const byPhase = (list) => list.reduce((m, r) => m.set(r.phase, (m.get(r.phase) ?? 0) + r.n), new Map());
const want = byPhase(expected);
const got = byPhase(recorded.filter((r) => !(r === recorded.at(-1) && r.phase !== "off" && r.n <= 1)));
for (const [phase, n] of want) if (Math.abs((got.get(phase) ?? 0) - n) > 1) problems.push(`${phase}: video ${got.get(phase) ?? 0} frames, time map ${n}`);

// From the trace alone, not the time map: the phase lasts its real span ÷ the
// setup factor, and its first moment, up to the next event, holds at least the
// page's 0.6 s phase hold because it lights a new lamp (none at real time).
const rate = 1 / setup;
const phaseHold = setup === 1 ? 0 : 0.6;
const at = (p) => events.find((e) => e.ev === "phase" && e.phase === p)?.at;
for (const [p, next] of [["provisioning", "wait-usable"], ["wait-usable", "ready"]]) {
  if (at(p) === undefined || at(next) === undefined) continue;
  const first = (events.find((e) => e.at > at(p)).at - at(p)) / 1000;
  const seconds = ((at(next) - at(p)) / 1000) * rate + Math.max(0, phaseHold - first * rate);
  const frames = seconds * fps;
  if (Math.abs((got.get(p) ?? 0) - frames) > 1.5) problems.push(`${p}: video ${got.get(p) ?? 0} frames, trace ${frames.toFixed(1)} (real ${((at(next) - at(p)) / 1000).toFixed(3)} s × ${+rate.toFixed(4)}, phase hold ${phaseHold} s)`);
}

const row = (m) => [...m].map(([p, n]) => `${p} ${n}`).join(", ");
console.log(`${tracePath}: setup ×${setup} · video [${row(got)}] · time map [${row(want)}] · ${total} frames`);
if (problems.length) {
  console.log("phases check FAILED\n  " + problems.join("\n  "));
  process.exit(1);
}
console.log("phases check passed");
