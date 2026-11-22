# A01 MCP

Under agentgateway only, the back post is the calculator MCP server. The cartridge's MCP port lights while MCP requests run. The steps follow `sumIs42` in `examples/mcp/a01_test.go`: `mcp initialize`, `tools/list = ["sum"]`, `tools/call sum(40,2) = 42`. After the call, the calculator post's display window shows `42`.

## Sub-features

- `route` shows `AwaitRoute mcp RouteReady · backend AgentgatewayBackend default/calculator`.
- `mcp-port` lights the agentgateway cartridge's MCP boss during the MCP steps.
- `tools-list` shows `tools/list = ["sum"]`.
- `sum` delivers the call to the back post and puts `42` on its display.

## How to get to it (user POV)

- Click `agentgateway`, then `A01`.
- Click `A01` under Envoy Gateway. The profile switches to agentgateway.

## Driving it with drive.sh

Preconditions:

- Doctor exits 0.

- **Run it.** `scripts/drive.sh a01-mcp journeys/a01-mcp.json`.
- **MCP port.** At step 7 the `.kh-mcp` eval reads `true`, and the say eval reads `POST http://mcp.example/mcp · mcp initialize`.
- **Sum.** At step 9 the journey waits for `[data-post="postA"][data-on]`. The say eval reads `tools/call sum(40,2) = 42`, and the display eval reads `42`. `a01-sum-42.png` shows `42` in the window on the post's front face.
- **Report.** At `off` the report eval reads `kubeharness phase=ready cause=ok cleanup=succeeded remaining=0 profile=gateway-agentgateway install=6.2s`.
- **Video.** `scripts/record.sh gateway-agentgateway A01` films the whole run.

## Gotchas

- The display keeps `42` until cleanup. Its presence alone doesn't prove the call step ran, so pair it with the step-9 say eval.
- The front post and gate valve are idle in A01. That is expected, because A01 has one backend.
