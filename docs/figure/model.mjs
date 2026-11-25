// Every word and number the figure shows comes from this module, and each
// one names the file it was read from. The page's timeline is built from
// these steps; nothing in build.mjs or the live script invents a value.

// internal/lifecycle/report.go: Phase constants, in order.
export const PHASES = ["new", "validating", "provisioning", "wait-usable", "ready"];

export const LAMPS = [...PHASES, "test", "cleanup"];

// internal/profile/envoygateway.go EnvoyGateway and agentgateway.go
// AgentGateway (profile names); README.md "Install a gateway profile" (pins).
export const PROFILES = {
  "gateway-api": {
    label: "Envoy Gateway",
    name: "gateway-api",
    pins: "Gateway API v1.6.1 · Envoy Gateway v1.9.3",
    scenarios: ["G01", "G02", "G03"],
  },
  "gateway-agentgateway": {
    label: "agentgateway",
    name: "gateway-agentgateway",
    pins: "Gateway API v1.6.1 · agentgateway v1.4.1",
    scenarios: ["G01", "G02", "G03", "A01", "K01"],
  },
};

// go.mod sigs.k8s.io/kind v0.33.0 and its default node image's Kubernetes,
// README.md "go test -tags e2e" and docs/adr/0030 (the tested combination).
export const CLUSTER = "kind v0.33.0 · k8s v1.37.0";

// Recorded report-line values. README.md's sample report line is one
// gateway-api run; the agentgateway install= times are CONCEPT.md "S3 slice 6"
// (G01–G03) and "S4 A01 result" (about 6.2s); K01's is its committed trace
// (traces/K01-gateway-agentgateway.jsonl). Fields the repo does not record
// for a run are left out rather than guessed.
export const REPORTS = {
  "gateway-api": {
    G01: { cluster: "kh-7630e8a319a4-1", create: "10.7s", ready: "17.4s", delete: "390ms", install: "15.7s" },
    G02: { cluster: "kh-7630e8a319a4-1", create: "10.7s", ready: "17.4s", delete: "390ms", install: "15.7s" },
    G03: { cluster: "kh-7630e8a319a4-1", create: "10.7s", ready: "17.4s", delete: "390ms", install: "15.7s" },
  },
  "gateway-agentgateway": {
    G01: { install: "7.4s" },
    G02: { install: "6.7s" },
    G03: { install: "7.0s" },
    A01: { install: "6.2s" },
    K01: { install: "6.3s" },
  },
};

// Which part of the bench each backend name stands on. The diverter has two
// outlets: "a" (back line) and "b" (front line, through the gate valve, onto
// the second namespace tray). "none" means no rule matched, so the request
// stops at the diverter and no backend answers.
export const BACKENDS = {
  G01: { a: "orders", b: "payments" },
  G02: { a: "shop/orders", b: "payments/payments" },
  G03: { a: "shop/blue", b: "shop/green" },
  A01: { a: "calculator", b: null },
  K01: { a: "kronk", b: null },
};

// One test body per scenario, as steps. Each step is shown for `hold`
// seconds of figure time. `send` fires one request pulse: `to` is the
// outlet, `stop` where it ends ("a", "b", "gate", "none"). `gate` sets the
// gate valve (true = open), `lever` the diverter lever ("a", "b", "none").
export const SCENARIOS = {
  // examples/gateway/gateway_test.go g01Routes, and testdata/g01/gateway.yaml.
  G01: {
    title: "G01 · host and path routing",
    gate: true,
    steps: [
      { say: "AwaitRoute shop RouteReady · AwaitRoute payments RouteReady", hold: 1.6 },
      { say: "GET http://shop.example/orders → orders", send: "a", lever: "a", hold: 1.4 },
      { say: "GET http://shop.example/orders/42 → orders", send: "a", lever: "a", hold: 1.4 },
      { say: "GET http://shop.example/payments → payments", send: "b", lever: "b", hold: 1.4 },
      { say: "GET http://payments.example/refunds → payments", send: "b", lever: "b", hold: 1.4 },
      { say: "GET http://shop.example/unrouted → no backend", send: "none", lever: "none", hold: 1.4 },
      { say: "GET http://unknown.example/orders → no backend", send: "none", lever: "none", hold: 1.4 },
    ],
  },
  // examples/gateway/gateway_test.go TestG02ReferenceGrant: denied, grant
  // Apply, allowed, grant Delete, denied. testdata/g02, g02-grant.
  G02: {
    title: "G02 · cross-namespace ReferenceGrant",
    gate: false,
    steps: [
      { say: "route shop/pay ResolvedRefs=False reason=RefNotPermitted", gate: false, hold: 1.8 },
      { say: "GET http://shop.example/orders → orders", send: "a", lever: "a", hold: 1.4 },
      { say: "burst of 5 to /payments · payments counter flat", send: "gate", lever: "b", burst: 5, hold: 2.2 },
      { say: "h.Apply(testdata/g02-grant) · ReferenceGrant payments/payments", gate: true, hold: 1.8 },
      { say: "route shop/pay RouteReady · GET /payments → payments, counter rises", send: "b", lever: "b", hold: 1.8 },
      { say: "h.Delete(testdata/g02-grant)", gate: false, hold: 1.6 },
      { say: "route shop/pay RefNotPermitted · payments counter flat", send: "gate", lever: "b", burst: 5, hold: 2.2 },
    ],
  },
  // examples/gateway/g03_test.go and flips_test.go: flip order blue, green,
  // blue; traffic every 50ms; hold 3s. testdata/g03-route/{blue,green}.
  // CONCEPT.md "S3 slice 4 result": status median 0.53s, dataplane 0.05s.
  G03: {
    title: "G03 · live weight flip under traffic",
    gate: true,
    stream: true,
    steps: [
      { say: "h.Apply(g03-route/blue) · weights blue 100 / green 0", lever: "a", flow: "a", hold: 3.0 },
      { say: "h.Apply(g03-route/green) · weights blue 0 / green 100", lever: "b", flow: "b", hold: 3.0 },
      { say: "h.Apply(g03-route/blue) · weights blue 100 / green 0", lever: "a", flow: "a", hold: 3.0 },
      { say: "convergence status median=0.53s · dataplane median=0.05s · transition_err max=0", lever: "a", flow: "a", hold: 2.0 },
    ],
  },
  // examples/mcp/a01_test.go sumIs42; testdata/a01, a01-backend/good;
  // CONCEPT.md "S4 A01 result".
  A01: {
    title: "A01 · MCP through AgentgatewayBackend",
    gate: true,
    steps: [
      { say: "AwaitRoute mcp RouteReady · backend AgentgatewayBackend default/calculator", lever: "a", hold: 1.8 },
      { say: "POST http://mcp.example/mcp · mcp initialize", send: "a", lever: "a", mcp: true, hold: 1.5 },
      { say: "tools/list = [\"sum\"]", send: "a", lever: "a", mcp: true, hold: 1.5 },
      { say: "tools/call sum(40,2) = 42", send: "a", lever: "a", mcp: true, display: "42", hold: 2.4 },
    ],
  },
  // examples/inference/k01_test.go TestK01Routing; testdata/k01 and
  // k01-route/{good,crossed}.
  K01: {
    title: "K01 · three models behind one kronk",
    gate: true,
    steps: [
      { say: "AwaitRoute llm RouteReady · three AgentgatewayBackends to Service default/kronk", lever: "a", model: -1, display: "", hold: 1.8 },
      { say: "POST http://llm.example/m/qwen/ → unsloth/Qwen3-0.6B-Q8_0", send: "a", lever: "a", model: 0, display: "QWEN3", hold: 1.6 },
      { say: "POST http://llm.example/m/smol/ → bartowski/SmolLM2-135M-Instruct-Q8_0", send: "a", lever: "a", model: 1, display: "SMOL2", hold: 1.6 },
      { say: "POST http://llm.example/m/gemma/ → ggml-org/gemma-3-270m-it-Q8_0", send: "a", lever: "a", model: 2, display: "GEMMA", hold: 1.6 },
      { say: "POST http://llm.example/m/unknown/ → 404 route not found", send: "none", lever: "none", model: -1, display: "", hold: 1.4 },
      { say: "h.Apply(testdata/k01-route/crossed) · /m/smol/ → unsloth/Qwen3-0.6B-Q8_0, rejected", send: "a", lever: "a", model: 0, display: "QWEN3", hold: 2.0 },
      { say: "h.Apply(testdata/k01-route/good) · /m/smol/ → bartowski/SmolLM2-135M-Instruct-Q8_0", send: "a", lever: "a", model: 1, display: "SMOL2", hold: 1.6 },
    ],
  },
};

// The lifecycle around every test body, in figure seconds.
export const LIFECYCLE = [
  { lamp: 0, phase: "new", hold: 0.5 },
  { lamp: 1, phase: "validating", hold: 0.6 },
  { lamp: 2, phase: "provisioning", hold: 1.2 },
  { lamp: 3, phase: "wait-usable", hold: 1.0 },
  { lamp: 4, phase: "ready", hold: 0.6 },
  { lamp: 4, phase: "ready", install: true, hold: 2.0 },
];

// What a recorded trace (docs/figure/replay.mjs) needs to know about each
// scenario's bench, by scenario. Only scenarios listed here can be replayed.
// Every entry has the same fields:
// - test: the name the trace header carries;
// - ctors: the constructor the test passes to InstallGateway, by profile
//   (gateway.go EnvoyGateway and AgentGateway);
// - posts: BACKEND_NAME, the name a backend writes into its response body,
//   to the post it stands on;
// - owners: the Deployments whose Pods stand on each post;
// - paths: the post each rule's PathPrefix sends to, for a 2xx whose body
//   names no backend;
// - gate: the route and path the ReferenceGrant gates, or null for none
//   (then the valve stays open);
// - weights: null, or the route whose backendRef weights pick the post, and
//   the post and weights each applied directory sets; then the lever turns
//   when the test applies the route, instead of following each request;
// - mcp: null, or the MCP endpoint path (its requests light the MCP port)
//   and how to read the JSON-RPC answer a response body head carries;
// - parts: the bench part each object the test applies stands on, by
//   kind/namespace/name; objects with no part (Deployments) are left out;
// - kinds: the parts the name panel lists, in pin order, each with the kind
//   of its primary object, the one that heads the part's group;
// - calls: Pods and Services the cluster names, by the call that names them:
//   InstallGateway waits on the controller's Deployment
//   (internal/profile/install.go Install) and Gateway forwards to the proxy's
//   Service and Pod (gateway.go GatewayController.Gateway);
// - install: the part that counts the objects the profile install applies
//   (CRDs, RBAC, the controller), instead of listing them;
// - models: null, or each model id a response body's "model" names, in
//   lamp order, with the tag that its lamp's label and the display show, and
//   the AgentgatewayBackend that pins it;
// - body: the test body's time map (replay.mjs timeMap): how many times
//   slower it plays, its minimum hold per step, and at most how many pulses
//   it draws per second (none means every one).
const CTORS = { "gateway-api": "EnvoyGateway()", "gateway-agentgateway": "AgentGateway()" };
const CART = { calls: { InstallGateway: "cart", Gateway: "cart" }, install: "cart" };

export const REPLAY = {
  G01: {
    test: "TestG01Routing",
    ctors: CTORS,
    // examples/gateway/testdata/g01/orders.yaml and payments.yaml: BACKEND_NAME,
    // and the Deployments, in the default namespace (no namespace set).
    posts: { orders: "a", payments: "b" },
    owners: { "default/orders": "a", "default/payments": "b" },
    // testdata/g01/gateway.yaml: HTTPRoute shop sends /orders and /payments;
    // HTTPRoute payments sends every path of payments.example to payments,
    // which the body names.
    paths: { "/orders": "a", "/payments": "b" },
    gate: null,
    weights: null,
    mcp: null,
    models: null,
    // testdata/g01/gateway.yaml (the Gateway, both HTTPRoutes on the
    // diverter), orders.yaml and payments.yaml (the Services).
    parts: {
      "Gateway/default/edge": "cart",
      "HTTPRoute/default/shop": "div",
      "HTTPRoute/default/payments": "div",
      "Service/default/orders": "a",
      "Service/default/payments": "b",
    },
    kinds: { cart: "Gateway", div: "HTTPRoute", a: "Service", b: "Service" },
    ...CART,
    body: { rate: 20, minHold: 1.2 },
  },
  G02: {
    test: "TestG02ReferenceGrant",
    ctors: CTORS,
    // examples/gateway/testdata/g02/20-orders.yaml and 21-payments.yaml.
    posts: { orders: "a", payments: "b" },
    owners: { "shop/orders": "a", "payments/payments": "b" },
    // examples/gateway/testdata/g02/10-gateway.yaml: HTTPRoute shop/pay sends
    // PathPrefix /payments to the cross-namespace backendRef the grant gates.
    paths: { "/orders": "a", "/payments": "b" },
    gate: { route: "shop/pay", path: "/payments" },
    weights: null,
    mcp: null,
    models: null,
    // testdata/g02/00-namespaces.yaml, 10-gateway.yaml, 20-orders.yaml,
    // 21-payments.yaml and testdata/g02-grant/grant.yaml.
    parts: {
      "Namespace/shop": "div",
      "Namespace/payments": "tray",
      "Gateway/shop/edge": "cart",
      "HTTPRoute/shop/pay": "div",
      "Service/shop/orders": "a",
      "Service/payments/payments": "b",
      "ReferenceGrant/payments/payments": "gate",
    },
    kinds: { cart: "Gateway", div: "HTTPRoute", gate: "ReferenceGrant", a: "Service", b: "Service", tray: "Namespace" },
    ...CART,
    body: { rate: 20, minHold: 1.2 },
  },
  G03: {
    test: "TestG03WeightFlip",
    ctors: CTORS,
    // examples/gateway/testdata/g03/20-blue.yaml and 21-green.yaml.
    posts: { blue: "a", green: "b" },
    owners: { "shop/blue": "a", "shop/green": "b" },
    // testdata/g03-route/*/route.yaml: one rule, PathPrefix /checkout, to both
    // backends; only its weights say which, so a bodyless 2xx has no post.
    paths: {},
    gate: null,
    // testdata/g03-route/blue and green: HTTPRoute shop/checkout with weights
    // 100/0 and 0/100; flips_test.go g03FlipOrder applies blue, green, blue.
    weights: {
      route: "shop/checkout",
      dirs: {
        "testdata/g03-route/blue": { post: "a", text: "weights blue 100 / green 0" },
        "testdata/g03-route/green": { post: "b", text: "weights blue 0 / green 100" },
      },
    },
    mcp: null,
    models: null,
    // testdata/g03/00-namespace.yaml, 10-gateway.yaml, 20-blue.yaml,
    // 21-green.yaml and testdata/g03-route/*/route.yaml.
    parts: {
      "Namespace/shop": "div",
      "Gateway/shop/edge": "cart",
      "HTTPRoute/shop/checkout": "div",
      "Service/shop/blue": "a",
      "Service/shop/green": "b",
    },
    kinds: { cart: "Gateway", div: "HTTPRoute", a: "Service", b: "Service" },
    ...CART,
    // g03_test.go holds each flip 3 s (flips_test.go g03Hold) under traffic
    // every 50 ms (g03TrafficInterval): about 10 s of body and 200 requests.
    // Slowed 20 times that is over 3 minutes, so the body plays at real time
    // and draws at most 4 of its 20 pulses a second.
    body: { rate: 1, minHold: 1.2, cap: 4 },
  },
  A01: {
    test: "TestA01Tools",
    ctors: CTORS,
    // examples/mcp/testdata/a01/calculator.yaml: the calculator Deployment in
    // default. The MCP bodies name no backend, so the path decides.
    posts: { calculator: "a" },
    owners: { "default/calculator": "a" },
    // testdata/a01/gateway.yaml: HTTPRoute mcp sends PathPrefix /mcp to the
    // AgentgatewayBackend calculator.
    paths: { "/mcp": "a" },
    gate: null,
    weights: null,
    // a01_test.go sumIs42 through internal/mcpprobe: initialize, tools/list,
    // tools/call sum(40,2). A response names no method, so the result's own
    // field says which call it answers (MCP spec: InitializeResult has
    // protocolVersion, ListToolsResult tools, CallToolResult content), and
    // the tools/call text is what the calculator's display shows. The SDK's
    // first exchange answers with supportedVersions and gets no method name.
    mcp: {
      path: "/mcp",
      answers: [
        { method: "tools/call", has: '"content":[' },
        { method: "tools/list", has: '"tools":[' },
        { method: "initialize", has: '"protocolVersion"' },
      ],
      display: "tools/call",
    },
    models: null,
    // testdata/a01/gateway.yaml, calculator.yaml and
    // testdata/a01-backend/good/backend.yaml.
    parts: {
      "Gateway/default/edge": "cart",
      "HTTPRoute/default/mcp": "div",
      "AgentgatewayBackend/default/calculator": "a",
      "Service/default/calculator": "a",
    },
    kinds: { cart: "Gateway", div: "HTTPRoute", a: "AgentgatewayBackend" },
    ...CART,
    body: { rate: 20, minHold: 1.2 },
  },
  K01: {
    test: "TestK01Routing",
    ctors: CTORS,
    // examples/inference/testdata/k01/kronk.yaml: one kronk Deployment and
    // Service in default.
    posts: {},
    owners: { "default/kronk": "a" },
    // testdata/k01-route/good.
    paths: { "/m/qwen/": "a", "/m/smol/": "a", "/m/gemma/": "a" },
    gate: null,
    weights: null,
    mcp: null,
    // testdata/k01/backends.yaml: each AgentgatewayBackend pins one model.
    models: {
      "unsloth/Qwen3-0.6B-Q8_0": { tag: "QWEN3", backend: "qwen" },
      "bartowski/SmolLM2-135M-Instruct-Q8_0": { tag: "SMOL2", backend: "smol" },
      "ggml-org/gemma-3-270m-it-Q8_0": { tag: "GEMMA", backend: "gemma" },
    },
    // testdata/k01/gateway.yaml, backends.yaml, kronk.yaml and
    // testdata/k01-route/good.
    parts: {
      "Gateway/default/edge": "cart",
      "HTTPRoute/default/llm": "div",
      "AgentgatewayBackend/default/qwen": "a",
      "AgentgatewayBackend/default/smol": "a",
      "AgentgatewayBackend/default/gemma": "a",
      "Service/default/kronk": "a",
    },
    kinds: { cart: "Gateway", div: "HTTPRoute", a: "Service" },
    ...CART,
    body: { rate: 8, minHold: 1.2 },
  },
};
