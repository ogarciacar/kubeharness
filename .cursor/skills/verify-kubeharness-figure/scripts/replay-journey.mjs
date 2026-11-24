// node scripts/replay-journey.mjs [traces-dir] [out.json]: writes journeys/replay.json
// from docs/figure/traces (or traces-dir), one leg per recorded trace, in profile
// then scenario order. Each leg clicks its profile and scenario buttons and asserts
// literal recorded values: the test name in the corner, the cluster name, Pod
// names, object names and API versions, group headings, the profile's pins in
// the bottom-right corner, each check box's colour, the PASS or FAIL badge and
// the report line. Rerun after re-recording a trace; doctor.sh fails when a
// trace's report line is missing from the journey.
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
const root = fileURLToPath(new URL("../../../../", import.meta.url));
const { checksOf, describe, groupsOf, objectsOf, parseTrace, stepsFromTrace, stoppedAt, verdictOf } = await import(`${root}docs/figure/replay.mjs`);
const { CLUSTER, LAMPS, PROFILES, REPLAY } = await import(`${root}docs/figure/model.mjs`);
const { benchWordsIn } = await import(`${root}docs/figure/tools/bench-words.mjs`);
const { answersOf } = await import(`${root}docs/figure/tools/k01-route.mjs`);
const dir = resolve(process.argv[2] ?? `${root}docs/figure/traces`);
const out = process.argv[3] ?? `${root}.cursor/skills/verify-kubeharness-figure/journeys/replay.json`;

const CARD_HEIGHT_PX = 625;

const crossedAnswer = (ev, models) => {
  const a = answersOf(ev).find((a) => a.want !== a.alias);
  return a && { alias: a.alias, id: Object.keys(models).find((id) => models[id].backend === a.want) };
};

const runs = {};
for (const name of readdirSync(dir).filter((f) => f.endsWith(".jsonl")).sort()) {
  const ev = parseTrace(readFileSync(`${dir}/${name}`, "utf8"), name);
  const run = describe(ev, name);
  const facts = REPLAY[run.scenario];
  const objects = objectsOf(ev, facts);
  const say = stepsFromTrace(ev, facts).map((s) => s.say).find((t) => / Pod \S+/.test(t));
  const podOn = (part) => objects.find((o) => o.part === part && o.kind === "Pod")?.id;
  (runs[run.profile] ??= {})[run.scenario] = {
    test: ev[0].test,
    cluster: ev[0].cluster,
    report: ev.find((e) => e.ev === "report").line,
    pod: / Pod (\S+)/.exec(say)[1],
    lag: stepsFromTrace(ev, facts).some((s) => s.say.includes("proxy still routing after ResolvedRefs=False")),
    pods: Object.fromEntries(["a", "b"].filter((p) => podOn(p)).map((p) => [p, podOn(p)])),
    groups: groupsOf(objects, facts),
    checks: checksOf(ev).map((c) => {
      const ret = ev.find((e) => e.ev === "return" && e.id === c.id);
      return { ...c, end: !ret ? "wait" : ret.failed ? "fail" : "pass" };
    }),
    verdict: verdictOf(ev.find((e) => e.ev === "report").line),
    stopped: stoppedAt(ev.find((e) => e.ev === "report").line),
    crossed: facts.models && crossedAnswer(ev, facts.models),
  };
}

const SHORT = { "gateway-api": "envoy", "gateway-agentgateway": "agw" };
const say = ["eval", "document.querySelector('[data-say]').textContent"];
const Y = `+document.querySelector('[data-piece="gate"]').getAttribute('transform').split(' ')[1].slice(0, -1)`;
const gateY = ["eval", `document.querySelector('[data-piece="gate"]').getAttribute('transform')`];
// The G02 body plays 20 times slower with a 1.2 s minimum per step, so the
// longest wait between two body states is one slowed AwaitRoute poll (~10 s)
// plus a few held steps.
const BODY_WAIT = 60000;
const row = (id) => `document.querySelector('.kh-groups[data-on] [data-obj="${id}"]')`;
const GRANT = row("ReferenceGrant/payments/payments");
const lit = (part) => `document.querySelector('[data-post="${part === "a" ? "postA" : "postB"}"]').hasAttribute('data-on')`;
const pinsOn = ["eval", "[...document.querySelectorAll('[data-pin]')].filter((p) => !p.hasAttribute('data-off')).map((p) => p.dataset.pin + '=' + p.textContent + (p.hasAttribute('data-on') ? '*' : '')).join(',') + ' · lit parts ' + [...document.querySelectorAll('[data-named][data-touched]')].map((p) => p.dataset.named).join(',')"];
const rowsShown = ["eval", "[...document.querySelectorAll('.kh-groups[data-on] [data-obj]')].filter((r) => r.dataset.state !== 'absent').map((r) => r.dataset.state + (r.hasAttribute('data-touched') ? '*' : '') + ' ' + r.textContent).join(' | ')"];
const heads = "[...document.querySelectorAll('.kh-groups[data-on] .kh-group-head')].map((h) => [...h.querySelectorAll('.kh-kind, .kh-name')].filter((e) => getComputedStyle(e).visibility !== 'hidden').map((e) => e.textContent).join(' ')).join(' | ')";
const headsAre = (list) => ["until", `${heads} === '${list.join(" | ")}'`, BODY_WAIT];
const noPartWords = ["until", `(${benchWordsIn})(document.querySelector('.kh-names').textContent).length === 0`, 1000];
const checkRows = "[...document.querySelectorAll('.kh-checks[data-on] [data-check]')]";
const checkLabels = `${checkRows}.map((r) => r.textContent).join(' | ')`;
const boxes = `${checkRows}.map((r) => r.dataset.s).join(',')`;
const badge = "document.querySelector('[data-verdict]').textContent";
// The colour an element is painted, named by the theme variable it matches: green, red or none.
const tone = "((el) => { const bg = getComputedStyle(el).backgroundColor; const probe = (v) => { const e = document.createElement('i'); e.style.background = `var(--anatomy-${v})`; el.appendChild(e); const c = getComputedStyle(e).backgroundColor; e.remove(); return c; }; return ['green', 'red'].find((v) => probe(v) === bg) ?? 'none'; })";
const boxColours = `${checkRows}.map((r) => ${tone}(r.querySelector('.kh-box'))).join(',')`;
const elapsed = "document.querySelector('[data-elapsed]').textContent";
const seconds = `((t) => +t.slice(0, 2) * 60 + +t.slice(3))(${elapsed})`;
// Each lamp as on, fail (the lamp a failed run stopped at, red), done (filled dim) or - (still to come).
const lampRow = "[...document.querySelectorAll('[data-lamp]')].map((l) => (l.hasAttribute('data-on') ? 'on' : l.hasAttribute('data-fail') ? 'fail' : l.hasAttribute('data-done') ? 'done' : '-')).join(',')";
const COLOUR = { pass: "green", fail: "red", wait: "none" };
const corner = (name) => `document.querySelector('[data-corner="${name}"]').innerText`;
const plateH = "Math.round(document.querySelector('.iso-plate').getBoundingClientRect().height)";
const cardHeight = [["eval", `'card height ' + ${plateH}`], ["until", `${plateH} === ${CARD_HEIGHT_PX}`, 1000]];
const apiShown = (o) => ["until", `${row(o.id)}.querySelector('.kh-api')?.textContent === '${o.api}' && getComputedStyle(${row(o.id)}.querySelector('.kh-api')).visibility === 'visible'`, BODY_WAIT];
const apisShown = ["eval", "[...document.querySelectorAll('.kh-groups[data-on] [data-obj]')].filter((r) => r.querySelector('.kh-api') && getComputedStyle(r.querySelector('.kh-api')).visibility === 'visible').map((r) => r.dataset.obj + ' ' + r.querySelector('.kh-api').textContent).join(' | ')"];
const sayHas = (text) => `document.querySelector('[data-say]').textContent.includes('${text}')`;
const display = "document.querySelector('[data-part=\"display\"]').textContent";
const modelLamps = "[...document.querySelectorAll('[data-model]')].map((l) => (l.hasAttribute('data-on') ? 1 : 0)).join('')";
const lampsShown = ["eval", `'model lamps ' + ${modelLamps} + ' · display ' + ${display}`];
const headOf = (f, kind) => f.groups.find((g) => g.kind === kind).head;
const kindsOf = (f) => f.groups.map((g) => g.kind);
const namesOf = (f) => f.groups.map((g) => `${g.kind} ${g.head.name}`);

function open(p, sc, f) {
  return [
    ["click", `button[data-profile="${p}"]`],
    ["click", `button[data-scenario="${sc}"]`],
    ["until", `document.querySelector('.kh-stage').dataset.scenario === '${sc}' && document.querySelector('.kh-stage').dataset.profile === '${p}'`, 2000],
    ["until", `${corner("hint")} === '${f.test}' && ${corner("fig")} === 'Replay'`, 2000],
    ["eval", `${corner("fig")} + ' · ' + ${corner("hint")}`],
    ["until", "document.querySelector('.kh-names-k').innerText === 'K8S OBJECTS'", 1000],
    ["until", "document.querySelector('[data-corner=\"hint\"]').getBoundingClientRect().right < document.querySelector('[data-corner=\"readout\"]').getBoundingClientRect().left", 1000],
    ["eval", "JSON.stringify(kubeharnessFigure.state())"],
    ["eval", "[...document.querySelectorAll('.kh-controls button')].map((b) => (b.dataset.scenario ?? b.dataset.profile ?? 'tour') + (b.disabled ? ':disabled' : ':on')).join(' ')"],
    ["until", `document.querySelector('[data-part="plate"]').textContent === '${f.cluster}'`, 5000],
    ["eval", `document.querySelector('[data-part="plate"]').textContent`],
    ["until", `${corner("readout")} === 'PINNED\\n${PROFILES[p].pins}\\nCLUSTER\\n${CLUSTER}'`, 1000],
    ["eval", corner("readout")],
    ["until", `${checkLabels} === ${JSON.stringify(f.checks.map((c) => `${c.fn} ${c.label}`).join(" | "))}`, 1000],
    ["until", `${boxes} === '${f.checks.map(() => "wait").join(",")}' && ${badge} === 'RUNNING'`, 2000],
    ["eval", `${checkLabels} + ' · ' + ${boxes} + ' · ' + ${badge}`],
    ["until", `/^\\d\\d:\\d\\d\\.\\d{3}$/.test(${elapsed}) && ${seconds} < 2`, 1000],
    headsAre(kindsOf(f)),
    ["eval", heads],
    noPartWords,
    pinsOn,
    ["eval", `kubeharnessFigure.steps().filter((s) => s.say.includes('Pod ${f.pod}')).map((s) => s.index + ': ' + s.say).join(' | ')`],
    ["until", "document.querySelector('.kh-stage').dataset.phase === 'test'", 60000],
    ["shot", `replay-${SHORT[p]}-${sc}-install.png`],
    ...cardHeight,
    ["until", Object.values(f.pods).map((id) => `${row(id)}.dataset.state === 'present' && ${row(id)}.textContent.includes('${id.slice(4)}')`).join(" && "), BODY_WAIT],
    ["eval", Object.values(f.pods).map((id) => `${row(id)}.textContent`).join(" + ' | ' + ")],
    apiShown(headOf(f, "HTTPRoute")),
    apisShown,
  ];
}
const close = (p, sc, f) => [
  ["until", `document.querySelector('.kh-stage').dataset.phase === 'off' && document.querySelector('[data-report]').textContent === '${f.report}'`, BODY_WAIT],
  ["until", `${boxes} === '${f.checks.map((c) => c.end).join(",")}' && ${boxColours} === '${f.checks.map((c) => COLOUR[c.end]).join(",")}'`, 1000],
  ["until", `${badge} === '${f.verdict}' && ${tone}(document.querySelector('[data-verdict]')) === '${f.verdict === "PASS" ? "green" : "red"}'`, 1000],
  ["eval", `${boxes} + ' · ' + ${boxColours} + ' · ' + ${badge}`],
  ["until", `${seconds} > kubeharnessFigure.runSeconds() - 3 && ${seconds} <= kubeharnessFigure.runSeconds()`, 1000],
  ["eval", `${elapsed} + ' of ' + kubeharnessFigure.runSeconds().toFixed(3) + ' s'`],
  ["until", `${lampRow} === '${LAMPS.map((_, n) => (n === f.stopped ? "fail" : "done")).join(",")}'`, 1000],
  ["eval", lampRow],
  ["shot", `replay-${SHORT[p]}-${sc}-off.png`],
  ...cardHeight,
  ["eval", "document.querySelector('[data-report]').textContent"],
];

const BODY = {
  // gateway_test.go g01Routes.
  G01: (p, f) => [
    headsAre(namesOf(f)),
    ["until", `document.querySelector('[data-say]').textContent.includes('GET http://shop.example/orders/42 → 200 from orders') && ${lit("a")}`, BODY_WAIT],
    ["shot", `replay-${SHORT[p]}-G01-orders.png`],
    say, pinsOn,
    ["until", `document.querySelector('[data-say]').textContent.includes('GET http://payments.example/refunds → 200 from payments') && ${lit("b")}`, BODY_WAIT],
    ["shot", `replay-${SHORT[p]}-G01-payments.png`],
    say,
    ["until", "document.querySelector('[data-say]').textContent.includes('GET http://unknown.example/orders') && document.querySelector('[data-kh=\"div\"]').hasAttribute('data-block')", BODY_WAIT],
    ["shot", `replay-${SHORT[p]}-G01-unrouted.png`],
    say, gateY,
  ],
  // gateway_test.go TestG02ReferenceGrant: denied, granted, revoked.
  G02: (p, f) => [
    headsAre(namesOf(f).map((h, i) => (f.groups[i].kind === "ReferenceGrant" ? "ReferenceGrant" : h))),
    ["eval", heads],
    ["eval", "kubeharnessFigure.steps().map((s) => (s.gate ? 'open' : 'closed')).filter((g, i, all) => g !== all[i - 1]).join(' → ')"],
    ["until", `document.querySelector('[data-kh="gate"]').hasAttribute('data-block') && ${Y} > -0.05`, 60000],
    ["shot", `replay-${SHORT[p]}-G02-denied.png`],
    say, gateY, rowsShown,
    ["until", `${GRANT}.dataset.state === 'present' && ${GRANT}.hasAttribute('data-touched') && document.querySelector('[data-pin="gate"]').hasAttribute('data-on') && document.querySelector('[data-named="gate"]').hasAttribute('data-touched')`, BODY_WAIT],
    apiShown(headOf(f, "ReferenceGrant")),
    ["shot", `replay-${SHORT[p]}-G02-grant-row.png`],
    headsAre(namesOf(f)),
    apisShown,
    say, pinsOn, rowsShown, ["eval", heads],
    ["until", `${Y} < -0.5`, BODY_WAIT], gateY,
    ...(f.lag ? [["until", "document.querySelector('[data-say]').textContent.includes('proxy still routing after ResolvedRefs=False')", BODY_WAIT], ["shot", `replay-${SHORT[p]}-G02-proxy-lag.png`], say, gateY] : []),
    ["until", lit("b"), BODY_WAIT],
    ...(f.lag ? [] : [["shot", `replay-${SHORT[p]}-G02-allowed.png`], say]),
    gateY,
    ["until", `document.querySelector('[data-kh="gate"]').hasAttribute('data-block')`, BODY_WAIT],
    ["shot", `replay-${SHORT[p]}-G02-revoked.png`],
    say,
    ["until", `${GRANT}.dataset.state === 'gone' && getComputedStyle(${GRANT}.querySelector('.kh-gone')).visibility === 'visible'`, BODY_WAIT],
    ["eval", `${GRANT}.dataset.state + ' · ' + ${GRANT}.textContent`],
    ["until", `${Y} > -0.05`, BODY_WAIT],
    gateY,
  ],
  // g03_test.go and flips_test.go: blue, green, blue under traffic; the lever follows the applied weights.
  G03: (p, f) => [
    headsAre(namesOf(f)),
    ["eval", "kubeharnessFigure.steps().map((s) => s.lever).filter((l, i, all) => l !== 'none' && l !== all[i - 1]).join(' → ')"],
    ["until", `kubeharnessFigure.state().lever === 'a' && ${lit("a")}`, BODY_WAIT],
    ["shot", `replay-${SHORT[p]}-G03-blue.png`],
    say, pinsOn,
    ["until", `kubeharnessFigure.state().lever === 'b' && ${lit("b")}`, BODY_WAIT],
    ["shot", `replay-${SHORT[p]}-G03-green.png`],
    say, pinsOn,
    ["until", `kubeharnessFigure.state().lever === 'a' && ${lit("a")}`, BODY_WAIT],
    ["shot", `replay-${SHORT[p]}-G03-blue-again.png`],
    say,
  ],
  // a01_test.go sumIs42: the MCP port lights for the session and the display reads 42.
  A01: (p, f) => [
    headsAre(namesOf(f)),
    ["until", "document.querySelector('.kh-mcp').hasAttribute('data-on')", BODY_WAIT],
    ["shot", `replay-${SHORT[p]}-A01-mcp.png`],
    say, pinsOn, rowsShown,
    ["until", "document.querySelector('[data-part=\"display\"]').textContent === '42' && document.querySelector('[data-say]').textContent.includes('tools/call = 42')", BODY_WAIT],
    ["shot", `replay-${SHORT[p]}-A01-42.png`],
    say,
  ],
  // k01_test.go TestK01Routing.
  K01: (p, f) => {
    if (!f.crossed) throw new Error(`${f.test} on ${f.cluster}: the trace passes but no /m/<alias>/ request was answered under a route that sends it to another alias's backend, so the K01 leg has no crossed answer to wait for`);
    const ids = Object.keys(REPLAY.K01.models);
    const answered = (alias, id) => `${sayHas(`POST http://llm.example/m/${alias}/`)} && ${sayHas(`from ${id}`)} && ${display} === '${REPLAY.K01.models[id].tag}' && ${modelLamps} === '${ids.map((k) => (k === id ? 1 : 0)).join("")}'`;
    const dark = `${display} === '' && ${modelLamps} === '${ids.map(() => 0).join("")}'`;
    const own = ids.find((id) => REPLAY.K01.models[id].backend === f.crossed.alias);
    return [
      headsAre(namesOf(f)),
      ["until", "document.querySelector('.kh-bench').hasAttribute('data-models')", 1000],
      ...ids.flatMap((id) => [
        ["until", answered(REPLAY.K01.models[id].backend, id), BODY_WAIT],
        ["shot", `replay-${SHORT[p]}-K01-${REPLAY.K01.models[id].backend}.png`],
        say, lampsShown, pinsOn,
      ]),
      ["until", `${sayHas("POST http://llm.example/m/unknown/")} && document.querySelector('[data-kh="div"]').hasAttribute('data-block') && ${modelLamps} === '${ids.map(() => 0).join("")}'`, BODY_WAIT],
      ["shot", `replay-${SHORT[p]}-K01-unknown.png`],
      say, lampsShown,
      ["until", answered(f.crossed.alias, f.crossed.id), BODY_WAIT],
      ["shot", `replay-${SHORT[p]}-K01-crossed.png`],
      say, lampsShown, rowsShown,
      ["until", `${sayHas("h.Apply(testdata/k01-route/good)")} && ${dark}`, BODY_WAIT],
      ["shot", `replay-${SHORT[p]}-K01-reapply.png`],
      say, lampsShown,
      ["until", `${sayHas("gw.AwaitRoute(llm)")} && ${dark}`, BODY_WAIT],
      say, lampsShown,
      ["until", answered(f.crossed.alias, own), BODY_WAIT],
      ["shot", `replay-${SHORT[p]}-K01-restored.png`],
      say, lampsShown,
      ["until", `['cleanup', 'off'].includes(document.querySelector('.kh-stage').dataset.phase) && ${dark}`, BODY_WAIT],
      say, lampsShown,
    ];
  },
};

const a = [];
for (const p of Object.keys(PROFILES)) for (const sc of PROFILES[p].scenarios) {
  const f = runs[p]?.[sc];
  // A failing run stops where its check failed, so the scenario's body (written
  // for the passing run) does not apply; its proof is the boxes and the badge.
  if (f) a.push(...open(p, sc, f), ...(f.verdict === "PASS" ? BODY[sc](p, f) : []), ...close(p, sc, f));
}
writeFileSync(out, JSON.stringify(a, null, 1) + "\n");
console.log(`wrote ${out}: ${Object.entries(runs).map(([p, byScenario]) => `${p} ${Object.keys(byScenario).join(",")}`).join(" · ")}`);
