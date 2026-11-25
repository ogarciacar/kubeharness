// node tools/replay-check.mjs [traces-dir]: checks replay.mjs on a real
// sequence inlined from an earlier recorded agentgateway G02 trace (cluster kh-25c6b51a9caa-2,
// at 38234 to 38518 ms), where the proxy still served payments after the route
// reported ResolvedRefs=False, then every recorded trace in traces-dir (default
// docs/figure/traces), then drives the built replay page in headless Chrome
// to read the model lamp keys at every step.
// Prints "replay check passed" or exits 1.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { answerOf, bodyOf, checksOf, describe, groupsOf, modelIn, objectsOf, parseTrace, stepsFromTrace, stoppedAt, timeMap, timed, verdictOf } from "../replay.mjs";
import { BACKENDS, LAMPS, PROFILES, REPLAY, SCENARIOS } from "../model.mjs";
import { benchWordsIn } from "./bench-words.mjs";
import { answersOf } from "./k01-route.mjs";

const fig = new URL("..", import.meta.url).pathname;
const dir = resolve(fig, process.argv[2] ?? "traces");

const body = (backend) => JSON.stringify(`{"backend":"${backend}","path":"/${backend}","host":"shop.example"}\n`);
const burst = (from, at, status) =>
  [0, 1, 2, 3, 4].flatMap((i) => [
    `{"at":${at + i},"ev":"request","id":${from + i},"via":"client","method":"GET","url":"http://shop.example/payments"}`,
    `{"at":${at + i + 1},"ev":"response","id":${from + i},"status":${status},"ms":0}`,
  ]);
const trace = [
  `{"at":0,"ev":"trace","v":1,"test":"TestG02ReferenceGrant/gateway-agentgateway","cluster":"kh-25c6b51a9caa-2","start":"2026-11-20T16:43:21.114Z"}`,
  `{"at":0,"ev":"call","id":1,"fn":"New"}`,
  `{"at":10,"ev":"return","id":1}`,
  `{"at":10,"ev":"call","id":2,"fn":"InstallGateway","arg":"gateway-agentgateway"}`,
  `{"at":20,"ev":"return","id":2}`,
  `{"at":35644,"ev":"call","id":9,"fn":"Gateway","arg":"shop/edge"}`,
  `{"at":37695,"ev":"return","id":9}`,
  `{"at":38234,"ev":"wait","in":20,"on":"route","subject":"shop/pay","state":"Accepted=True ResolvedRefs=True","done":true}`,
  `{"at":38235,"ev":"request","id":22,"in":21,"via":"await","method":"GET","url":"http://shop.example/payments"}`,
  `{"at":38238,"ev":"response","id":22,"status":200,"ms":3}`,
  `{"at":38238,"ev":"body","id":22,"head":${body("payments")}}`,
  `{"at":38239,"ev":"call","id":23,"fn":"Delete","arg":"testdata/g02-grant"}`,
  `{"at":38246,"ev":"object","in":23,"obj":"ReferenceGrant/payments/payments","state":"gone"}`,
  `{"at":38246,"ev":"return","id":23}`,
  `{"at":38246,"ev":"call","id":24,"fn":"AwaitRoute","arg":"shop/pay"}`,
  `{"at":38248,"ev":"wait","in":24,"on":"route","subject":"shop/pay","state":"Accepted=True ResolvedRefs=False reason=RefNotPermitted: backendRef payments/payments not accessible","done":true}`,
  `{"at":38248,"ev":"return","id":24}`,
  `{"at":38248,"ev":"call","id":25,"fn":"AwaitResponse","arg":"GET http://shop.example/orders"}`,
  `{"at":38248,"ev":"request","id":26,"in":25,"via":"await","method":"GET","url":"http://shop.example/orders"}`,
  `{"at":38250,"ev":"response","id":26,"status":200,"ms":2}`,
  `{"at":38250,"ev":"body","id":26,"head":${body("orders")}}`,
  `{"at":38250,"ev":"return","id":25}`,
  ...burst(27, 38251, 200),
  ...burst(32, 38509, 500),
  `{"at":38518,"ev":"call","id":37,"fn":"Cleanup","arg":"ok"}`,
  `{"at":39072,"ev":"report","in":37,"line":"kubeharness cluster=kh-25c6b51a9caa-2 phase=ready cause=ok"}`,
  `{"at":39072,"ev":"return","id":37}`,
].join("\n");

const events = parseTrace(trace, "agentgateway excerpt");
assert.ok(objectsOf(events, REPLAY.G02).every((o) => !("api" in o)), "a trace from before the api field leaves every object without one");
const steps = stepsFromTrace(events, REPLAY.G02);
const pulsed = steps.filter((s) => s.pulses.length);
const [allowed, orders, lagging, blocked] = pulsed;

assert.equal(pulsed.length, 4, "four pulse steps: allowed, orders, the lagging burst, the blocked burst");
assert.deepEqual(allowed.pulses.map((p) => p.to), ["b"], "a 200 from payments with the valve open reaches post b");
assert.equal(allowed.gate, true);
assert.deepEqual(orders.pulses.map((p) => p.to), ["a"], "orders reaches post a");
assert.equal(lagging.gate, false, "the valve has closed on ResolvedRefs=False");
assert.deepEqual(lagging.pulses.map((p) => p.to), ["b", "b", "b", "b", "b"], "the five bodyless 200s still reach post b");
assert.deepEqual(lagging.pulses.map((p) => p.delay), [0, 0.001, 0.002, 0.003, 0.004], "the burst keeps its recorded spacing");
assert.equal(lagging.say, "GET http://shop.example/payments → 200 from payments ×5 · proxy still routing after ResolvedRefs=False");
assert.equal(lagging.at, 38.251);
assert.deepEqual(blocked.pulses.map((p) => p.to), ["gate", "gate", "gate", "gate", "gate"], "the next burst of 500s stops at the valve");
assert.equal(blocked.say, "GET http://shop.example/payments → 500 ×5");
assert.equal(+(blocked.at - lagging.at).toFixed(3), 0.258, "250 ms of real time between the bursts");
assert.equal(steps.at(-1).report, "kubeharness cluster=kh-25c6b51a9caa-2 phase=ready cause=ok", "the report line is verbatim");

const span = bodyOf(events);
assert.deepEqual(span, { from: 37695, to: 38518 }, "the body runs from Gateway's return to Cleanup's call");
const play = timed(steps, timeMap(span));
const inSpan = (s) => s.at * 1000 >= span.from && s.at * 1000 < span.to;
for (const s of play) {
  if (inSpan(s)) assert.equal(s.hold, Math.max(s.gap * 20, 1.2), `body step at ${s.at} holds max(gap × 20, 1.2 s)`);
  else if (s.at * 1000 < span.from) assert.equal(s.hold, Math.max(s.gap * 0.1, s.lamp !== play[play.indexOf(s) - 1]?.lamp ? 0.6 : 0), `setup step at ${s.at} holds gap × 0.1, at least 0.6 s when it lights a new lamp`);
  else assert.equal(s.hold, s.gap, `cleanup step at ${s.at} holds its real gap`);
}
const lagPlayed = play.find((s) => s.at === lagging.at);
assert.deepEqual(lagPlayed.pulses.map((p) => p.to), ["b", "b", "b", "b", "b"], "slowed, the five lagging 200s still reach post b");
assert.deepEqual(lagPlayed.pulses.map((p) => +p.delay.toFixed(3)), [0, 0.02, 0.04, 0.06, 0.08], "pulse delays scale by the body rate");
assert.equal(lagPlayed.hold, 0.258 * 20, "the 258 ms gap before the blocked burst plays 20 times slower");
const custom = timed(steps, timeMap(span, { rate: 5, minHold: 0 }));
assert.equal(custom.find((s) => s.at === lagging.at).hold, 0.258 * 5, "?body=5&hold=0 plays the body 5 times slower with no minimum");

const runs = readdirSync(dir).filter((f) => f.endsWith(".jsonl")).sort().map((name) => {
  const text = readFileSync(`${dir}/${name}`, "utf8");
  const events = parseTrace(text, name);
  const run = describe(events, name);
  const facts = REPLAY[run.scenario];
  return { ...run, name, text, events, lines: text.trim().split("\n").map((l) => JSON.parse(l)), facts, objects: objectsOf(events, facts), steps: stepsFromTrace(events, facts) };
});
const of = (scenario) => runs.filter((r) => r.scenario === scenario);
assert.deepEqual(of("G02").map((r) => r.profile).sort(), ["gateway-agentgateway", "gateway-api"], "a G02 trace per profile");

const named = (o) => `${o.kind} ${o.name}`;
const headsOf = (r) => groupsOf(r.objects, r.facts).map((g) => g.head && named(g.head));
const rowsOn = (r, part) => r.objects.filter((o) => o.part === part).map(named).sort();
const inBody = (r, s) => s.at * 1000 >= bodyOf(r.events).from && s.at * 1000 < bodyOf(r.events).to;
const podsOf = (r, part) => [...new Set(r.lines.filter((e) => e.ev === "pod" && e.ready && r.facts.owners[e.owner.split("/").slice(-2).join("/")] === part).map((e) => `Pod ${e.owner.split("/")[1]}/${e.pod}`))];
const pulsesOf = (steps) => steps.flatMap((s) => s.pulses.map((p) => p.to));

for (const [scenario, sc] of Object.entries(SCENARIOS)) if (BACKENDS[scenario].b == null) assert.ok(sc.steps.every((s) => s.send !== "b"), `${scenario}: the scripted body sends nothing to the hidden post b`);
for (const [scenario, sc] of Object.entries(SCENARIOS)) {
  const models = REPLAY[scenario]?.models;
  if (!models) continue;
  const ids = Object.keys(models);
  sc.steps.forEach((s, i) => {
    assert.ok(Number.isInteger(s.model) && s.model >= -1 && s.model < ids.length, `${scenario}: scripted step ${i}'s lamp slot ${s.model} is -1 or one of ${ids.length} lamps`);
    const id = ids[s.model];
    assert.equal(s.display, id ? models[id].tag : "", `${scenario}: scripted step ${i} displays ${id ? `${id}'s tag` : "nothing"}`);
    assert.deepEqual(ids.filter((k) => s.say.includes(k)), id ? [id] : [], `${scenario}: scripted step ${i}'s say line names ${id ?? "no model"}`);
  });
}
for (const r of runs) if (BACKENDS[r.scenario].b == null) assert.ok(!pulsesOf(r.steps).includes("b"), `${r.name}: no pulse travels to the hidden post b`);

for (const r of runs) {
  const span = bodyOf(r.events);
  const want = r.facts.body;
  const play = timed(r.steps, timeMap(span, want));
  const mid = play.filter((s) => inBody(r, s));
  assert.ok(mid.length > 3 && mid.every((s) => s.hold >= want.minHold && s.rate === want.rate), `${r.name}: every body step plays at ×${want.rate} and holds at least ${want.minHold} s`);
  const from = bodyOf(r.events).from;
  play.forEach((s, i) => {
    if (s.at * 1000 >= from) return;
    const lit = i === 0 || s.lamp !== play[i - 1].lamp;
    assert.ok(s.rate === 0.1 && s.hold === (lit ? Math.max(s.gap * 0.1, 0.6) : s.gap * 0.1), `${r.name}: setup step ${i} at ${s.at} plays ×10 fast and holds ${lit ? "max(gap × 0.1, 0.6 s), a new lamp" : "gap × 0.1"}, got ${s.hold}`);
    assert.deepEqual(s.pulses.map((p) => p.delay), r.steps[i].pulses.map((p) => p.delay * 0.1), `${r.name}: setup step ${i}'s pulse delays scale by 0.1`);
  });
  assert.deepEqual([...new Set(play.filter((s) => s.at * 1000 < from).map((s) => s.lamp))], [0, 1, 2, 3, 4, 5], `${r.name}: setup lights new, validating, provisioning, wait-usable, ready and test in order`);
  for (const lamp of new Set(play.filter((s) => s.at * 1000 < from).map((s) => s.lamp))) assert.ok(play.filter((s) => s.lamp === lamp && s.at * 1000 < from).reduce((a, s) => a + s.hold, 0) >= 0.6, `${r.name}: lamp ${lamp} shows at least 0.6 s in setup`);
  const after = play.filter((s) => s.at * 1000 >= from);
  assert.deepEqual(after.map((s) => s.hold), after.map((s) => (mid.includes(s) ? Math.max(s.gap * want.rate, want.minHold) : s.gap)), `${r.name}: body and cleanup holds are unchanged`);
  assert.ok(after.filter((s) => !mid.includes(s)).every((s) => s.rate === 1), `${r.name}: cleanup plays at ×1`);
  const real = timed(r.steps, timeMap(span, { ...want, setup: 1 }));
  assert.ok(real.filter((s) => s.at * 1000 < from).every((s) => s.hold === s.gap && s.rate === 1), `${r.name}: ?setup=1 plays setup at real time with no phase hold`);
  assert.deepEqual(real.filter((s) => s.at * 1000 >= from), after, `${r.name}: ?setup=1 leaves body and cleanup as they are`);
  for (const s of play) for (let i = 1; want.cap && i < s.pulses.length; i++) assert.ok(s.pulses[i].delay - s.pulses[i - 1].delay >= 1 / want.cap - 1e-9, `${r.name}: step at ${s.at} draws at most ${want.cap} pulses a second`);
  assert.equal(r.steps.at(-1).report, r.lines.find((e) => e.ev === "report").line, `${r.name}: the report line is verbatim`);
  assert.equal(r.steps.at(-1).plate, r.cluster, `${r.name}: the plate carries the trace's cluster`);

  assert.ok(r.objects.every((o) => o.part in r.facts.kinds), `${r.name}: every object stands on a part the scenario lists`);
  for (const step of r.steps) for (const id of [...step.present, ...step.gone, ...step.touched]) assert.ok(r.objects.some((o) => o.id === id), `${r.name}: step at ${step.at} names ${id}, which is not in the registry`);
  assert.ok(!r.objects.some((o) => /kube-system|^Deployment|^CustomResourceDefinition|Role/.test(`${o.kind}/${o.name}`)), `${r.name}: no kube-system, Deployment, CRD or RBAC rows`);
  const install = r.lines.filter((e) => e.ev === "object" && e.in === r.lines.find((c) => c.ev === "call" && c.fn === "InstallGateway").id);
  assert.equal(r.objects.find((o) => o.id === "install").name, `+${new Set(install.map((e) => e.obj)).size} install objects`, `${r.name}: the install count is the install call's objects`);
  for (const part of ["a", "b"].filter((p) => p in r.facts.kinds)) {
    const pods = podsOf(r, part);
    assert.ok(pods.length > 0, `${r.name}: a pod line names the Pod on post ${part}`);
    for (const pod of pods) assert.ok(rowsOn(r, part).includes(pod), `${r.name}: ${pod} is a row on post ${part}`);
  }
  const cartCalls = new Set(r.lines.filter((e) => e.ev === "call" && (e.fn === "InstallGateway" || e.fn === "Gateway")).map((e) => e.id));
  const cartLines = r.lines.filter((e) => cartCalls.has(e.in));
  const cart = [`install +${new Set(install.map((e) => e.obj)).size} install objects`, `Gateway ${Object.keys(r.facts.parts).find((k) => r.facts.parts[k] === "cart").split("/").slice(1).join("/")}`, ...cartLines.filter((e) => e.ev === "pod" && e.ready && !r.facts.owners[e.owner.split("/").slice(-2).join("/")]).map((e) => `Pod ${e.owner.split("/")[1]}/${e.pod}`), ...cartLines.filter((e) => e.ev === "forward").map((e) => `Service ${e.svc}`)];
  assert.deepEqual(rowsOn(r, "cart"), [...new Set(cart)].sort(), `${r.name}: the names on the Gateway's part, read off its lines`);
  const groups = groupsOf(r.objects, r.facts);
  assert.deepEqual(groups.map((g) => g.part), Object.keys(r.facts.kinds), `${r.name}: one group per part the scenario lists, in pin order`);
  for (const g of groups) assert.ok(g.head && !g.rows.includes(g.head) && g.head.kind === r.facts.kinds[g.part], `${r.name}: ${g.part} is headed by its ${g.kind}, which is not one of its rows`);

  for (const o of r.objects.filter((o) => o.kind !== "install")) {
    const tail = o.name.split("/").at(-1);
    const mine = r.lines.filter((e) => (e.ev === "object" && e.obj === o.id) || (e.ev === "pod" && o.kind === "Pod" && e.pod === tail && e.ready) || (e.ev === "forward" && ((o.kind === "Service" && e.svc === o.name) || (o.kind === "Pod" && e.pod === tail))));
    const from = mine[0].at / 1000;
    const to = mine.find((e) => e.state === "gone")?.at / 1000;
    r.steps.forEach((step, i) => {
      const end = i + 1 < r.steps.length ? r.steps[i + 1].at : Infinity;
      const want = end <= from ? "absent" : to !== undefined && to < end ? "gone" : "present";
      const got = step.gone.includes(o.id) ? "gone" : step.present.includes(o.id) ? "present" : "absent";
      assert.equal(got, want, `${r.name}: ${o.id} at step ${i} (${step.at}s)`);
    });
  }
  for (const o of r.objects) {
    const tail = o.name.split("/").at(-1);
    const apis = new Set(r.lines.filter((e) => e.api && ((e.ev === "object" && e.obj === o.id) || (e.ev === "pod" && o.kind === "Pod" && e.pod === tail))).map((e) => e.api));
    assert.ok(apis.size <= 1, `${r.name}: ${o.id} has one api across its lines, got ${[...apis].join(", ")}`);
    assert.equal(o.api, o.kind === "install" ? undefined : [...apis][0], `${r.name}: ${o.id} carries the api its lines carry`);
  }
  assert.ok(r.objects.some((o) => o.api), `${r.name}: the trace carries api on its object and pod lines`);
  const requests = r.lines.filter((e) => e.ev === "request");
  assert.equal(pulsesOf(r.steps).length, requests.length, `${r.name}: one pulse per request`);
}

// G01: testdata/g01 in the default namespace, and g01Routes in gateway_test.go.
for (const r of of("G01")) {
  assert.deepEqual(headsOf(r), ["Gateway default/edge", "HTTPRoute default/shop", "Service default/orders", "Service default/payments"], `${r.name}: the four group headings`);
  assert.deepEqual(rowsOn(r, "div"), ["HTTPRoute default/payments", "HTTPRoute default/shop"], `${r.name}: both routes on the diverter`);
  const client = r.steps.filter((s) => inBody(r, s)).flatMap((s) => s.pulses.map((p) => ({ to: p.to, say: s.say }))).slice(-6);
  assert.deepEqual(client.map((p) => p.to), ["a", "a", "b", "b", "none", "none"], `${r.name}: orders, orders/42, payments, payments.example/refunds, unrouted, unknown host`);
  assert.ok(client.slice(0, 2).every((p) => p.say.includes("from orders")) && client.slice(2, 4).every((p) => p.say.includes("from payments")), `${r.name}: the say line names the backend that answered`);
  assert.ok(r.steps.every((s) => s.gate), `${r.name}: no ReferenceGrant, so the valve stays open`);
}

// G02: testdata/g02 and g02-grant.
for (const r of of("G02")) {
  for (const [part, list] of Object.entries({ div: ["Namespace shop", "HTTPRoute shop/pay"], gate: ["ReferenceGrant payments/payments"], tray: ["Namespace payments"], a: ["Service shop/orders", ...podsOf(r, "a")], b: ["Service payments/payments", ...podsOf(r, "b")] })) assert.deepEqual(rowsOn(r, part), [...list].sort(), `${r.name}: the names on part ${part}`);
  assert.deepEqual(headsOf(r), ["Gateway shop/edge", "HTTPRoute shop/pay", "ReferenceGrant payments/payments", "Service shop/orders", "Service payments/payments", "Namespace payments"], `${r.name}: the six group headings`);
  assert.equal(r.steps.map((s) => (s.gate ? "open" : "closed")).filter((g, i, all) => g !== all[i - 1]).join(" → "), "closed → open → closed", `${r.name}: the valve closes, opens and closes`);
  const grant = r.steps.find((s, i) => s.gate && !r.steps[i - 1].gate);
  for (const id of ["ReferenceGrant/payments/payments", "Service/payments/payments", `Pod/${podsOf(r, "b")[0].slice(4)}`]) assert.ok(grant.touched.includes(id), `${r.name}: the grant step at ${grant.at}s touches ${id}`);
  const del = r.steps.find((s) => s.gone.includes("ReferenceGrant/payments/payments"));
  assert.ok(del.touched.includes("ReferenceGrant/payments/payments") && del.say.length, `${r.name}: the delete step touches the grant as it goes`);
  assert.ok(r.steps.filter((s) => s.at > del.at).every((s) => !s.touched.includes("ReferenceGrant/payments/payments") && !s.present.includes("ReferenceGrant/payments/payments")), `${r.name}: nothing touches the grant once it is gone`);
  const denied = r.steps.find((s) => s.pulses.some((p) => p.to === "gate"));
  assert.ok(!denied.touched.some((id) => id.includes("payments-")), `${r.name}: a refused burst touches no payments Pod`);
  const mid = r.steps.filter((s) => inBody(r, s));
  assert.ok(mid.some((s) => s.say.includes("→ 500 ×5")) && mid.some((s) => s.say.startsWith("h.Apply(testdata/g02-grant)")) && mid.some((s) => s.say.startsWith("h.Delete(testdata/g02-grant)")), `${r.name}: the denied burst, the grant and the revoke are in the body`);
}

// G03: testdata/g03 in namespace shop; flips_test.go applies blue, green, blue under traffic every 50 ms.
for (const r of of("G03")) {
  assert.deepEqual(headsOf(r), ["Gateway shop/edge", "HTTPRoute shop/checkout", "Service shop/blue", "Service shop/green"], `${r.name}: the four group headings`);
  const levers = r.steps.map((s) => s.lever).filter((l, i, all) => l !== "none" && l !== all[i - 1]);
  assert.deepEqual(levers, ["a", "b", "a"], `${r.name}: the lever goes to blue, green, blue`);
  const applies = r.lines.filter((e) => e.ev === "call" && e.fn === "Apply" && e.arg?.startsWith("testdata/g03-route/")).map((e) => e.arg.split("/").at(-1));
  assert.deepEqual(applies, ["blue", "green", "blue"], `${r.name}: the trace applies blue, green, blue`);
  const appliedAt = new Set(r.lines.filter((e) => e.ev === "object" && e.obj === "HTTPRoute/shop/checkout" && e.state === "applied").map((e) => e.at / 1000));
  const moves = r.steps.filter((s, i) => i && s.lever !== r.steps[i - 1].lever && s.lever !== "none");
  assert.ok(moves.length === 3 && moves.every((s) => appliedAt.has(s.at)), `${r.name}: the lever turns when shop/checkout is applied (${[...appliedAt].join(", ")}), got ${moves.map((s) => s.at).join(", ")}`);
  const byId = new Map(r.lines.filter((e) => e.ev === "body").map((e) => [e.id, /"backend":"(\w+)"/.exec(e.head)?.[1]]));
  const status = new Map(r.lines.filter((e) => e.ev === "response").map((e) => [e.id, e.status]));
  const want = r.lines.filter((e) => e.ev === "request").map((e) => (status.get(e.id) === 200 && { blue: "a", green: "b" }[byId.get(e.id)]) || "none");
  assert.deepEqual(pulsesOf(r.steps), want, `${r.name}: every pulse reaches the post that answered`);
  const traffic = r.lines.filter((e) => e.ev === "request" && e.via === "client").length;
  assert.ok(traffic > 100, `${r.name}: the stream sent ${traffic} requests, more than 100 over three 3 s holds`);
  assert.ok(want.includes("a") && want.includes("b"), `${r.name}: both blue and green answered`);
  const play = timed(r.steps, timeMap(bodyOf(r.events), r.facts.body)).filter((s) => inBody(r, s));
  const seconds = play.reduce((a, s) => a + s.hold, 0);
  assert.ok(seconds < 60, `${r.name}: the body plays in ${seconds.toFixed(1)} s, under a minute`);
}

// A01: testdata/a01 and a01-backend/good in default; sumIs42 in a01_test.go.
for (const r of of("A01")) {
  assert.deepEqual(headsOf(r), ["Gateway default/edge", "HTTPRoute default/mcp", "AgentgatewayBackend default/calculator"], `${r.name}: the three group headings`);
  assert.deepEqual(rowsOn(r, "a"), ["AgentgatewayBackend default/calculator", "Service default/calculator", ...podsOf(r, "a")].sort(), `${r.name}: the backend, its Service and its Pod on post a`);
  const call = r.lines.find((e) => e.ev === "body" && answerOf(r.lines.find((q) => q.ev === "request" && q.id === e.id), e, r.facts.mcp)?.method === "tools/call");
  assert.ok(call && /"text"\s*:\s*"42"/.test(call.head), `${r.name}: a tools/call body head carries the text 42`);
  const shown = r.steps.findIndex((s) => s.display === "42");
  assert.ok(shown > 0 && r.steps[shown].say.includes("tools/call = 42"), `${r.name}: the display shows 42 on the tools/call step`);
  assert.ok(r.steps.slice(0, shown).every((s) => s.display === ""), `${r.name}: the display is blank until tools/call answers`);
  assert.ok(r.steps.some((s) => s.say.includes("tools/list = [") && s.say.includes("sum")), `${r.name}: tools/list names sum`);
  const mcpSteps = r.steps.filter((s) => s.pulses.length && /POST http:\/\/mcp\.example\/mcp/.test(s.say));
  assert.ok(mcpSteps.length >= 3 && mcpSteps.every((s) => s.mcp), `${r.name}: the MCP port is lit on every MCP request step`);
  assert.ok(!r.steps.at(-1).mcp && r.steps.filter((s) => !inBody(r, s)).every((s) => !s.mcp), `${r.name}: the MCP port is dark outside the body`);
  assert.ok(pulsesOf(r.steps.filter((s) => s.mcp)).every((to) => to === "a" || to === "none"), `${r.name}: MCP pulses reach the calculator or stop at the diverter`);
  assert.ok(r.steps.every((s) => s.gate), `${r.name}: no ReferenceGrant, so the valve stays open`);
}

// K01: testdata/k01 and k01-route/{good,crossed} in default; TestK01Routing in k01_test.go.
for (const r of of("K01")) {
  const models = r.facts.models;
  const ids = Object.keys(models);
  for (const id of ids) assert.ok(models[id].tag.length <= 5, `${r.name}: ${id}'s tag ${models[id].tag} fits the display window's five characters`);
  assert.deepEqual(headsOf(r), ["Gateway default/edge", "HTTPRoute default/llm", "Service default/kronk"], `${r.name}: the three group headings`);
  assert.deepEqual(rowsOn(r, "a"), ["AgentgatewayBackend default/gemma", "AgentgatewayBackend default/qwen", "AgentgatewayBackend default/smol", "Service default/kronk", ...podsOf(r, "a")].sort(), `${r.name}: the three backends, kronk's Service and its Pod on post a`);
  assert.equal(r.steps.at(-1).verdict, "PASS", `${r.name}: the recorded run passed (report cause=ok)`);
  const answers = answersOf(r.lines);
  for (const a of answers) assert.ok(a.id in models, `${r.name}: the answer to /m/${a.alias}/ names ${a.id}, which is not a key of REPLAY.K01.models`);
  for (const id of ids) assert.ok(answers.some((a) => a.id === id), `${r.name}: ${id} answers at least once`);
  for (const a of answers) assert.equal(models[a.id].backend, a.want, `${r.name}: /m/${a.alias}/ under ${a.dir} goes to backend ${a.want}, and ${a.id} answered`);
  assert.ok(answers.some((a) => a.want !== a.alias), `${r.name}: a request ran under a route that crosses its alias`);
  const pulsed = r.steps.filter((s) => s.pulses.length);
  for (const s of pulsed) {
    const id = ids.find((k) => s.say.includes(`from ${k}`));
    assert.equal(s.model, ids.indexOf(id), `${r.name}: step at ${s.at} lights the lamp of ${id ?? "no model"}`);
    assert.equal(s.display, id ? models[id].tag : "", `${r.name}: step at ${s.at} displays ${id ? models[id].tag : "nothing"}`);
    assert.deepEqual(s.pulses.map((p) => p.to), [id ? "a" : "none"], `${r.name}: step at ${s.at} sends its pulse to ${id ? "kronk" : "the diverter"}`);
  }
  assert.deepEqual([...new Set(pulsed.map((s) => s.model))].filter((m) => m >= 0).sort(), ids.map((_, i) => i), `${r.name}: every model lamp lights`);
  assert.ok(pulsed.some((s) => s.model === -1 && s.say.includes("/m/unknown/")), `${r.name}: /m/unknown/ lights no lamp`);
  assert.ok(r.steps.filter((s) => s.at * 1000 < bodyOf(r.events).from).every((s) => s.model === -1), `${r.name}: no lamp lights before the body`);
  assert.ok(r.steps.every((s) => Number.isInteger(s.model) && s.display === (s.model >= 0 ? models[ids[s.model]].tag : "")) && r.steps.slice(r.steps.findIndex((s) => s.lamp === 6)).every((s) => s.model === -1 && s.display === ""), `${r.name}: every step carries a lamp slot and the display shows its tag or nothing, and cleanup turns the lamps and the display off`);
  const senders = new Set(r.lines.filter((e) => e.ev === "request").map((e) => e.in));
  const bodyModel = new Map(r.lines.filter((e) => e.ev === "body").map((e) => [e.id, ids.indexOf(modelIn(e.head))]));
  const slots = r.lines.flatMap((e) => (e.ev === "request" ? [bodyModel.get(e.id) ?? -1] : e.ev === "call" && !senders.has(e.id) ? [-1] : []));
  const changes = (list) => list.filter((m, i) => m !== list[i - 1]);
  assert.deepEqual(changes(r.steps.map((s) => s.model)), changes([-1, ...slots]), `${r.name}: a lamp lights on its request and goes dark at the next call that sends no request`);
  const idle = r.steps.filter((s) => inBody(r, s) && /^h\.Apply\(|^gw\.AwaitRoute\(/.test(s.say));
  assert.ok(idle.length >= 4 && idle.every((s) => s.model === -1 && s.display === ""), `${r.name}: no lamp or tag shows while the test applies a route or waits for it`);
  const play = timed(r.steps, timeMap(bodyOf(r.events), r.facts.body)).filter((s) => inBody(r, s));
  assert.ok(play.every((s) => s.gap * r.facts.body.rate <= 12), `${r.name}: every body step fits the 12 s maxHold at ×${r.facts.body.rate}`);
}

execFileSync(process.execPath, ["build.mjs", "--traces", dir], { cwd: fig });
const html = readFileSync(`${fig}kubeharness-replay.html`, "utf8");
const keysProbe = (r) => `Array.from({ length: ${r.steps.length} }, (_, step) => {
  kubeharnessFigure.select(${JSON.stringify(r.profile)}, ${JSON.stringify(r.scenario)}, { step });
  const shown = (el) => getComputedStyle(el).display !== "none";
  const slots = (sel, key) => [...document.querySelectorAll(sel)].filter(shown).sort((a, b) => a.dataset[key] - b.dataset[key]);
  const labels = slots("[data-model-label]", "modelLabel");
  const lamps = slots("[data-model]", "model");
  return {
    tags: labels.map((el) => el.textContent),
    bold: labels.filter((el) => getComputedStyle(el).fontWeight === "700").map((el) => +el.dataset.modelLabel),
    lamps: lamps.length,
    lit: lamps.filter((el) => el.hasAttribute("data-on")).map((el) => +el.dataset.model),
    vents: [...document.querySelectorAll("[data-unkeyed]")].every(shown),
  };
})`;
const driven = execFileSync(process.execPath, ["tools/drive.mjs", `${fig}kubeharness-replay.html`, JSON.stringify(runs.map((r) => ["eval", keysProbe(r)]))], { cwd: fig, encoding: "utf8" });
assert.ok(driven.includes("console: clean"), `the replay page's console is clean while it is driven: ${driven.trim().split("\n").at(-1)}`);
const keys = [...driven.matchAll(/^eval: (.*)$/gm)].map((m) => JSON.parse(m[1]));
assert.equal(keys.length, runs.length, "one lamp key reading per recorded trace");
runs.forEach((r, n) => {
  const tags = Object.values(r.facts.models ?? {}).map((m) => m.tag);
  keys[n].forEach((k, step) => {
    const on = r.steps[step].model >= 0 ? [r.steps[step].model] : [];
    assert.deepEqual(k.tags, tags, `${r.name}: at step ${step} the page labels its lamps ${tags.join(", ") || "with nothing"}, in lamp order`);
    assert.equal(k.lamps, tags.length, `${r.name}: at step ${step} the page shows one lamp per tag`);
    assert.equal(k.vents, !tags.length, `${r.name}: at step ${step} post a's vents ${tags.length ? "give way to the lamp keys" : "show"}`);
    assert.deepEqual(k.lit, on, `${r.name}: at step ${step} (${r.steps[step].say}) ${on.length ? `only lamp ${on[0]} is lit` : "no lamp is lit"}`);
    assert.deepEqual(k.bold, on, `${r.name}: at step ${step} (${r.steps[step].say}) ${on.length ? `only the tag ${tags[on[0]]} is emphasised` : "no tag is emphasised"}`);
  });
});
const panel = /<aside class="kh-names"[\s\S]*?<\/aside>/.exec(html)[0];
const lists = panel.match(/<ol class="kh-groups"[\s\S]*?<\/ol><\/li><\/ol>/g);
assert.equal(lists.length, runs.length, "one group list per recorded trace");
for (const list of lists) {
  const key = /data-run="([^"]+)"/.exec(list)[1];
  const r = runs.find((x) => `${x.profile} ${x.scenario}` === key);
  const heads = [...list.matchAll(/<div class="kh-group-head"[^>]*>(.*?)<\/div>/g)].map((m) => m[1].replace(/<span class="kh-gone">.*?<\/span>/, "").replace(/ <span class="kh-api">.*?<\/span>/, "").replace(/<[^>]+>/g, "").replace(/^\d/, ""));
  assert.deepEqual(heads, headsOf(r), `${key}: the page's group headings`);
  const headIds = [...list.matchAll(/class="kh-group-head" data-obj="([^"]+)"/g)].map((m) => m[1]);
  const rowIds = [...list.matchAll(/class="kh-obj" data-obj="([^"]+)"/g)].map((m) => m[1]);
  assert.equal(headIds.length, Object.keys(r.facts.kinds).length, `${key}: every heading carries its object id`);
  assert.deepEqual(headIds.filter((id) => rowIds.includes(id)), [], `${key}: no heading object is also a row`);
}
const g02 = lists.find((l) => l.includes('data-run="gateway-api G02"'));
const apiBeside = (id) => new RegExp(`data-obj="${id}"[^>]*>(?:(?!</div>|</li>).)*?<span class="kh-api">([^<]*)</span>`).exec(g02)?.[1];
assert.equal(apiBeside("ReferenceGrant/payments/payments"), "gateway.networking.k8s.io/v1beta1", "G02 gateway-api: the ReferenceGrant heading shows its v1beta1 api");
assert.equal(apiBeside("HTTPRoute/shop/pay"), "gateway.networking.k8s.io/v1", "G02 gateway-api: the HTTPRoute heading shows its v1 api");
assert.ok(!panel.includes("data-pins"), "the pins live in the bottom-right corner, not the panel");
for (const r of runs) {
  const want = checksOf(r.events);
  const rows = new RegExp(`<ol class="kh-checks" data-run="${r.profile} ${r.scenario}">([\\s\\S]*?)</ol>`).exec(panel)?.[1] ?? "";
  assert.equal((rows.match(/data-check=/g) ?? []).length, want.length, `${r.name}: one check row per Await call`);
  const failed = new Set(r.events.filter((e) => e.ev === "return" && e.failed).map((e) => e.id));
  assert.deepEqual(r.steps[0].checks, want.map(() => "wait"), `${r.name}: every box starts empty`);
  assert.deepEqual(r.steps.at(-1).checks, want.map((c) => (failed.has(c.id) ? "fail" : "pass")), `${r.name}: each box ends as its call's return says`);
  assert.equal(r.steps[0].verdict, null, `${r.name}: no verdict before the report`);
  assert.equal(r.steps.at(-1).verdict, verdictOf(r.events.find((e) => e.ev === "report").line), `${r.name}: the verdict follows the report's cause`);
}
assert.equal(verdictOf("kubeharness cluster=x phase=ready cause=test-failed cleanup=succeeded"), "FAIL", "a test-failed cause is FAIL");
assert.equal(verdictOf("kubeharness cluster=x phase=ready cause=ok cleanup=succeeded"), "PASS", "an ok cause is PASS");
assert.equal(stoppedAt("kubeharness cluster=x phase=ready cause=test-failed cleanup=succeeded"), LAMPS.indexOf("test"), "a test-failed run stopped at the test lamp");
assert.equal(stoppedAt("kubeharness cluster=x phase=provisioning cause=provisioning cleanup=succeeded"), LAMPS.indexOf("provisioning"), "a setup failure stopped at the phase it reached");
assert.equal(stoppedAt("kubeharness cluster=x phase=ready cause=ok cleanup=succeeded"), -1, "an ok run stopped nowhere");
for (const r of runs) assert.equal(r.steps.at(-1).stopped, stoppedAt(r.events.find((e) => e.ev === "report").line), `${r.name}: the last step carries where the run stopped`);
for (const r of runs) assert.ok(html.includes(`data-scenario="${r.scenario}"`), `${r.name}: the page has a ${r.scenario} button`);
const text = panel.replace(/<[^>]+>/g, " ");
assert.deepEqual(benchWordsIn(text), [], "no bench-part word in the panel or its caption");
console.log("replay check passed");
