import { readFileSync } from "node:fs";
import { modelIn } from "../replay.mjs";

const inference = new URL("../../../examples/inference/", import.meta.url);

/** `{ alias: backend }`: the AgentgatewayBackend each `/m/<alias>/` rule of an applied route directory sends to. */
export function routeOf(dir) {
  const yaml = readFileSync(new URL(`${dir}/route.yaml`, inference), "utf8");
  const rules = [...yaml.matchAll(/value: \/m\/([^/]+)\/\}\}\]\s*backendRefs: \[\{[^}]*\bname: ([\w-]+)\}\]/g)];
  if (!rules.length) throw new Error(`${dir}/route.yaml: no /m/<alias>/ rule with a backendRef`);
  return Object.fromEntries(rules.map(([, alias, backend]) => [alias, backend]));
}

/**
 * Each chat answer in a K01 trace, in order: `{ alias, dir, id, want }`, where
 * `id` is the model the body names and `want` the backend that the route
 * applied before the request sends `/m/<alias>/` to.
 */
export function answersOf(events) {
  const asked = new Map();
  let dir;
  for (const e of events) {
    if (e.ev === "call" && e.fn === "Apply" && e.arg.startsWith("testdata/k01-route/")) dir = e.arg;
    if (e.ev === "request") asked.set(e.id, { alias: /^\/m\/([^/]+)\//.exec(new URL(e.url).pathname)?.[1], dir });
  }
  return events.filter((e) => e.ev === "body" && modelIn(e.head)).map((e) => {
    const { alias, dir } = asked.get(e.id);
    return { alias, dir, id: modelIn(e.head), want: routeOf(dir)[alias] };
  });
}
