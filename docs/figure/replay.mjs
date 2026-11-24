import { LAMPS, REPLAY } from "./model.mjs";

export const VERSION = 1;

const ON_WAIT = ["usable", "crds", "rollout", "class", "job", "gateway", "route", "response", "probe"];

// Fields per event kind, from docs/figure/trace-design.md "Trace format".
// internal/trace/testdata/golden.jsonl holds one line of every kind and the
// figure doctor parses it, so a kind added in Go fails here first.
export const KINDS = {
  trace: { v: "number", test: "string", cluster: "string", start: "string" },
  call: { id: "number", fn: "string", arg: "string?" },
  return: { id: "number", failed: "boolean?" },
  phase: { phase: LAMPS.slice(0, 5) },
  wait: { in: "number", on: ON_WAIT, subject: "string", state: "string?", done: "boolean?" },
  object: { in: "number", api: "string?", obj: "string", state: ["applied", "deleting", "gone"] },
  pod: { in: "number", api: "string?", owner: "string", pod: "string", ready: "boolean" },
  forward: { in: "number?", svc: "string", port: "number", pod: "string", local: "string" },
  request: { id: "number", in: "number?", via: "string", method: "string", url: "string" },
  response: { id: "number", status: "number?", err: "string?", ms: "number" },
  body: { id: "number", head: "string" },
  report: { in: "number", line: "string" },
};

function check(e, where) {
  const spec = KINDS[e.ev];
  if (!spec) throw new Error(`${where}: unknown ev ${JSON.stringify(e.ev)}`);
  if (!Number.isInteger(e.at) || e.at < 0) throw new Error(`${where}: at must be a non-negative integer ms, got ${JSON.stringify(e.at)}`);
  for (const [field, want] of Object.entries(spec)) {
    const v = e[field];
    if (Array.isArray(want)) {
      if (!want.includes(v)) throw new Error(`${where}: ${e.ev}.${field} is ${JSON.stringify(v)}, want one of ${want.join(", ")}`);
      continue;
    }
    const optional = want.endsWith("?");
    if (v === undefined && optional) continue;
    if (typeof v !== want.replace("?", "")) throw new Error(`${where}: ${e.ev}.${field} is ${JSON.stringify(v)}, want a ${want.replace("?", "")}`);
  }
}

/** Parse one JSONL trace into its events. Throws `${source}:${line}: …` on any schema error. */
export function parseTrace(text, source = "trace") {
  const lines = text.split("\n");
  if (lines.at(-1) === "") lines.pop();
  const events = [];
  lines.forEach((raw, i) => {
    const where = `${source}:${i + 1}`;
    let e;
    try {
      e = JSON.parse(raw);
    } catch (err) {
      throw new Error(`${where}: not JSON (${err.message})`);
    }
    if (typeof e !== "object" || e === null || Array.isArray(e)) throw new Error(`${where}: not a JSON object`);
    check(e, where);
    if (i === 0 && (e.ev !== "trace" || e.v !== VERSION)) throw new Error(`${where}: first line must be {"ev":"trace","v":${VERSION}}`);
    if (i > 0 && e.ev === "trace") throw new Error(`${where}: a second trace header`);
    if (i > 0 && e.at < events[i - 1].at) throw new Error(`${where}: at ${e.at} goes back from ${events[i - 1].at}`);
    events.push(e);
  });
  if (!events.length) throw new Error(`${source}: empty trace`);
  return events;
}

export function describe(events, source = "trace") {
  const header = events[0];
  const test = header.test.split("/")[0];
  const scenario = Object.keys(REPLAY).find((k) => REPLAY[k].test === test);
  if (!scenario) throw new Error(`${source}: test ${test} has no entry in model.mjs REPLAY`);
  const install = events.find((e) => e.ev === "call" && e.fn === "InstallGateway");
  if (!install?.arg) throw new Error(`${source}: no InstallGateway call with a profile arg`);
  return { test: header.test, cluster: header.cluster, profile: install.arg, scenario };
}

const backendIn = (head) => /"backend"\s*:\s*"([^"]+)"/.exec(head)?.[1];

export const modelIn = (head) => /"model"\s*:\s*"([^"]+)"/.exec(head)?.[1];

/**
 * Where one request's pulse ends: "a" | "b" (a post), "gate", or "none" (the
 * diverter). A 2xx goes to the backend its body names; the G02 burst closes
 * its bodies unread and an MCP body names no backend, so otherwise a 2xx goes
 * to the backend its path's rule sends to.
 */
export function outletOf(request, response, body, facts) {
  const ok = response?.status >= 200 && response.status < 300;
  const path = new URL(request.url).pathname;
  const rule = Object.keys(facts.paths).find((p) => path.startsWith(p));
  const post = facts.posts[backendIn(body?.head ?? "")] ?? facts.paths[rule];
  if (ok && post) return post;
  if (!ok && facts.gate && path.startsWith(facts.gate.path)) return "gate";
  return "none";
}

/**
 * What an MCP response answers, read off its body head: `{ method, text }`,
 * where `text` is the tool names of tools/list or the text of tools/call, or
 * null for a request off the MCP path or a body that answers no listed method.
 */
export function answerOf(request, body, mcp) {
  if (!mcp || !body || !new URL(request.url).pathname.startsWith(mcp.path)) return null;
  const flat = body.head.replace(/\s/g, "");
  const method = mcp.answers.find((a) => flat.includes(a.has))?.method;
  if (!method) return null;
  const all = (key) => [...body.head.matchAll(new RegExp(`"${key}"\\s*:\\s*"([^"]*)"`, "g"))].map((m) => m[1]);
  const TEXT = { "tools/list": () => `[${all("name").join(", ")}]`, "tools/call": () => all("text").join("") };
  return { method, text: TEXT[method]?.() ?? "" };
}

const LEVER = { a: "a", b: "b", gate: "b", none: "none" };

function exchanges(events, facts) {
  const byId = (ev) => new Map(events.filter((e) => e.ev === ev).map((e) => [e.id, e]));
  const res = byId("response");
  const body = byId("body");
  const out = new Map();
  let run = null;
  for (const e of events) {
    if (e.ev === "response" || e.ev === "body") continue;
    if (e.ev !== "request") {
      run = null;
      continue;
    }
    const r = res.get(e.id);
    const to = outletOf(e, r, body.get(e.id), facts);
    const answer = answerOf(e, body.get(e.id), facts.mcp);
    const model = facts.models ? modelIn(body.get(e.id)?.head ?? "") : undefined;
    const said = answer ? `${answer.method}${answer.text ? ` = ${answer.text}` : ""}` : "";
    const key = `${e.via} ${e.method} ${e.url} ${r?.status ?? r?.err} ${to} ${said} ${model}`;
    if (run?.key !== key) run = { key, count: 0, e, r, to, answer, said, model };
    run.count++;
    out.set(e.id, run);
  }
  for (const x of out.values()) {
    const name = x.model ?? Object.keys(facts.posts).find((n) => facts.posts[n] === x.to);
    x.text = `${x.e.method} ${x.e.url} → ${x.r?.status ?? x.r?.err ?? "no response"}${name ? ` from ${name}` : ""}${x.count > 1 ? ` ×${x.count}` : ""}${x.said ? ` · ${x.said}` : ""}`;
  }
  return out;
}

const RECEIVER = { Gateway: "gc", AwaitRoute: "gw", AwaitResponse: "gw", AwaitProbe: "gw", Client: "gw" };
const CALL_TEXT = {
  New: () => "kubeharness.New(t)",
  Apply: (e, f) => `h.Apply(${e.arg})${f.weights?.dirs[e.arg] ? ` · ${f.weights.dirs[e.arg].text}` : ""}`,
  InstallGateway: (e, f) => `h.InstallGateway(kubeharness.${f.ctors[e.arg] ?? e.arg})`,
  Cleanup: () => "t.Cleanup",
};
const callText = (e, f) => CALL_TEXT[e.fn]?.(e, f) ?? `${RECEIVER[e.fn] ?? "h"}.${e.fn}(${e.arg ?? ""})`;

const CALL_ENTER = {
  New: () => ({ lamp: 0 }),
  InstallGateway: (s, e) => ({ show: true, cart: 1, profile: e.arg }),
  Cleanup: () => ({ lamp: 6, show: false, cart: 1, lever: "none" }),
};
const CALL_EXIT = {
  New: (s, e) => (e.failed ? {} : { lamp: 5 }),
  InstallGateway: () => ({ cart: 0 }),
  Cleanup: () => ({ lamp: -1, ready: { a: false, b: false } }),
};

const WAIT_BENCH = {
  route: (s, e, f) => {
    const refs = [...(e.state ?? "").matchAll(/ResolvedRefs=(True|False)/g)].at(-1)?.[1];
    return e.subject === f.gate?.route && refs ? { gate: refs === "True" } : {};
  },
};
const WAIT_DONE = { class: (e) => `GatewayClass ${e.subject} Accepted` };
const waitText = (e) => (e.state ? `${e.on} ${e.subject} ${e.state}` : e.done ? WAIT_DONE[e.on]?.(e) ?? `${e.on} ${e.subject} done` : `${e.on} ${e.subject}`);

const nsName = (owner) => owner.split("/").slice(-2).join("/");

const flipOf = (e, x) => (e.state === "applied" && e.obj === `HTTPRoute/${x.facts.weights?.route}` ? x.facts.weights.dirs[x.calls.get(e.in)?.arg] : undefined);

export const ON = {
  trace: (s, e) => ({ ...s, cluster: e.cluster }),
  call: (s, e, x) => ({ ...s, calls: [...s.calls, { id: e.id, fn: e.fn, text: callText(e, x.facts) }], obs: "", ...(s.model !== undefined && !x.senders.has(e.id) && { model: -1, display: "" }), ...CALL_ENTER[e.fn]?.(s, e) }),
  return: (s, e, x) => ({ ...s, calls: s.calls.filter((c) => c.id !== e.id), last: s.calls.find((c) => c.id === e.id)?.text ?? "", mcp: false, checks: x.checkIds.has(e.id) ? { ...s.checks, [e.id]: e.failed ? "fail" : "pass" } : s.checks, ...CALL_EXIT[s.calls.find((c) => c.id === e.id)?.fn]?.(s, e) }),
  phase: (s, e) => ({ ...s, lamp: LAMPS.indexOf(e.phase), phase: e.phase, obs: `phase=${e.phase}` }),
  wait: (s, e, x) => ({ ...s, obs: waitText(e), ...WAIT_BENCH[e.on]?.(s, e, x.facts) }),
  object: (s, e, x) => ({ ...s, obs: `${e.obj.replace("/", " ")} ${e.state}`, lever: flipOf(e, x)?.post ?? s.lever }),
  pod: (s, e, x) => {
    const post = x.facts.owners[nsName(e.owner)];
    const ns = nsName(e.owner).split("/")[0];
    return { ...s, obs: `Pod ${ns}/${e.pod} ${e.ready ? "Ready" : "not Ready"}`, ready: post && e.ready ? { ...s.ready, [post]: true } : s.ready };
  },
  forward: (s, e) => ({ ...s, obs: `Pod ${e.pod} → ${e.local}` }),
  request: (s, e, x) => {
    const f = x.facts;
    const run = x.ex.get(e.id);
    // The valve follows the route status; a response can still come from the
    // proxy's older config, so say so when one passes a closed valve.
    const lag = f.gate && run.to === f.paths[f.gate.path] && !s.gate ? " · proxy still routing after ResolvedRefs=False" : "";
    const mcp = !!f.mcp && new URL(e.url).pathname.startsWith(f.mcp.path);
    const display = run.answer && run.answer.method === f.mcp.display ? run.answer.text : s.display;
    const model = f.models ? { model: Object.keys(f.models).indexOf(run.model), display: f.models[run.model]?.tag ?? "" } : {};
    return { ...s, lever: f.weights ? s.lever : LEVER[run.to], mcp, display, ...model, obs: run.text + lag, last: "", pulse: run.to };
  },
  response: (s) => s,
  body: (s) => s,
  report: (s, e) => ({ ...s, report: e.line, obs: "", verdict: verdictOf(e.line), stopped: stoppedAt(e.line) }),
};

/** PASS when the report line's cause is ok, FAIL for any other cause, such as test-failed or a setup cause. */
export const verdictOf = (line) => (/\bcause=ok\b/.test(line) ? "PASS" : "FAIL");

/**
 * The lamp a failed run stopped at, from its report line: the test lamp for a
 * test-failed cause, else the phase the run last reached; -1 for an ok cause.
 */
export const stoppedAt = (line) => {
  const cause = /\bcause=(\S+)/.exec(line)?.[1];
  if (cause === "ok") return -1;
  return LAMPS.indexOf(cause === "test-failed" ? "test" : /\bphase=(\S+)/.exec(line)?.[1]);
};

const checkLabel = (arg = "") => arg.replace(/^(\S+ )https?:\/\/[^/]+/, "$1");

/**
 * The run's checks, in call order: one per Await* call, `{ id, fn, label }`.
 * Each turns pass or fail when its call returns, by the return's `failed`.
 */
export const checksOf = (events) => events.filter((e) => e.ev === "call" && e.fn.startsWith("Await")).map((e) => ({ id: e.id, fn: e.fn, label: checkLabel(e.arg) }));

const callsOf = (events) => new Map(events.filter((e) => e.ev === "call").map((e) => [e.id, e]));
const ref = (kind, name, part, state, api) => ({ id: `${kind}/${name}`, kind, name, part, state, api });

const NAMED = {
  object: (e, f, fn) => {
    const [kind, ...rest] = e.obj.split("/");
    if (f.parts[e.obj]) return [ref(kind, rest.join("/"), f.parts[e.obj], e.state, e.api)];
    return fn === "InstallGateway" ? [ref("install", e.obj, f.install, e.state)] : [];
  },
  pod: (e, f, fn) => {
    const owner = nsName(e.owner);
    return e.ready ? [ref("Pod", `${owner.split("/")[0]}/${e.pod}`, f.owners[owner] ?? f.calls[fn], "ready", e.api)] : [];
  },
  forward: (e, f, fn) => {
    const part = f.parts[`Service/${e.svc}`] ?? f.calls[fn];
    return [ref("Service", e.svc, part, "ready"), ref("Pod", `${e.svc.split("/")[0]}/${e.pod}`, part, "ready")];
  },
};
const namedBy = (e, f, calls) => (NAMED[e.ev]?.(e, f, calls.get(e.in)?.fn) ?? []).filter((r) => r.part);

/**
 * The registry of objects on the bench, in the order the trace first names
 * them: `{ id, kind, name, api?, part, from, to }`, with `from` and `to` in
 * trace seconds from the object's own events (applied or Ready, and gone); `to`
 * is null while it still exists when the trace ends. `api` is the apiVersion an
 * object or pod line carries, and stays unset for an object only a forward
 * names or a trace that predates the field. The install's own objects are one
 * entry of kind "install" whose name counts them, with no api.
 */
export function objectsOf(events, facts) {
  const calls = callsOf(events);
  const byId = new Map();
  const install = new Set();
  for (const e of events) {
    for (const r of namedBy(e, facts, calls)) {
      if (r.kind === "install") install.add(r.name);
      const id = r.kind === "install" ? "install" : r.id;
      if (!byId.has(id)) byId.set(id, { id, kind: r.kind, name: r.name, part: r.part, from: e.at / 1000, to: null });
      if (r.api) byId.get(id).api ??= r.api;
      if (r.state === "gone") byId.get(id).to = e.at / 1000;
    }
  }
  if (byId.has("install")) byId.get("install").name = `+${install.size} install objects`;
  return [...byId.values()];
}

/**
 * The name panel's groups, in pin order: `{ part, pin, kind, head, rows }`, one
 * per part of the scenario's `kinds`. `head` is the part's first object of its
 * primary kind, or null when the trace never names one; `rows` are the part's
 * other objects, the install count last.
 */
export function groupsOf(objects, facts) {
  return Object.entries(facts.kinds).map(([part, kind], i) => {
    const on = objects.filter((o) => o.part === part);
    const head = on.find((o) => o.kind === kind) ?? null;
    const rows = on.filter((o) => o !== head).sort((a, b) => (a.kind === "install") - (b.kind === "install"));
    return { part, pin: i + 1, kind, head, rows };
  });
}

function touchedBy(e, before, after, x, objects) {
  const onPart = (part) => objects.filter((o) => o.part === part).map((o) => o.id);
  const ids = namedBy(e, x.facts, x.calls).map((r) => (r.kind === "install" ? "install" : r.id));
  if (e.ev === "request" && (after.pulse === "a" || after.pulse === "b")) ids.push(...onPart(after.pulse));
  if (e.ev === "wait" && e.on === "route") ids.push(`HTTPRoute/${e.subject}`);
  if (before.gate !== after.gate) ids.push(...onPart("gate"), ...onPart(x.facts.paths[x.facts.gate.path]));
  return ids;
}

function annotate(steps, objects, touched) {
  steps.forEach((step, i) => {
    const end = i + 1 < steps.length ? steps[i + 1].at : Infinity;
    const gone = objects.filter((o) => o.to !== null && o.to < end);
    const present = objects.filter((o) => o.from < end && !gone.includes(o));
    const live = new Set([...present, ...gone.filter((o) => o.to >= step.at)].map((o) => o.id));
    Object.assign(step, { present: present.map((o) => o.id), gone: gone.map((o) => o.id), touched: objects.filter((o) => touched[i].has(o.id) && live.has(o.id)).map((o) => o.id) });
  });
  return steps;
}

const clip = (text, max) => (text.length > max ? `${text.slice(0, max - 1)}…` : text);
export const SAY_MAX = 176;

/**
 * The say line: the innermost open call as the test wrote it (or the call that
 * just returned, so the line does not blink between calls), then the latest
 * observation.
 */
export function sayText(s) {
  const call = s.calls.at(-1)?.text ?? s.last;
  return clip([call, s.obs].filter(Boolean).join(" · "), SAY_MAX);
}

/** Until the report event, the line the figure composes from trace facts. */
export function reportText(s) {
  return s.report ?? `kubeharness cluster=${s.cluster} phase=${s.phase}${s.profile ? ` profile=${s.profile}` : ""}`;
}

const view = (s, x) => ({ checks: x.checks.map((c) => s.checks[c.id] ?? "wait"), verdict: s.verdict, stopped: s.stopped, lamp: s.lamp, phase: s.phase, cart: s.cart, show: s.show, gate: s.gate, lever: s.lever, ready: s.ready, mcp: s.mcp, display: s.display, model: s.model, say: sayText(s), report: reportText(s), plate: s.cluster });

/**
 * Fold the trace and cut a step wherever the drawn state, the say line or the
 * report changes; events in one millisecond share a step unless the lamp
 * changes between them. Steps carry `at` and `gap` in real seconds (`gap` runs to the
 * next step; the last step's is `tail`); each pulse's `delay` is its request's
 * offset inside the step, and `present`, `gone` and `touched` list the ids of
 * `objectsOf` for the bench's name panel. `timed` turns gaps into holds.
 */
export function stepsFromTrace(events, facts, { tail = 1.2 } = {}) {
  const checks = checksOf(events);
  const x = { facts, ex: exchanges(events, facts), calls: callsOf(events), senders: new Set(events.filter((e) => e.ev === "request").map((e) => e.in)), checks, checkIds: new Set(checks.map((c) => c.id)) };
  const objects = objectsOf(events, facts);
  const touched = [];
  let s = { lamp: 0, phase: "new", cart: 1, show: false, gate: !facts.gate, lever: "none", ready: { a: false, b: false }, mcp: false, display: "", model: facts.models ? -1 : undefined, calls: [], last: "", obs: "", report: null, cluster: "", profile: "", checks: {}, verdict: null, stopped: -1 };
  const steps = [];
  for (const e of events) {
    const before = s;
    s = ON[e.ev](s, e, x);
    const at = e.at / 1000;
    const v = view(s, x);
    const last = steps.at(-1);
    if (!last || Object.keys(v).some((k) => JSON.stringify(last[k]) !== JSON.stringify(v[k]))) {
      if (last?.at === at && last.lamp === v.lamp) Object.assign(last, v);
      else {
        steps.push({ at, gap: 0, ...v, pulses: [] });
        touched.push(new Set());
      }
    }
    for (const id of touchedBy(e, before, s, x, objects)) touched.at(-1).add(id);
    if (s.pulse) {
      const step = steps.at(-1);
      step.pulses.push({ to: s.pulse, delay: +(at - step.at).toFixed(3) });
      s = { ...s, pulse: undefined };
    }
  }
  steps.forEach((step, i) => (step.gap = i + 1 < steps.length ? +(steps[i + 1].at - step.at).toFixed(3) : tail));
  return annotate(steps, objects, touched);
}

/**
 * The test body in trace ms: from the return of Gateway, when the test first
 * holds a reachable gateway and starts sending through it, to the call of
 * Cleanup. A trace without a Gateway call starts its body where New returns.
 */
export function bodyOf(events) {
  const returnOf = (fn) => {
    const id = events.find((e) => e.ev === "call" && e.fn === fn)?.id;
    return events.find((e) => e.ev === "return" && e.id === id)?.at;
  };
  const cleanup = events.find((e) => e.ev === "call" && e.fn === "Cleanup")?.at;
  const from = returnOf("Gateway") ?? returnOf("New");
  if (from === undefined || cleanup === undefined) throw new Error("trace has no New return or no Cleanup call, so it has no test body");
  return { from, to: cleanup };
}

// timeMap and timed run in Node (build, checks) and are also inlined into the
// page, which applies ?body, ?hold and ?setup, so they use nothing outside themselves.

/**
 * The time map: segments `{from, to, rate, minHold, phaseHold, cap}` in trace ms.
 * Setup plays `setup` times faster and holds a step that lights a new lamp at
 * least `phaseHold` seconds, so every lifecycle phase stays visible; at
 * `setup` 1 it plays at real time with no phase hold. The body plays `rate`
 * times slower, holds each step at least `minHold` seconds and at most
 * `maxHold`, so a wait that ran to its timeout does not play for minutes, and, with a `cap`,
 * draws at most `cap` pulses a second. Cleanup plays at real time.
 */
export function timeMap(body, { rate = 20, minHold = 1.2, maxHold = 12, cap, setup = 10, phaseHold = 0.6 } = {}) {
  return [
    { from: 0, to: body.from, rate: 1 / setup, minHold: 0, phaseHold: setup === 1 ? 0 : phaseHold },
    { from: body.from, to: body.to, rate, minHold, maxHold, cap },
    { from: body.to, to: Infinity, rate: 1, minHold: 0 },
  ];
}

/**
 * Each step's hold, from the segment its start falls in: max(gap × rate,
 * minHold), with a step longer than maxHold played at the slower rate that
 * fits it in maxHold, and at least the segment's phaseHold when the step's lamp differs
 * from the previous step's. Pulse delays scale by the same rate; under a cap a
 * step drops each pulse that comes sooner than 1/cap seconds after the last
 * one it kept.
 */
export function timed(steps, map) {
  return steps.map((step, i) => {
    const ms = Math.round(step.at * 1000);
    const seg = map.find((m) => ms >= m.from && ms < m.to);
    const rate = seg.maxHold && step.gap * seg.rate > seg.maxHold ? seg.maxHold / step.gap : seg.rate;
    const pulses = [];
    for (const p of step.pulses) {
      const delay = p.delay * rate;
      if (seg.cap && pulses.length && delay - pulses.at(-1).delay < 1 / seg.cap) continue;
      pulses.push({ ...p, delay });
    }
    const phaseHold = steps[i - 1]?.lamp !== step.lamp ? seg.phaseHold ?? 0 : 0;
    return { ...step, rate, hold: Math.max(step.gap * rate, seg.minHold, phaseHold), pulses };
  });
}
