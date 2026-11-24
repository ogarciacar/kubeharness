# K01 models

Under agentgateway only, the back post is one kronk Pod that serves three open-weight models. The post carries three small lamps above a display window. Each chat request the route sends to kronk lights the lamp of the model that answered and writes that model's tag in the window: `QWEN3`, `SMOL2` or `GEMMA`. The bench has one backend, so the front post, its line, the diverter's `b` outlet, the gate valve and the tray are not drawn. The steps follow `TestK01Routing` in `examples/inference/k01_test.go`: one request per alias, a request to `/m/unknown/` that no rule matches, `/m/smol/` under a crossed route that sends it to Qwen, then `/m/smol/` again under the good route.

## Sub-features

- `route` shows `AwaitRoute llm RouteReady · three AgentgatewayBackends to Service default/kronk` with no lamp lit and a blank display.
- `lamps` lights one lamp per answer, in the order of `model.mjs` `REPLAY.K01.models`: `/m/qwen/` lights lamp 0 with `QWEN3`, `/m/smol/` lamp 1 with `SMOL2`, `/m/gemma/` lamp 2 with `GEMMA`. Each request reaches the back post.
- `unknown` stops `/m/unknown/` at the diverter, with no lamp lit and a blank display.
- `crossed` shows `h.Apply(testdata/k01-route/crossed)`: `/m/smol/` lights the Qwen lamp with `QWEN3`, and the say line ends `rejected`.
- `restored` shows `h.Apply(testdata/k01-route/good)`: `/m/smol/` lights the SmolLM2 lamp again.
- `one-backend` hides the front post, its line, the diverter's `b` outlet, the gate valve and the tray, and brings them back in the next two-backend scenario.
- `lamps-only-in-k01` draws the three lamps only while `.kh-bench` carries `data-models`. Other scenarios show none.

## How to get to it (user POV)

- Click `agentgateway`, then `K01`.
- Click `K01` under Envoy Gateway. The profile switches to agentgateway.
- Press `→` from `A01` with the figure focused.
- Open the page with no parameters and wait. The tour plays K01 after A01.

## Driving it with drive.sh

Preconditions:

- Doctor exits 0.

- **Run it.** `scripts/drive.sh k01-models journeys/k01-models.json`. Add `--light` for the light theme. It takes under 30 s.
- **Profile.** The journey clicks `Envoy Gateway`, then `K01`, and waits `until` `.kh-stage` reads `gateway-agentgateway` and `K01`.
- **Bench.** It waits `until` `.kh-bench` carries `data-models` and `data-one-backend`, three `[data-model]` lamps are drawn, and none of the `postB.*`, `gate.*`, `div.b`, `tray` and line `b` parts is shown. The eval reads `hidden parts shown 0 of 19`. It waits on the hidden parts again at step 12.
- **Lamps.** Each step is one `until` on `data-step`, the lamp row, the display and the say line together, so a wrong lamp, a wrong tag or a wrong step fails the drive. Each eval after it prints the say line, the row and the display.

  | Step | Lamp row | Display | Say line has | Shot |
  | --- | --- | --- | --- | --- |
  | 6 | `000` | blank | `AwaitRoute llm RouteReady` | |
  | 7 | `100` | `QWEN3` | `/m/qwen/ → unsloth/Qwen3-0.6B-Q8_0` | `k01-qwen.png` |
  | 8 | `010` | `SMOL2` | `/m/smol/ → bartowski/SmolLM2-135M-Instruct-Q8_0` | `k01-smol.png` |
  | 9 | `001` | `GEMMA` | `/m/gemma/ → ggml-org/gemma-3-270m-it-Q8_0` | `k01-gemma.png` |
  | 10 | `000` | blank | `/m/unknown/ → 404 route not found` | `k01-unknown.png` |
  | 11 | `100` | `QWEN3` | `h.Apply(testdata/k01-route/crossed) · /m/smol/ → unsloth/Qwen3-0.6B-Q8_0, rejected` | `k01-crossed.png` |
  | 12 | `010` | `SMOL2` | `h.Apply(testdata/k01-route/good) · /m/smol/ → bartowski/SmolLM2-135M-Instruct-Q8_0` | `k01-restored.png` |

- **Posts.** At steps 7, 8 and 9 the journey also waits for `[data-post="postA"][data-on]`, and at step 10 for `[data-kh="div"][data-block]`. Each shot shows one line from the diverter to the back post, the lit lamp, and the tag in the window.
- **Cleanup.** It waits `until` `data-phase` is `cleanup` with the row `000` and a blank display.
- **Report.** It waits `until` `data-phase` is `off` and the report line reads `kubeharness phase=ready cause=ok cleanup=succeeded remaining=0 profile=gateway-agentgateway install=6.3s`.
- **Back to two backends.** The journey presses `ArrowRight`. The scenario becomes `G01`. It waits `until` `.kh-bench` carries neither `data-models` nor `data-one-backend`, no model lamp is drawn and all the hidden parts are shown. The eval reads `hidden parts shown 19 of 19 · model lamps drawn 0`, and `k01-then-g01.png` shows the front post, valve and tray again and no lamps on the back post.
- **Recorded run.** [Replay](./replay.md) plays the real `TestK01Routing` trace with the same lamps. `scripts/drive.sh replay --replay journeys/replay.json` drives it, and `scripts/record.sh gateway-agentgateway K01 --replay` films it with both panels (46.6 s).
- **Video of the scripted scene.** `scripts/record.sh gateway-agentgateway K01` films this page. Use it to check the drawing, not as proof of a real run.

## Gotchas

- The lamp shows the model named in the response, which is the id that the routed AgentgatewayBackend pins. In the crossed step the Qwen lamp under `/m/smol/` is the expected picture. The test rejects that answer, and the say line says so.
- On this page the lamp and tag stay lit from one request step to the next, and clear at `/m/unknown/` and at cleanup. On the replay page they also go dark while the test applies a route or waits for it.
- The post lamp flashes for 0.5 s after a pulse arrives, so wait on the attribute with `until`. The model lamp and the tag hold for the whole step.
- The lamp row counts `[data-model]` elements in document order, which is slot order `0`, `1`, `2`, left to right on the post.
- The hide check counts 19 parts. The number is the drawing's, so a change to the front post, the valve or line `b` in `build.mjs` changes it. The journey waits on none shown and on all shown, and only the eval prints the count.
- The tags come from `model.mjs` `REPLAY.K01.models` and hold at most five characters, which is what the window fits. `tools/replay-check.mjs` checks that.
