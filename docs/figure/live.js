const D = __DATA__;
const M = D.model;
const stage = document.querySelector(".kh-stage");
const svg = stage.querySelector("svg");
const bench = svg.querySelector(".kh-bench");
const readout = document.querySelector("[data-readout]");
const hint = document.querySelector('[data-corner="hint"]');
const sayEl = document.querySelector("[data-say]");
const reportEl = document.querySelector("[data-report]");
const still = matchMedia("(prefers-reduced-motion: reduce)");
const params = new URLSearchParams(location.search);
const REPLAY = D.replay;
const SPEED = +params.get("speed") > 0 ? +params.get("speed") : 1;
const MAP = {
  ...(+params.get("body") > 0 ? { rate: +params.get("body") } : {}),
  ...(params.has("hold") && +params.get("hold") >= 0 ? { minHold: +params.get("hold") } : {}),
  ...(+params.get("setup") > 0 ? { setup: +params.get("setup") } : {}),
};

const $$ = (sel) => [...svg.querySelectorAll(sel)];
const lamps = $$("[data-lamp]").sort((a, b) => a.dataset.lamp - b.dataset.lamp);
const postLamp = { a: svg.querySelector('[data-post="postA"]'), b: svg.querySelector('[data-post="postB"]') };
const cartEls = $$('[data-piece="cart"]');
const gateEls = $$('[data-piece="gate"]');
const leverArm = svg.querySelector('[data-part="lever-arm"]');
const leverKnob = svg.querySelector('[data-part="lever-knob"]');
const display = svg.querySelector('[data-part="display"]');
const plate = svg.querySelector('[data-part="plate"]');
const mcp = svg.querySelector(".kh-mcp");
const gateBody = svg.querySelector('[data-kh="gate"]');
const divBody = svg.querySelector('[data-kh="div"]');
const tray = svg.querySelector('[data-kh="tray"]');
const pins = $$("[data-pin]");
const namedParts = $$("[data-named]");
const groups = [...document.querySelectorAll(".kh-groups")];
const checkLists = [...document.querySelectorAll(".kh-checks")];
// In the replay the bottom-right corner names the pinned versions; the
// running clock moves to the stage's data-clock for tools that read it.
if (REPLAY) {
  readout.innerHTML = '<span class="kh-ro-k">pinned</span><span data-pins></span><span class="kh-ro-k">cluster</span><span></span>';
  readout.lastChild.textContent = D.cluster;
  document.querySelector('[data-corner="title"]').insertAdjacentHTML("beforeend", '<span class="kh-verdict" data-verdict="RUNNING">RUNNING</span>');
  readout.insertAdjacentHTML("beforebegin", '<span class="iso-plate-corner" data-corner="elapsed" data-elapsed></span>');
}
const elapsedEl = document.querySelector("[data-elapsed]");
// Video time, not trace time: what a viewer of the replay or its MP4 has watched so far.
const clockOf = (seconds) => `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${(seconds % 60).toFixed(3).padStart(6, "0")}`;
const pinsEl = document.querySelector("[data-pins]");
const verdictEl = document.querySelector("[data-verdict]");
const UP = axisVector(D.P, [0, 0, 1]);

const chunks = {};
for (const el of $$("[data-route]")) (chunks[el.dataset.route] ??= []).push(el);
for (const list of Object.values(chunks)) list.sort((a, b) => a.dataset.chunk - b.dataset.chunk);
const gate = D.lines.find((l) => l.name === "b").gate;
const head = [...chunks.host, ...chunks.fwd, ...chunks.cart];
const PATHS = {
  a: [...head, ...chunks.a],
  b: [...head, ...chunks.b],
  gate: [...head, ...chunks.b.filter((el) => +el.dataset.from < gate[0])],
  none: head,
};
const PER_CHUNK = 0.045;

const TOUR = [
  ["gateway-api", "G01"],
  ["gateway-api", "G02"],
  ["gateway-api", "G03"],
  ["gateway-agentgateway", "G01"],
  ["gateway-agentgateway", "A01"],
  ["gateway-agentgateway", "G02"],
  ["gateway-agentgateway", "G03"],
];

const ctor = { "gateway-api": "EnvoyGateway()", "gateway-agentgateway": "AgentGateway()" };

const scenariosOf = (profile) => M.PROFILES[profile].scenarios.filter((s) => !REPLAY || REPLAY[profile]?.[s]);

function stepsFor(profile, scenario) {
  const run = REPLAY?.[profile][scenario];
  if (run) return timed(run.steps, timeMap(run.body, { ...run.map, ...MAP }));
  const sc = M.SCENARIOS[scenario];
  const out = [];
  for (const l of M.LIFECYCLE) {
    if (l.install) {
      out.push({ lamp: 4, phase: "ready", cart: 0, show: true, install: true, gate: sc.gate, lever: "none", hold: l.hold, say: `h.InstallGateway(kubeharness.${ctor[profile]}) · GatewayClass kubeharness Accepted · ${M.PROFILES[profile].pins}` });
    } else {
      out.push({ lamp: l.lamp, phase: l.phase, cart: 1, show: false, gate: sc.gate, lever: "none", hold: l.hold, say: l.phase === "new" ? "kubeharness.New(t)" : `phase=${l.phase}` });
    }
  }
  let lever = "none";
  let gateOpen = sc.gate;
  let shown = "";
  for (const s of sc.steps) {
    if (s.lever) lever = s.lever;
    if (s.gate !== undefined) gateOpen = s.gate;
    if (s.display) shown = s.display;
    const pulses = s.send ? Array.from({ length: s.burst ?? 1 }, (_, b) => ({ to: s.send, delay: b * 0.2 })) : [];
    out.push({ ...s, lamp: 5, phase: "ready", cart: 0, show: true, installed: true, gate: gateOpen, lever, display: shown, test: true, pulses });
  }
  out.push({ lamp: 6, phase: "ready", cart: 1, show: false, installed: true, gate: gateOpen, lever: "none", hold: 2.2, say: "t.Cleanup · cluster deleted", final: true });
  out.push({ lamp: -1, phase: "ready", cart: 1, show: false, installed: true, gate: gateOpen, lever: "none", hold: 1.2, say: "", final: true });
  return out;
}

function reportLine(profile, scenario, step) {
  const r = M.REPORTS[profile][scenario];
  const f = ["kubeharness"];
  if (r.cluster) f.push(`cluster=${r.cluster}`);
  f.push(`phase=${step.phase}`);
  if (!step.final) return f.join(" ") + (step.installed || step.install ? ` profile=${profile}` : "");
  f.push("cause=ok", "cleanup=succeeded", "remaining=0");
  if (r.create) f.push(`create=${r.create}`, `ready=${r.ready}`, `delete=${r.delete}`);
  f.push(`profile=${profile}`, `install=${r.install}`);
  return f.join(" ");
}

const firstProfile = REPLAY && !REPLAY["gateway-api"] ? Object.keys(REPLAY)[0] : "gateway-api";
const state = {
  profile: params.get("profile") in (REPLAY ?? M.PROFILES) ? params.get("profile") : firstProfile,
  scenario: params.get("scenario") in M.SCENARIOS ? params.get("scenario") : scenariosOf(firstProfile)[0],
  touring: !REPLAY && !params.has("profile") && !params.has("scenario") && !params.has("still"),
  tourIndex: 0,
  steps: [],
  index: 0,
  t: 0,
  frozen: params.has("still"),
  manual: params.has("manual"),
  cart: 1,
  cartOpacity: 0,
  gate: 0,
  lever: 0,
  pulses: [],
  flowAt: 0,
  flashes: new Map(),
};
if (state.scenario === "A01") state.profile = "gateway-agentgateway";
if (!scenariosOf(state.profile).includes(state.scenario)) state.scenario = scenariosOf(state.profile)[0];

function begin(profile, scenario) {
  state.profile = profile;
  state.scenario = scenario;
  state.steps = stepsFor(profile, scenario);
  let start = 0;
  state.starts = state.steps.map((s) => ((start += s.hold / SPEED), start - s.hold / SPEED));
  state.index = 0;
  state.t = 0;
  state.pulses = [];
  state.cart = 1;
  state.cartOpacity = 0;
  bench.setAttribute("data-profile", profile);
  for (const g of [...groups, ...checkLists]) g.toggleAttribute("data-on", g.dataset.run === `${profile} ${scenario}`);
  const list = groups.find((g) => g.hasAttribute("data-on"));
  const listed = list ? [...list.querySelectorAll(".kh-group")].map((g) => g.dataset.part) : [];
  tray.toggleAttribute("data-on", REPLAY ? listed.includes("tray") : scenario === "G02");
  for (const el of pins) {
    el.toggleAttribute("data-off", !listed.includes(el.dataset.pin));
    el.querySelector("text").textContent = String(listed.indexOf(el.dataset.pin) + 1);
  }
  stage.dataset.profile = profile;
  stage.dataset.scenario = scenario;
  stage.dataset.stepCount = String(state.steps.length);
  for (const b of document.querySelectorAll("[data-profile]")) if (b.tagName === "BUTTON") b.setAttribute("aria-pressed", String(b.dataset.profile === profile));
  for (const b of document.querySelectorAll("button[data-scenario]")) b.setAttribute("aria-pressed", String(b.dataset.scenario === scenario));
  document.querySelector("[data-tour]").setAttribute("aria-pressed", String(state.touring));
  if (REPLAY) {
    hint.textContent = REPLAY[profile][scenario].test;
    pinsEl.textContent = M.PROFILES[profile].pins;
    for (const b of document.querySelectorAll("button[data-profile]")) b.disabled = !REPLAY[b.dataset.profile];
    for (const b of document.querySelectorAll("button[data-scenario]")) b.disabled = !REPLAY[profile][b.dataset.scenario];
    document.querySelector("[data-tour]").disabled = true;
  }
  enter(0);
}

const rateText = (slow) => (slow === 1 ? "×1" : slow > 1 ? `slowed ×${+slow.toFixed(2)}` : `fast ×${+(1 / slow).toFixed(2)}`);
const readoutText = (s) => `${state.profile} · ${state.scenario} · ${s.lamp >= 0 ? M.LAMPS[s.lamp] : "off"}${REPLAY ? ` · t+${(s.at + Math.min((state.t * SPEED) / s.rate, s.gap)).toFixed(2)}s · ${rateText(s.rate / SPEED)}` : ""}`;

function enter(i, carry = 0) {
  state.index = i;
  state.t = carry;
  const s = state.steps[i];
  lamps.forEach((el, n) => {
    el.toggleAttribute("data-on", n === s.lamp);
    el.toggleAttribute("data-done", s.lamp < 0 || n < s.lamp);
    el.toggleAttribute("data-fail", n === s.stopped);
  });
  display.textContent = s.display ?? "";
  mcp.toggleAttribute("data-on", !!s.mcp);
  sayEl.textContent = s.say ?? "";
  reportEl.textContent = s.report ?? reportLine(state.profile, state.scenario, s);
  if (plate && s.plate) plate.textContent = s.plate;
  for (const k of ["a", "b"]) postLamp[k].toggleAttribute("data-ready", !!s.ready?.[k]);
  if (REPLAY) showNames(s);
  showClock(s);
  stage.dataset.step = String(i);
  stage.dataset.phase = s.lamp >= 0 ? M.LAMPS[s.lamp] : "off";
  stage.setAttribute("aria-description", s.say ?? "");
  for (const p of s.pulses ?? []) send(p.to, p.delay / SPEED - carry);
  state.flowAt = 0;
  if (state.frozen) settleNow();
}

function showClock(s) {
  const text = readoutText(s);
  if (REPLAY) {
    if (stage.dataset.clock !== text) stage.dataset.clock = text;
    const elapsed = clockOf(state.starts[state.index] + state.t);
    if (elapsedEl.textContent !== elapsed) elapsedEl.textContent = elapsed;
  } else if (readout.textContent !== text) readout.textContent = text;
}

function showNames(s) {
  const checks = checkLists.find((g) => g.hasAttribute("data-on"));
  checks?.querySelectorAll("[data-check]").forEach((row, n) => (row.dataset.s = s.checks[n]));
  const verdict = s.verdict ?? "RUNNING";
  verdictEl.dataset.verdict = verdictEl.textContent = verdict;
  const list = groups.find((g) => g.hasAttribute("data-on"));
  const present = new Set(s.present);
  const gone = new Set(s.gone);
  const touched = new Set(s.touched);
  const lit = new Set(REPLAY[state.profile][state.scenario].objects.filter((o) => touched.has(o.id)).map((o) => o.part));
  for (const row of list.querySelectorAll("[data-obj]")) {
    const id = row.dataset.obj;
    row.dataset.state = gone.has(id) ? "gone" : present.has(id) ? "present" : "absent";
    row.toggleAttribute("data-touched", touched.has(id));
  }
  for (const g of list.querySelectorAll(".kh-group")) g.toggleAttribute("data-on", lit.has(g.dataset.part));
  for (const el of pins) el.toggleAttribute("data-on", lit.has(el.dataset.pin));
  for (const el of namedParts) el.toggleAttribute("data-touched", lit.has(el.dataset.named));
}

// A replayed request answers in milliseconds and the route can change half a
// second later, so a replay pulse crosses the bench in a quarter second.
const perChunk = REPLAY ? 0.25 / PATHS.b.length : PER_CHUNK;
function send(to, delay = 0) {
  const list = PATHS[to];
  state.pulses.push({ to, list, t: -delay, seconds: (list.length * perChunk) / SPEED, done: false });
}

function flash(el, seconds = 0.6) {
  state.flashes.set(el, seconds);
}

function settleNow() {
  const s = state.steps[state.index];
  state.cart = s.cart;
  state.cartOpacity = s.show ? 1 : 0;
  state.gate = s.gate ? 1 : 0;
  state.lever = D.lever.angles[s.lever];
  const to = s.pulses?.[0]?.to ?? s.flow;
  state.pulses = [];
  for (const list of Object.values(chunks)) for (const el of list) el.toggleAttribute("data-hot", false);
  if (to) for (const el of PATHS[to]) el.toggleAttribute("data-hot", true);
  const lamp = to === "a" || to === "b" ? postLamp[to] : null;
  postLamp.a.toggleAttribute("data-on", lamp === postLamp.a);
  postLamp.b.toggleAttribute("data-on", lamp === postLamp.b);
  gateBody.toggleAttribute("data-block", to === "gate");
  divBody.toggleAttribute("data-block", to === "none");
  draw();
}

function draw() {
  const lift = state.cart * D.LIFT;
  const tr = translateAlong(D.P, [0, 0, lift]);
  for (const el of cartEls) {
    el.setAttribute("transform", tr);
    el.style.opacity = state.cartOpacity.toFixed(3);
  }
  const rise = state.gate * D.STROKE;
  const dx = UP[0] * rise;
  const dy = UP[1] * rise;
  for (const el of gateEls) {
    el.setAttribute("transform", `translate(${dx.toFixed(2)} ${dy.toFixed(2)})`);
    holdStill(el, [dx, dy]);
  }
  const t = (state.lever * Math.PI) / 180;
  leverArm.setAttribute("d", lineOnTop([0, 0], [Math.cos(t) * D.lever.r, Math.sin(t) * D.lever.r], D.lever.z, D.P));
  leverKnob.setAttribute("transform", translateAlong(D.P, [Math.cos(t) * D.lever.r - D.lever.r, Math.sin(t) * D.lever.r, 0]));
}

const follow = (v, target, dt, tau) => (still.matches ? target : v + (target - v) * (1 - Math.exp(-dt / tau)));

function update(dt) {
  const s = state.steps[state.index];
  state.t += dt;
  if (s.show) {
    state.cartOpacity = follow(state.cartOpacity, 1, dt, 0.12);
    if (state.cartOpacity > 0.9 || !s.install) state.cart = follow(state.cart, s.cart, dt, 0.32);
  } else {
    state.cart = follow(state.cart, s.cart, dt, 0.32);
    if (state.cart > 0.8 || s.lamp < 6) state.cartOpacity = follow(state.cartOpacity, 0, dt, 0.14);
  }
  if (Math.abs(state.cart - s.cart) < 1e-3) state.cart = s.cart;
  state.gate = follow(state.gate, s.gate ? 1 : 0, dt, 0.25);
  state.lever = follow(state.lever, D.lever.angles[s.lever], dt, 0.18);
  if (s.flow) {
    state.flowAt -= dt;
    if (state.flowAt <= 0) {
      send(s.flow);
      state.flowAt = 0.28;
    }
  }
  const hot = new Set();
  state.pulses = state.pulses.filter((p) => {
    p.t += dt;
    if (p.t < 0) return true;
    const pos = (p.t / p.seconds) * p.list.length;
    p.list.forEach((el, i) => Math.abs(i - pos + 0.5) < 1.2 && hot.add(el));
    if (!p.done && pos >= p.list.length) {
      p.done = true;
      if (p.to === "a" || p.to === "b") flash(postLamp[p.to], 0.5);
      if (p.to === "gate") flash(gateBody, 0.5);
      if (p.to === "none") flash(divBody, 0.5);
    }
    return pos < p.list.length + 1.5;
  });
  for (const list of Object.values(chunks)) for (const el of list) el.toggleAttribute("data-hot", hot.has(el));
  for (const [el, left] of state.flashes) {
    const next = left - dt;
    if (next <= 0) state.flashes.delete(el);
    else state.flashes.set(el, next);
  }
  for (const el of [postLamp.a, postLamp.b]) el.toggleAttribute("data-on", state.flashes.has(el));
  gateBody.toggleAttribute("data-block", state.flashes.has(gateBody));
  divBody.toggleAttribute("data-block", state.flashes.has(divBody));
  draw();
  while (state.t >= state.steps[state.index].hold / SPEED) {
    const carry = state.t - state.steps[state.index].hold / SPEED;
    if (state.index + 1 >= state.steps.length) {
      next();
      break;
    }
    enter(state.index + 1, carry);
  }
  if (REPLAY) showClock(state.steps[state.index]);
}

function next() {
  if (state.touring) {
    state.tourIndex = (state.tourIndex + 1) % TOUR.length;
    begin(...TOUR[state.tourIndex]);
  } else begin(state.profile, state.scenario);
}

let frame = 0;
let last = 0;
let visible = false;
function tick(now) {
  const dt = last ? Math.min((now - last) / 1000, 1 / 20) : 1 / 60;
  last = now;
  update(dt);
  frame = visible && !state.frozen && !state.manual ? requestAnimationFrame(tick) : 0;
}
const run = () => {
  if (!frame && visible && !state.frozen && !state.manual) {
    last = 0;
    frame = requestAnimationFrame(tick);
  }
};
new IntersectionObserver((entries) => {
  visible = entries[entries.length - 1].isIntersecting;
  if (visible) run();
}, { rootMargin: "120px 0px" }).observe(stage);

function select(profile, scenario, { step } = {}) {
  if (scenario === "A01") profile = "gateway-agentgateway";
  if (!scenariosOf(profile).includes(scenario)) scenario = scenariosOf(profile)[0];
  state.touring = false;
  begin(profile, scenario);
  if (step !== undefined) {
    state.frozen = true;
    enter(Math.max(0, Math.min(state.steps.length - 1, step)));
  }
}

document.querySelectorAll("button[data-profile]").forEach((b) => b.addEventListener("click", () => { state.frozen = false; select(b.dataset.profile, state.scenario); run(); }));
document.querySelectorAll("button[data-scenario]").forEach((b) => b.addEventListener("click", () => { state.frozen = false; select(state.profile, b.dataset.scenario); run(); }));
document.querySelector("[data-tour]").addEventListener("click", () => {
  state.frozen = false;
  state.touring = true;
  state.tourIndex = 0;
  begin(...TOUR[0]);
  run();
});
stage.addEventListener("keydown", (event) => {
  const keys = REPLAY ? scenariosOf(state.profile) : Object.keys(M.SCENARIOS);
  const i = keys.indexOf(state.scenario);
  let to = null;
  if (event.key === "ArrowRight") to = keys[(i + 1) % keys.length];
  if (event.key === "ArrowLeft") to = keys[(i + keys.length - 1) % keys.length];
  if (event.key === "p") {
    const other = state.profile === "gateway-api" ? "gateway-agentgateway" : "gateway-api";
    if (REPLAY && !REPLAY[other]) return;
    state.frozen = false;
    select(other, state.scenario);
    run();
    return;
  }
  if (to === null) return;
  event.preventDefault();
  state.frozen = false;
  select(state.profile, to);
  run();
});

window.kubeharnessFigure = {
  select: (profile, scenario, options) => { select(profile, scenario, options); run(); },
  tour: () => document.querySelector("[data-tour]").click(),
  // With ?manual the page has no animation loop; advance() moves the
  // timeline by exactly `seconds`, so recordings do not depend on capture speed.
  advance: (seconds, step = 1 / 60) => {
    state.frozen = false;
    for (let left = seconds; left > 1e-9; left -= step) update(Math.min(step, left));
    return window.kubeharnessFigure.state();
  },
  runSeconds: () => state.steps.reduce((sum, s) => sum + s.hold, 0) / SPEED,
  steps: () => state.steps.map((s, i) => ({ index: i, lamp: s.lamp >= 0 ? M.LAMPS[s.lamp] : "off", say: s.say ?? "", gate: !!s.gate, lever: s.lever })),
  state: () => ({ profile: state.profile, scenario: state.scenario, step: state.index, phase: stage.dataset.phase, say: sayEl.textContent, report: reportEl.textContent, lever: state.steps[state.index].lever, touring: state.touring }),
};

if (still.matches) state.touring = false;
begin(state.profile, state.scenario);
if (params.has("step")) select(state.profile, state.scenario, { step: +params.get("step") });
else if (state.frozen) settleNow();
draw();
