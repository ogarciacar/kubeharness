// Fig 1: kubeharness as a test bench. node build.mjs [--light] [--audit]
// writes kubeharness.html (or kubeharness-light.html) next to this script.
// With --traces <dir or file> it replays recorded traces instead and writes
// kubeharness-replay.html (or kubeharness-replay-light.html).
import { readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import * as k from "./kit/iso-kit.mjs";
import * as G from "./kit/lathe.mjs";
import * as A from "./kit/audit.mjs";
import { tubePieces, tubeSvg } from "./kit/tube.mjs";
import * as M from "./model.mjs";
import { bodyOf, checksOf, describe, groupsOf, objectsOf, parseTrace, stepsFromTrace, timeMap, timed } from "./replay.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const THEME = process.argv.includes("--light") ? "light" : "dark";
const TRACES = process.argv.includes("--traces") ? process.argv[process.argv.indexOf("--traces") + 1] : null;
const kitScript = () => readFileSync(join(HERE, "kit/iso-kit.mjs"), "utf8").replace(/^export /gm, "");

const W = 640;
const H = 372;
const AZIMUTH = 58;

// World plan, units of about 1 px, z up. The cluster plate's top is z = 0.
const PLATE = { x: -170, y: -90, w: 332, d: 180, r: 9 };
const PLATE_H = 12;
const FOOT_H = 5;
const HOST = { x: -248, y: -28, w: 54, d: 56, r: 5 };
const HOST_H = 10;
const ZT = 24;
const TR = 2.4;
const PORT_R = 4.2;
const PORT_T = 3;
const LIFT = 28;
const STROKE = 9;

function jsonlUnder(path) {
  if (!statSync(path).isDirectory()) return [path];
  return readdirSync(path, { recursive: true }).filter((f) => f.endsWith(".jsonl")).sort().map((f) => join(path, f));
}
function replayOf(path) {
  const out = {};
  const from = {};
  const clusters = [];
  const files = jsonlUnder(resolve(path));
  if (!files.length) throw new Error(`--traces ${path}: no .jsonl files`);
  for (const file of files) {
    const events = parseTrace(readFileSync(file, "utf8"), file);
    const run = describe(events, file);
    const key = `${run.profile} ${run.scenario}`;
    if (from[key]) throw new Error(`${file}: a second ${key} trace, the first is ${from[key]}`);
    from[key] = file;
    clusters.push(run.cluster);
    const facts = M.REPLAY[run.scenario];
    (out[run.profile] ??= {})[run.scenario] = { test: run.test, steps: stepsFromTrace(events, facts), body: bodyOf(events), map: facts.body, objects: objectsOf(events, facts), checks: checksOf(events).map(({ fn, label }) => ({ fn, label })) };
  }
  return { steps: out, files, clusters };
}
const REPLAYED = TRACES && replayOf(TRACES);
const named = (part) => (REPLAYED ? ` data-named="${part}"` : "");

const COLUMN = { x: -236, y: -15, w: 34, d: 30, r: 3 };
const COLUMN_H = 42;
const BULK = { x: -162, y: -10, w: 14, d: 20, r: 2 };
const BULK_H = 36;
const SOCK = { x: -124, y: -32, w: 64, d: 64, r: 4 };
const SOCK_H = 8;
const CART = { x: -114, y: -22, w: 44, d: 44, r: 4 };
const CART_Z = SOCK_H;
const CART_TOP = 56;
const DIV = { x: -24, y: -24, w: 48, d: 48, r: 5 };
const DIV_H = 40;
const TRAY = { x: 56, y: 18, w: 100, d: 62, r: 5 };
const TRAY_H = 2;
const POST_A = { x: 96, y: -62, w: 36, d: 36, r: 4 };
const POST_B = { x: 96, y: 30, w: 36, d: 36, r: 4 };
const POST_TOP = 50;
const VALVE = { x: 68, y: 40, w: 12, d: 16, r: 1.6 };
const VALVE_Z = [14, 34];
const STEM = [74, 48];
const LEVER = { r: 17, angles: { a: -35, b: 35, none: 0 } };

const P = k.fitProjection(
  [...k.boxCorners(PLATE, -PLATE_H - FOOT_H, 0), ...k.boxCorners(HOST, -HOST_H - FOOT_H, 0), [CART.x, CART.y, CART_TOP + LIFT + 8], [CART.x + CART.w, CART.y + CART.d, CART_TOP + LIFT + 8], [POST_A.x + POST_A.w, POST_A.y, POST_TOP + 6]],
  W,
  H,
  { pad: 28, azimuth: AZIMUTH },
);
const V = G.viewOf(P);
const at = (p) => k.iso(p, P);
const S = (paths, style) => k.solidSvg(paths, style);
const Ln = (d, style) => k.lineSvg(d, style);
const Dots = (points, style) => k.dotsSvg(points, style);
const ZX = [1, 0, 0];

const R = A.recorder(P, { order: ["cart", "gate"], spring: { stagger: 0, stiffness: 60, damping: 15.5, substeps: 4 } });
function put(name, ref, svg, shapes = [], { bias = 0, piece = "", move, attrs = "" } = {}) {
  return R.put({ name, at: ref, bias, svg, shapes, piece, ...(move ? { move } : {}), attrs });
}
function slab(name, plan, z0, h, style = {}, { steps = 4, bevel = 0.6, inner = "", extra = "", bias = 0, piece, move, attrs, meta = {} } = {}) {
  const shape = R.solid(A.slab(plan, z0, h, { name, ...meta }, steps));
  put(name, [plan.x + plan.w / 2, plan.y + plan.d / 2, z0 + h / 2], S(k.slabOf(plan, z0, h, P, steps, bevel), { tone: "mid", ...style, inner }) + extra, [shape], { bias, piece, move, attrs });
  return shape;
}
function post(name, x, y, r, z0, h, style = {}, { steps = 28, extra = "", bias = 0, piece, move, attrs, inner = "", meta = {} } = {}) {
  const shape = R.solid(A.cylinder(x, y, r, z0, h, { name, ...meta }));
  put(name, [x, y, z0 + h / 2], S(k.cylinder(x, y, r, z0, h, P, steps), { tone: "mid", ...style, inner }) + extra, [shape], { bias, piece, move, attrs });
  return shape;
}
function port(name, x0, x1, y, z, opts = {}) {
  const F = G.frameAlong([x0, y, z], ZX);
  const len = x1 - x0;
  const shape = R.solid(A.disc(F, 0, len, PORT_R, { name }));
  const faceS = V[0] > 0 ? len : 0;
  const ring = Math.abs(V[0]) > 0.12 ? Ln(G.circleOf(F, faceS, PORT_R - 1.1, P, 32), { tone: "faint" }) : "";
  put(name, [x0 + len / 2, y, z], S(G.disc(0, len, PORT_R, F, P), { tone: "mid" }) + ring, [shape], opts);
  return shape;
}

const BACK = [];

function facePanel(y, x0, x1, z1, z0, { rules = [0.8, 0.55, 0.7], screws = true, slots = 0 } = {}) {
  const m = k.sideMatrix([0, y], 0, P, "left");
  const w = x1 - x0;
  const h = z1 - z0;
  let out = `<rect class="kh-panel" x="${x0}" y="${-z1}" width="${w}" height="${h}" rx="1"/>`;
  const gap = h / (rules.length + 1);
  rules.forEach((f, i) => {
    out += `<path class="iso-line" data-tone="lo" data-free="" d="M${(x0 + 3).toFixed(1)} ${(-z1 + gap * (i + 1)).toFixed(1)}H${(x0 + 3 + (w - 9) * f).toFixed(1)}"/>`;
  });
  if (screws) out += `<circle class="kh-screw" cx="${x1 - 2}" cy="${-z1 + 2}" r="0.5"/><circle class="kh-screw" cx="${x1 - 2}" cy="${-z0 - 2}" r="0.5"/>`;
  for (let i = 0; i < slots; i++) out += `<rect class="kh-slot" x="${x0 + 2 + i * ((w - 4) / slots)}" y="${-z1 + 2}" width="${(w - 4) / slots - 2}" height="${h - 4}" rx="0.9"/>`;
  return `<g transform="${m}">${out}</g>`;
}
function ventSlots(y, x0, x1, z1, z0, count) {
  const m = k.sideMatrix([0, y], 0, P, "left");
  const pitch = (x1 - x0) / count;
  let out = "";
  for (let i = 0; i < count; i++) out += `<rect class="kh-slot" x="${(x0 + i * pitch + 0.6).toFixed(2)}" y="${-z1}" width="${(pitch - 1.6).toFixed(2)}" height="${z1 - z0}" rx="0.8"/>`;
  return `<g transform="${m}">${out}</g>`;
}

const CLAMP_R = 3.6;
function clamp(name, line, x, y, zBase) {
  const F = G.frameAlong([x, y, ZT], ZX);
  const collar = R.solid(A.disc(F, -1, 1, CLAMP_R, { name: `${name}.collar`, owner: line }));
  const face = Math.abs(V[0]) > 0.12 ? Ln(G.circleOf(F, V[0] > 0 ? 1 : -1, TR + 0.3, P, 24), { tone: "faint" }) : "";
  put(`${name}.collar`, [x, y, ZT], S(G.disc(-1, 1, CLAMP_R, F, P), { tone: "mid" }) + face + Dots([at([x, y, ZT + CLAMP_R])], { size: 0.4 }), [collar]);
  post(`${name}.foot`, x, y, 3, zBase, 1.2, { tone: "lo" }, { steps: 20 });
  post(`${name}.post`, x, y, 1.3, zBase + 1.2, ZT - CLAMP_R - zBase - 1.2, { tone: "mid" }, { steps: 16 });
}

const LINES = [];
function line(name, points, { gaps = [], free = [false, false] } = {}) {
  const route = points.length > 2 ? G.fillet(points, Math.max(3, TR * 2.6), 9, { P, tube: TR }) : points;
  const total = G.pathLength(route);
  R.route(name, route, TR, { gaps, free, rims: [TR, TR] });
  const pieces = tubePieces(route, TR, P, { maxLength: 12, gaps });
  pieces.forEach((piece, index) =>
    put(`${name}:${index}`, piece.mid, tubeSvg(piece, { tone: "hi" }), [A.tube(piece.points, piece.radii)], { bias: 0.05, attrs: ` data-route="${name}" data-chunk="${index}" data-from="${piece.from.toFixed(1)}"` }),
  );
  LINES.push({ name, total, chunks: pieces.map((p) => [p.from, p.to]) });
}

function hostStand() {
  BACK.push(`<path class="iso-halo" d="${k.haloOf(HOST, -HOST_H - FOOT_H, HOST_H, P)}"/>`);
  for (const [x, y] of k.corners(HOST, 9)) post("stand.host.foot", x, y, 4.2, -HOST_H - FOOT_H, FOOT_H, { tone: "lo" }, { steps: 20 });
  slab("host.plate", HOST, -HOST_H, HOST_H, { tone: "mid" }, { steps: 6, bevel: 1.2, extra: Ln(k.planOutline(k.insetPlan(HOST, 4), 0, P), { tone: "lo" }) + Dots(k.corners(HOST, 8).map(([x, y]) => at([x, y, 0])), { size: 0.45 }) });
  const seams = Ln(k.sideSeam(COLUMN, 10, P, 4), { tone: "faint" }) + Ln(k.sideSeam(COLUMN, 34, P, 4), { tone: "faint" });
  const vents = [0, 1, 2].map((i) => Ln(k.planOutline({ x: COLUMN.x + 6, y: COLUMN.y + 6 + i * 6.5, w: 16, d: 2.4, r: 1.1 }, COLUMN_H, P, 3), { tone: "lo" })).join("");
  slab("host.column", COLUMN, 0, COLUMN_H, { tone: "mid" }, { bevel: 0.8, inner: seams, extra: vents + facePanel(COLUMN.y + COLUMN.d, COLUMN.x + 5, COLUMN.x + 26, 30, 14, { rules: [0.9, 0.7, 0.8, 0.45] }) });
  post("host.cap", COLUMN.x + 27, COLUMN.y + 22, 3.2, COLUMN_H, 3, { tone: "hi" }, { steps: 20, extra: Ln(k.knurl(COLUMN.x + 27, COLUMN.y + 22, 3.2, COLUMN_H, COLUMN_H + 3, 18, P), { tone: "lo" }) });
  port("host.port", COLUMN.x + COLUMN.w, COLUMN.x + COLUMN.w + PORT_T, 0, ZT);
}

function plate() {
  BACK.push(`<path class="iso-halo" d="${k.haloOf(PLATE, -PLATE_H - FOOT_H, PLATE_H, P)}"/>`);
  for (const [x, y] of k.corners(PLATE, 22)) {
    post("stand.foot", x, y, 7, -PLATE_H - FOOT_H, 1.6, { tone: "lo" });
    post("plate.foot", x, y, 5.4, -PLATE_H - FOOT_H + 1.6, FOOT_H - 1.6, { tone: "mid" });
  }
  const screws = k.corners(PLATE, 10);
  const extra =
    Ln(k.planOutline(k.insetPlan(PLATE, 5), 0, P), { tone: "lo" }) +
    Ln(screws.map(([x, y]) => k.ring(x, y, 2.2, 0, P, 16)).join(""), { tone: "lo" }) +
    Dots(screws.map(([x, y]) => at([x, y, 0])), { size: 0.5 });
  slab("plate", PLATE, -PLATE_H, PLATE_H, { tone: "mid" }, { steps: 8, bevel: 1.6, extra, bias: -100 });

  const strip = { x: -152, y: 66, w: 110, d: 14, r: 2 };
  slab("lamps.strip", strip, 0, 1.4, { tone: "lo" }, { steps: 3, bevel: 0.3, extra: Dots([at([strip.x + 2.4, strip.y + 7, 1.4]), at([strip.x + strip.w - 2.4, strip.y + 7, 1.4])], { size: 0.42 }) });
  M.LAMPS.forEach((_, i) => {
    const x = strip.x + 10 + i * 15;
    const y = strip.y + 7;
    post(`lamps.ring${i}`, x, y, 4.4, 1.4, 1.6, { tone: "lo" }, { steps: 24 });
    post(`lamps.lens${i}`, x, y, 3, 3, 2.2, { tone: "hi" }, { steps: 24, attrs: ` data-kh="lamp" data-lamp="${i}"` });
  });

  // sized so a 17-character cluster name reads at 1x in a 2x capture of the card
  const label = { x: -38, y: 65, w: 90, d: 17, r: 1.5 };
  const engraved = REPLAYED ? REPLAYED.clusters[0] : M.CLUSTER;
  // a monospace advance with .04em tracking is 0.64 em; the text starts 6 in and must stop 4 short of the edge
  const fits = (chars) => (label.w - 10) / (chars * 0.64);
  const TEXT = REPLAYED ? 7 : Math.min(7, fits(engraved.length));
  if (REPLAYED) {
    const longest = Math.max(...REPLAYED.clusters.map((c) => c.length));
    if (fits(longest) < TEXT) throw new Error(`cluster name of ${longest} characters does not fit the ${label.w}-unit label plate`);
  }
  const plateText = k.faceTextSvg(k.topMatrix([label.x + 6, label.y + 4 + TEXT * 0.75], 0.8, P, "x"), engraved, { size: TEXT, tone: "hi" });
  const rules = Ln(k.lineOnTop([label.x + 6, label.y + label.d - 4.5], [label.x + 50, label.y + label.d - 4.5], 0.8, P), { tone: "lo", free: true });
  slab("plate.label", label, 0, 0.8, { tone: "lo" }, {
    steps: 4,
    bevel: 0.3,
    extra:
      Ln(k.planOutline(k.insetPlan(label, 1.6), 0.8, P), { tone: "faint" }) +
      (REPLAYED ? plateText.replace("<text ", '<text data-part="plate" ') : plateText) +
      rules +
      Dots([at([label.x + 3, label.y + label.d / 2, 0.8]), at([label.x + label.w - 3, label.y + label.d / 2, 0.8])], { size: 0.42 }),
  });

  const ruler = { x: -150, y: -86, w: 230, d: 7, r: 1 };
  const t = k.topTicks(ruler.x + 4, ruler.x + ruler.w - 4, 4, 5, ruler.y + 0.8, 0.6, [2, 3.6], P);
  slab("plate.ruler", ruler, 0, 0.6, { tone: "lo", crease: "none" }, { steps: 3, bevel: 0, extra: Ln(t.minor, { tone: "lo" }) + Ln(t.major, { tone: "mid" }) });

  const trayScrews = k.corners(TRAY, 5);
  slab("tray", TRAY, 0, TRAY_H, { tone: "lo" }, { steps: 5, bevel: 0.5, extra: Ln(k.planOutline(k.insetPlan(TRAY, 3), TRAY_H, P), { tone: "faint" }) + Dots(trayScrews.map(([x, y]) => at([x, y, TRAY_H])), { size: 0.45 }), attrs: ` data-kh="tray"${named("tray")}` });
}

function bulkhead() {
  const flange = { x: BULK.x - 3, y: BULK.y - 4, w: BULK.w + 6, d: BULK.d + 8, r: 2 };
  slab("bulk.flange", flange, 0, 3, { tone: "lo" }, { steps: 3, bevel: 0.4, extra: Dots(k.corners(flange, 2.4).map(([x, y]) => at([x, y, 3])), { size: 0.4 }) });
  slab("bulk.tower", BULK, 3, BULK_H - 3, { tone: "mid" }, { steps: 3, bevel: 0.6, inner: Ln(k.sideSeam(BULK, 30, P, 3), { tone: "faint" }) });
  port("bulk.in", BULK.x - PORT_T, BULK.x, 0, ZT);
  port("bulk.out", BULK.x + BULK.w, BULK.x + BULK.w + PORT_T, 0, ZT);
}

function socket() {
  slab("sock", SOCK, 0, SOCK_H, { tone: "mid" }, { steps: 4, bevel: 0.8, extra: Ln(k.planOutline(k.insetPlan(CART, -1.2), SOCK_H, P), { tone: "lo" }) + Dots(k.corners(SOCK, 9).map(([x, y]) => at([x, y, SOCK_H])), { size: 0.45 }) });
  for (const [x, y] of k.corners(SOCK, 4)) post("sock.pin", x, y, 2.4, SOCK_H, 12, { tone: "mid" }, { steps: 18, extra: Ln(k.ring(x, y, 1, SOCK_H + 12, P, 12), { tone: "faint" }) });
}

function cartridge() {
  const move = [0, 0, LIFT];
  const opts = { piece: "cart", move };
  const seams = Ln(k.sideSeam(CART, CART_Z + 6, P, 4), { tone: "faint" }) + Ln(k.sideSeam(CART, CART_TOP - 6, P, 4), { tone: "faint" });
  const slots = [0, 1, 2].map((i) => Ln(k.planOutline({ x: CART.x + 5, y: CART.y + 8 + i * 6, w: 2.4, d: 3.6, r: 1 }, CART_TOP, P, 3), { tone: "lo" })).join("");
  const panel = facePanel(CART.y + CART.d, CART.x + 6, CART.x + 30, CART_TOP - 12, CART_Z + 14);
  slab("cart.body", CART, CART_Z, CART_TOP - CART_Z, { tone: "hi", lit: true }, { bevel: 0.9, inner: seams, extra: Ln(k.planOutline(k.insetPlan(CART, 3.5), CART_TOP, P), { tone: "faint" }) + slots + panel, ...opts, attrs: ` data-kh="cart"${named("cart")}` });
  port("cart.in", CART.x - PORT_T, CART.x, 0, ZT, opts);
  port("cart.out", CART.x + CART.w, CART.x + CART.w + PORT_T, 0, ZT, opts);
  for (const y of [-14, 14]) post("cart.handle.post", CART.x + 12, y, 1.6, CART_TOP, 6, { tone: "mid" }, { steps: 14, ...opts });
  slab("cart.handle.bar", { x: CART.x + 10, y: -16, w: 4, d: 32, r: 1.4 }, CART_TOP + 6, 3, { tone: "mid" }, { steps: 3, bevel: 0.4, ...opts });
  const kx = CART.x + 32;
  const envoy = S(k.cylinder(kx, 0, 7, CART_TOP, 4, P, 36), { tone: "mid" }) + Ln(k.knurl(kx, 0, 7, CART_TOP, CART_TOP + 4, 40, P), { tone: "lo" }) + Ln(k.lineOnTop([kx, 0], [kx + 5, -3], CART_TOP + 4, P), { tone: "hi" });
  const agw =
    S(k.cylinder(kx, 9, 5, CART_TOP, 4, P, 30), { tone: "mid" }) + Ln(k.knurl(kx, 9, 5, CART_TOP, CART_TOP + 4, 30, P), { tone: "lo" }) + Ln(k.lineOnTop([kx, 9], [kx + 3.6, 7], CART_TOP + 4, P), { tone: "hi" }) +
    `<g class="kh-mcp">${S(k.cylinder(kx, -10, 3.6, CART_TOP, 3, P, 24), { tone: "hi" })}</g>` + Ln(k.ring(kx, -10, 1.6, CART_TOP + 3, P, 16), { tone: "lo" });
  const owner = "cart.top";
  const knobShapes = [R.solid(A.cylinder(kx, 0, 7, CART_TOP, 4, { name: "cart.knob", owner })), R.solid(A.cylinder(kx, -10, 3.6, CART_TOP, 3, { name: "cart.mcp", owner })), R.solid(A.cylinder(kx, 9, 5, CART_TOP, 4, { name: "cart.knob.agw", owner }))];
  put("cart.top", [kx, 0, CART_TOP + 2], `<g data-variant="gateway-api">${envoy}</g><g data-variant="gateway-agentgateway">${agw}</g>`, knobShapes, opts);
}

function diverter() {
  const seams = Ln(k.sideSeam(DIV, 8, P, 5), { tone: "faint" });
  const dial = Ln(k.ring(0, 0, 21, DIV_H, P, 72), { tone: "lo" });
  const ticks = [];
  const major = [];
  for (let a = -45; a <= 45; a += 5) {
    const t = (a * Math.PI) / 180;
    const big = a % 35 === 0 || a === 0;
    const r0 = 21;
    const r1 = big ? 17 : 19;
    (big ? major : ticks).push(k.lineOnTop([Math.cos(t) * r0, Math.sin(t) * r0], [Math.cos(t) * r1, Math.sin(t) * r1], DIV_H, P));
  }
  const screws = k.corners(DIV, 4.5);
  slab("div.body", DIV, 0, DIV_H, { tone: "mid" }, { steps: 5, bevel: 1, inner: seams, extra: facePanel(DIV.y + DIV.d, DIV.x + 8, DIV.x + 30, 30, 16, { rules: [0.9, 0.6] }) + dial + Ln(ticks.join(""), { tone: "lo" }) + Ln(major.join(""), { tone: "mid" }) + Dots(screws.map(([x, y]) => at([x, y, DIV_H])), { size: 0.45 }), attrs: ` data-kh="div"${named("div")}` });
  port("div.in", DIV.x - PORT_T, DIV.x, 0, ZT);
  port("div.a", DIV.x + DIV.w, DIV.x + DIV.w + PORT_T, -12, ZT);
  port("div.b", DIV.x + DIV.w, DIV.x + DIV.w + PORT_T, 12, ZT);
  post("div.hub", 0, 0, 4.4, DIV_H, 3, { tone: "mid" }, { steps: 28, extra: Ln(k.ring(0, 0, 1.6, DIV_H + 3, P, 16), { tone: "lo" }) });
  const arm = Ln(leverArm(0), { tone: "hi" });
  const knob = S(G.sphereOf([LEVER.r, 0, DIV_H + 4.4], 2.4, P, { steps: 32 }), { tone: "hi" });
  put("div.lever", [0, 0, DIV_H + 4], `<g data-part="lever">${arm.replace("<path", '<path data-part="lever-arm" style="stroke-width:1.3"')}</g><g data-part="lever-knob">${knob}</g>`, [R.solid(A.ball([LEVER.r, 0, DIV_H + 4.4], 2.4, { name: "div.knob", owner: "div.lever" })), R.solid(A.box([0, -0.7, DIV_H + 3], [LEVER.r - 2.2, 0.7, DIV_H + 4.4], { name: "div.arm", owner: "div.lever" }))], { bias: 2 });
}
function leverArm(deg) {
  const t = (deg * Math.PI) / 180;
  return k.lineOnTop([0, 0], [Math.cos(t) * LEVER.r, Math.sin(t) * LEVER.r], DIV_H + 3.2, P);
}

const MODEL_LAMPS = Math.max(0, ...Object.values(M.REPLAY).map((r) => Object.keys(r.models ?? {}).length));
function modelLamps(name, plan) {
  const cx = plan.x + plan.w / 2;
  const y = plan.y + plan.d;
  const z = POST_TOP - 4;
  for (let i = 0; i < MODEL_LAMPS; i++) {
    const x = cx + (i - (MODEL_LAMPS - 1) / 2) * 9;
    const F = G.frameAlong([x, y, z], [0, 1, 0]);
    const shape = R.solid(A.disc(F, 0, 1.4, 2.2, { name: `${name}.model${i}` }));
    put(`${name}.model${i}`, [x, y + 0.7, z], S(G.disc(0, 1.4, 2.2, F, P), { tone: "hi" }), [shape], { attrs: ` data-kh="lamp" data-model="${i}"` });
  }
}

function backend(name, plan, z0, { display = false, part, models = false } = {}) {
  const base = k.insetPlan(plan, -3);
  slab(`${name}.base`, base, z0, 4, { tone: "lo" }, { steps: 4, bevel: 0.5, extra: Dots(k.corners(base, 2.6).map(([x, y]) => at([x, y, z0 + 4])), { size: 0.42 }) });
  const seams = Ln(k.sideSeam(plan, z0 + 12, P, 4), { tone: "faint" }) + Ln(k.sideSeam(plan, POST_TOP - 8, P, 4), { tone: "faint" });
  let face = "";
  if (display) {
    const m = k.sideMatrix([0, plan.y + plan.d], 0, P, "left");
    const win = { x: plan.x + 5, y: -(POST_TOP - 12), w: 26, h: 9 };
    face = `<g transform="${m}"><clipPath id="kh-win"><rect x="${win.x}" y="${win.y}" width="${win.w}" height="${win.h}" rx="1.2"/></clipPath><rect class="kh-window" x="${win.x}" y="${win.y}" width="${win.w}" height="${win.h}" rx="1.2"/><g clip-path="url(#kh-win)"><text class="kh-digits" data-part="display" x="${win.x + win.w - 2}" y="${win.y + win.h - 2}" text-anchor="end" font-size="6.6"></text></g></g>`;
  }
  const vents = ventSlots(plan.y + plan.d, plan.x + 8, plan.x + 28, z0 + 26, z0 + 15, 6);
  slab(`${name}.body`, plan, z0 + 4, POST_TOP - z0 - 4, { tone: "mid" }, { bevel: 0.8, inner: seams, extra: face + vents + Ln(k.planOutline(k.insetPlan(plan, 3), POST_TOP, P), { tone: "faint" }), attrs: named(part) });
  const cx = plan.x + plan.w / 2;
  const cy = plan.y + plan.d / 2;
  post(`${name}.lamp.ring`, cx, cy, 6.4, POST_TOP, 1.6, { tone: "lo" }, { steps: 32 });
  post(`${name}.lamp`, cx, cy, 4.6, POST_TOP + 1.6, 2.6, { tone: "hi" }, { steps: 32, attrs: ` data-kh="lamp" data-post="${name}"` });
  if (models) modelLamps(name, plan);
  port(`${name}.port`, plan.x - PORT_T, plan.x, cy, ZT);
}

function gateValve() {
  const ped = { x: VALVE.x + 2, y: VALVE.y + 3, w: VALVE.w - 4, d: VALVE.d - 6, r: 1 };
  slab("gate.pedestal", ped, TRAY_H, VALVE_Z[0] - TRAY_H, { tone: "lo" }, { steps: 3, bevel: 0.3 });
  slab("gate.body", VALVE, VALVE_Z[0], VALVE_Z[1] - VALVE_Z[0], { tone: "mid" }, { steps: 3, bevel: 0.6, inner: Ln(k.sideSeam(VALVE, VALVE_Z[1] - 4, P, 3), { tone: "faint" }), extra: Dots(k.corners(VALVE, 2).map(([x, y]) => at([x, y, VALVE_Z[1]])), { size: 0.4 }), attrs: ` data-kh="gate"${named("gate")}`, meta: { owner: "b" } });
  const bonnetTop = VALVE_Z[1] + 4;
  post("gate.bonnet", STEM[0], STEM[1], 3.6, VALVE_Z[1], 4, { tone: "mid" }, { steps: 28 });
  const F = G.frameAlong([STEM[0], STEM[1], bonnetTop], [0, 0, 1]);
  const ROD = G.rodOut(F, 0, 4, 1.2, P, { stroke: STROKE });
  const seal = Ln(G.circleOf(F, 0, 1.6, P, 20), { tone: "faint" });
  const wheel = S(G.disc(4, 5.6, 4.6, F, P), { tone: "hi" }) + Ln(G.circleOf(F, 5.6, 2.2, P, 24), { tone: "lo" }) + Dots([at([STEM[0], STEM[1], bonnetTop + 5.6])], { size: 0.5 });
  const svg = `${seal}<g data-part="gate-stem"><clipPath id="kh-stem"><path data-hold d="${ROD.clip}"/></clipPath><g clip-path="url(#kh-stem)">${S(ROD.paths, { tone: "hi" })}</g>${wheel}</g>`;
  put("gate.stem", [STEM[0], STEM[1], bonnetTop + 3], svg, [R.solid(A.slide(F, 0, 4, 1.2, { name: "gate.rod" })), R.solid(A.disc(F, 4, 5.6, 4.6, { name: "gate.wheel" }))], { piece: "gate", move: [0, 0, STROKE], bias: 0.5 });
}

hostStand();
plate();
bulkhead();
socket();
cartridge();
diverter();
backend("postA", POST_A, 0, { display: true, part: "a", models: true });
backend("postB", POST_B, TRAY_H, { part: "b" });
gateValve();

const PX = (p) => p;
line("host", [[COLUMN.x + COLUMN.w + PORT_T, 0, ZT], [BULK.x - PORT_T, 0, ZT]]);
line("fwd", [[BULK.x + BULK.w + PORT_T, 0, ZT], [CART.x - PORT_T, 0, ZT]], { free: [false, true] });
{
  const x0 = CART.x + CART.w + PORT_T;
  const xc = (x0 + DIV.x - PORT_T) / 2;
  clamp("clamp.cart", "cart", xc, 0, 0);
  line("cart", [[x0, 0, ZT], [DIV.x - PORT_T, 0, ZT]], { free: [true, false], gaps: [[xc - x0 - 1, xc - x0 + 1]] });
}
{
  const pts = [[DIV.x + DIV.w + PORT_T, -12, ZT], [46, -12, ZT], [46, -44, ZT], [POST_A.x - PORT_T, -44, ZT]];
  const total = G.pathLength(G.fillet(pts, Math.max(3, TR * 2.6), 9, { P, tube: TR }));
  const xc = 72;
  const tail = POST_A.x - PORT_T;
  clamp("clamp.a", "a", xc, -44, 0);
  line("a", pts, { gaps: [[total - (tail - xc) - 1, total - (tail - xc) + 1]] });
}
{
  const pts = [[DIV.x + DIV.w + PORT_T, 12, ZT], [46, 12, ZT], [46, 48, ZT], [POST_B.x - PORT_T, 48, ZT]];
  const route = G.fillet(pts, Math.max(3, TR * 2.6), 9, { P, tube: TR });
  const total = G.pathLength(route);
  const tail = POST_B.x - PORT_T;
  const gap = [total - (tail - VALVE.x), total - (tail - VALVE.x - VALVE.w)];
  line("b", pts, { gaps: [gap] });
  LINES.find((l) => l.name === "b").gate = gap;
}

A.settle(R, P);
R.items.sort((a, b) => a.key - b.key);

const groups = R.items.map((item) => {
  const piece = item.piece ? ` data-piece="${item.piece}"` : "";
  return `<g class="it" data-name="${item.name}"${piece}${item.attrs ?? ""}>${item.svg}</g>`;
});

const LABEL =
  "kubeharness as a test bench. A small stand on the left is the go test process. Its line runs through a port-forward bulkhead onto a large base plate, the Kind cluster, and into a profile cartridge seated in a socket. From the cartridge the line enters a diverter block whose lever is the HTTPRoute rule. Two lines leave it: one to a backend post at the back, one through a gate valve onto a second namespace tray and a backend post at the front. Seven lamps along the front edge show the lifecycle.";

const PINS = { cart: [CART.x + 22, CART.y + 6, CART_TOP], div: [DIV.x + 6, DIV.y + DIV.d - 6, DIV_H], gate: [VALVE.x, VALVE.y + VALVE.d, VALVE_Z[1]], a: [POST_A.x + 6, POST_A.y + 6, POST_TOP], b: [POST_B.x + 6, POST_B.y + 6, POST_TOP], tray: [TRAY.x + TRAY.w - 6, TRAY.y + TRAY.d - 4, TRAY_H] };
function pinsSvg() {
  return Object.keys(PINS).map((part, i) => {
    const [x, y] = at(PINS[part]);
    const pin = `<g class="kh-pin" data-pin="${part}" transform="translate(${x.toFixed(1)} ${(y - 8).toFixed(1)})"><circle r="5.2"/><text>${i + 1}</text></g>`;
    return part === "cart" ? `<g data-piece="cart">${pin}</g>` : pin;
  }).join("");
}

const svg = k.figureSvg({ width: W, height: H, label: LABEL, body: [...BACK, `<g class="kh-bench">${groups.join("")}</g>`, ...(REPLAYED ? [`<g class="kh-pins">${pinsSvg()}</g>`] : [])] });

const DATA = {
  P,
  LIFT,
  STROKE,
  lever: { r: LEVER.r, z: DIV_H + 3.2, knobZ: DIV_H + 4.4, angles: LEVER.angles },
  lines: LINES,
  model: { PHASES: M.PHASES, LAMPS: M.LAMPS, PROFILES: M.PROFILES, REPORTS: M.REPORTS, BACKENDS: M.BACKENDS, SCENARIOS: M.SCENARIOS, LIFECYCLE: M.LIFECYCLE },
};
if (REPLAYED) Object.assign(DATA, { replay: REPLAYED.steps, cluster: M.CLUSTER });

const CSS = readFileSync(join(HERE, "kubeharness.css"), "utf8") + (REPLAYED ? readFileSync(join(HERE, "kubeharness-replay.css"), "utf8") : "");
const LIVE = readFileSync(join(HERE, "live.js"), "utf8").replace("__DATA__", JSON.stringify(DATA));

const controls = `<div class="kh-controls" role="group" aria-label="Profile and scenario">
  <div class="kh-row"><span class="kh-k">profile</span>${Object.values(M.PROFILES).map((p) => `<button type="button" data-profile="${p.name}">${p.label}</button>`).join("")}</div>
  <div class="kh-row"><span class="kh-k">scenario</span>${Object.keys(M.SCENARIOS).map((s) => `<button type="button" data-scenario="${s}">${s}</button>`).join("")}<button type="button" data-tour>tour</button></div>
</div>
<div class="kh-log"><div class="kh-say" data-say aria-live="polite"></div><code class="kh-report" data-report></code></div>`;

const esc = (text) => String(text).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
// The replay's name panel: one list per recorded run, stacked in one cell so
// the panel keeps the height of the longest. Every row is laid out from the
// start and only its state changes, so the card never grows between steps;
// an object the trace deletes carries its "gone" tag from the start too.
function namesPanel() {
  const runs = Object.entries(REPLAYED.steps).flatMap(([profile, byScenario]) => Object.entries(byScenario).map(([scenario, run]) => ({ profile, scenario, objects: run.objects, checks: run.checks })));
  const api = (o) => (o.api ? ` <span class="kh-api">${esc(o.api)}</span>` : "");
  const kindName = (o) => `<span class="kh-kind">${esc(o.kind)}</span> <span class="kh-name">${esc(o.name)}</span>${o.to === null ? "" : '<span class="kh-gone"> gone</span>'}${api(o)}`;
  const row = (o) => `<li class="kh-obj" data-obj="${esc(o.id)}" data-state="absent">${o.kind === "install" ? `<span class="kh-name kh-count">${esc(o.name)}</span>` : kindName(o)}</li>`;
  const head = ({ pin, kind, head }) => {
    const key = `<span class="kh-pin-key">${pin}</span>`;
    return head ? `<div class="kh-group-head" data-obj="${esc(head.id)}" data-state="absent">${key}<span>${kindName(head)}</span></div>` : `<div class="kh-group-head" data-state="absent">${key}<span><span class="kh-kind">${esc(kind)}</span></span></div>`;
  };
  const list = ({ profile, scenario, objects }) =>
    `<ol class="kh-groups" data-run="${profile} ${scenario}">${groupsOf(objects, M.REPLAY[scenario]).map((g) => `<li class="kh-group" data-part="${g.part}">${head(g)}<ol>${g.rows.map(row).join("")}</ol></li>`).join("")}</ol>`;
  const checkList = ({ profile, scenario, checks }) =>
    `<ol class="kh-checks" data-run="${profile} ${scenario}">${checks.map((c, i) => `<li class="kh-check" data-check="${i}" data-s="wait"><span class="kh-box"></span><span class="kh-kind">${esc(c.fn)}</span> <span class="kh-name">${esc(c.label)}</span></li>`).join("")}</ol>`;
  return `<aside class="kh-names" aria-label="Kubernetes objects from the trace"><div class="kh-names-k">K8s objects</div><div class="kh-names-lists">${runs.map(list).join("")}</div><div class="kh-checks-k">Checks</div><div class="kh-checks-lists">${runs.map(checkList).join("")}</div></aside>`;
}

const stage = `<div class="iso-stage kh-stage" tabindex="0" role="group" aria-label="kubeharness test bench. Use the buttons to pick a profile and a scenario.">${svg}</div>`;
const body =
  `<style>${CSS}</style>` +
  k.plateHtml({
    fig: REPLAYED ? "Replay" : "Fig 1",
    title: "kubeharness test bench",
    hint: REPLAYED ? Object.values(Object.values(REPLAYED.steps)[0])[0].test : "Pick a profile and a scenario",
    readout: "gateway-api · G01",
    keys: [
      { mark: "lit", label: "The profile cartridge: Envoy Gateway or agentgateway, installed into the cluster per test" },
      { mark: "raised", label: "Lines: go test → port-forward → gateway → HTTPRoute diverter → backends" },
      { mark: "edge", label: "Lamps: new, validating, provisioning, wait-usable, ready, test, cleanup" },
    ],
    caption: REPLAYED
      ? "A replay of real kubeharness e2e runs, one per test and profile, from the trace each run wrote into its test artifact directory. The plate carries the run's real Kind cluster name. Lamps, the cartridge, the lever, the gate valve and every request pulse move at their recorded times: a 2xx that names a backend lights that post, a refused request stops at the valve, an unrouted one at the diverter, and the report line at the end is the one the test logged. Each finished lamp stays dimly lit, so the row fills from new to cleanup; when a run fails, the lamp it stopped at turns red: test for a failed test, else the phase the report names. In G02 the valve follows the route's ResolvedRefs condition; in G03 the lever follows the weights each Apply set once the route reports them, while each pulse goes to the backend that answered; in A01 the MCP port lights for the session and the display shows the tools/call result. Setup, from New until the test holds a reachable gateway, plays 10 times faster, with each lifecycle phase lit at least 0.6 s. The test body, from that moment to cleanup, plays 20 times slower and holds each step at least 1.2 s so every line can be read, and at most 12 s, so a wait that timed out does not play for minutes; G03's body, 10 s of traffic every 50 ms, plays at real time with at most 4 pulses drawn a second. Add ?setup=N, ?body=N or ?hold=S to change those (?setup=1 plays setup at real time), and ?speed=2 to play everything faster. The numbered pins match the groups on the right, which list the run's real object names and API versions as the trace records them, each group headed by its primary object: a row appears when its object is applied or Ready, brightens with its part while a step touches it, and is struck through once the object is gone. Left out are objects with no part on the bench, such as the backends' Deployments and anything in kube-system; the profile install's CRDs, RBAC and controller objects are one count in the Gateway group. Under them, CHECKS has one box per Await call the test made, filled when the call returns: green when it passed, red when it failed the test. The badge by the title reads RUNNING until the report line, then PASS when its cause is ok and FAIL for any other cause, which includes a failed t.Errorf the boxes do not show. The bottom-right corner names the profile's pinned versions and the Kind cluster. The top-right corner counts the replay's own elapsed time, not the trace's, so a stretch where nothing moves still shows time passing."
      : "Each run builds a fresh Kind cluster (the plate), installs one gateway profile (the cartridge drops into its socket), applies the scenario's manifests and sends real HTTP from the test process through a port-forward. The diverter's lever is the HTTPRoute: in G01 it picks a backend by host and path, and with no matching rule the request stops at the diverter; in G03 it flips the weights 100/0 and 0/100 under traffic. The gate valve on the front line is the ReferenceGrant into the second namespace (G02): closed, the route reports RefNotPermitted and the payments counter stays flat. A01 runs only under agentgateway and calls the calculator's sum tool through an AgentgatewayBackend. Each finished lamp stays dimly lit, so the row fills from new to cleanup. At cleanup the cartridge lifts out. Report-line values are the recorded runs in README.md and CONCEPT.md; the motion runs faster than those durations.",
    body: REPLAYED ? `<div class="kh-split"><div class="kh-main">${stage}${controls}</div>${namesPanel()}</div>` : `${stage}${controls}`,
  });

const page = k.pageHtml({ title: "kubeharness test bench", theme: THEME, body, script: kitScript() + `${timeMap}\n${timed}\n` + LIVE, width: REPLAYED ? 1060 : 680 });
const name = `kubeharness${REPLAYED ? "-replay" : ""}${THEME === "light" ? "-light" : ""}.html`;
writeFileSync(join(HERE, name), page);
if (REPLAYED) console.log("replaying", REPLAYED.files.join(", "));
console.log("wrote", name, `${(page.length / 1024).toFixed(0)} KB`, `${R.items.length} items`, `${R.solids.length} solids`, `${R.tubes.length} tube runs`);

A.auditOrExit(R, P, {});
