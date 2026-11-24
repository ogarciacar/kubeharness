# A01 MCP

Under agentgateway only, the back post is the calculator MCP server. The cartridge's MCP port lights while MCP requests run. The steps follow `sumIs42` in `examples/mcp/a01_test.go`: `mcp initialize`, `tools/list = ["sum"]`, `tools/call sum(40,2) = 42`. After the call, the calculator post's display window shows `42`.

## Sub-features

- `route` shows `AwaitRoute mcp RouteReady · backend AgentgatewayBackend default/calculator`.
- `mcp-port` lights the agentgateway cartridge's MCP boss during the MCP steps.
- `tools-list` shows `tools/list = ["sum"]`.
- `sum` delivers the call to the back post and puts `42` on its display.
- `one-backend` hides the front post, its line, the diverter's `b` outlet, the gate valve and the tray, and brings them back in the next two-backend scenario.

## How to get to it (user POV)

- Click `agentgateway`, then `A01`.
- Click `A01` under Envoy Gateway. The profile switches to agentgateway.

## Driving it with drive.sh

Preconditions:

- Doctor exits 0.

- **Run it.** `scripts/drive.sh a01-mcp journeys/a01-mcp.json`.
- **MCP port.** At step 7 the `.kh-mcp` eval reads `true`, and the say eval reads `POST http://mcp.example/mcp · mcp initialize`.
- **Sum.** At step 9 the journey waits for `[data-post="postA"][data-on]`. The say eval reads `tools/call sum(40,2) = 42`, and the display eval reads `42`. `a01-sum-42.png` shows `42` in the window on the post's front face.
- **One backend.** Before that shot, the journey waits `until` none of the `postB.*`, `gate.*`, `div.b`, `tray` and line `b` parts is shown, so a broken hide rule fails the drive. The eval after it reads `hidden parts shown 0 of 19`. `a01-sum-42.png` shows one line from the diverter to the back post, and no front post, valve or tray.
- **Report.** At `off` the report eval reads `kubeharness phase=ready cause=ok cleanup=succeeded remaining=0 profile=gateway-agentgateway install=6.2s`.
- **Back to two backends.** After `off` the journey presses `ArrowRight` twice. The scenario becomes `K01`, which also has one backend, then `G01`. The journey waits `until` every one of those parts is shown again, the eval reads `hidden parts shown 19 of 19`, and `a01-then-g01.png` shows the front post, valve and tray again.
- **Video.** `scripts/record.sh gateway-agentgateway A01` films the whole run.

## Gotchas

- The display keeps `42` until cleanup. Its presence alone doesn't prove the call step ran, so pair it with the step-9 say eval.
- A01 has one backend (`BACKENDS.A01.b` is `null` in `model.mjs`), so `.kh-bench` carries `data-one-backend` and the CSS hides the front post, its line, the diverter's `b` outlet, the gate valve and the tray. Any scenario whose `BACKENDS` entry has `b: null` gets the same bench.
