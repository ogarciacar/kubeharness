# Profile switch

The profile buttons swap the cartridge in the socket. The Envoy Gateway cartridge (`gateway-api`) has one large knurled knob on top. The agentgateway cartridge (`gateway-agentgateway`) has a smaller knob and an MCP port boss. G01, G02 and G03 run under both profiles with the same lines and posts. A01 and K01 exist only under agentgateway.

## Sub-features

- `envoy` draws the Envoy Gateway top and puts `gateway-api` in the readout and report line.
- `agentgateway` draws the agentgateway top and puts `gateway-agentgateway` in the readout and report line.
- `a01-forces-agentgateway` switches to agentgateway when A01 or K01 is chosen under Envoy Gateway. `profileFor` in `live.js` picks the only profile whose `model.mjs` `PROFILES` entry lists the scenario.

## How to get to it (user POV)

- Click `Envoy Gateway` or `agentgateway`.
- Press `p` with the figure focused.
- Click `A01` or `K01` while Envoy Gateway is selected.
- Press `→` from `G03` under Envoy Gateway. The scenarios cycle `G01`, `G02`, `G03`, `A01`, `K01`, so the next one is `A01` and the profile becomes agentgateway. It stays agentgateway when the cycle returns to `G01`.

## Driving it with drive.sh

Preconditions:

- Doctor exits 0.

- **Run it.** `scripts/drive.sh profile-switch journeys/profile-switch.json`.
- **Envoy.** After the clicks, at step 6, the `.kh-bench` eval reads `gateway-api`, and `profile-envoy.png` shows the single large knob.
- **agentgateway.** After clicking `agentgateway`, at step 6, the eval reads `gateway-agentgateway`, and `profile-agentgateway.png` shows the small knob and the MCP boss.
- **A01 forces the profile.** Click `Envoy Gateway`, then `A01`. The state eval reads `"profile":"gateway-agentgateway","scenario":"A01"`. The [K01 models](./k01-models.md) journey proves the same for `K01`: it clicks `Envoy Gateway`, then `K01`, and waits for `gateway-agentgateway`.

## Gotchas

- The cartridge is hidden in the lifecycle steps before install (0–4). A profile shot before step 5 shows an empty socket.
- `p` on A01 or K01 keeps agentgateway and only restarts the run, since no other profile lists them.
- Switching the profile restarts the run. It does not swap the cartridge mid-test, matching kubeharness, where a second `InstallGateway` with another profile fails the test.
