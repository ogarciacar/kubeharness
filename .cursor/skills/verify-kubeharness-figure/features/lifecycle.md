# Lifecycle

Every run steps the lamps along the plate's front edge through `new`, `validating`, `provisioning`, `wait-usable` and `ready`. Then the profile cartridge appears above its socket and drops in. During the scenario the `test` lamp is lit. At `cleanup` the cartridge lifts out and fades, and the report line under the drawing shows the full recorded values. Each finished lamp stays filled dim, so the row fills from `new` to `cleanup` like a progress bar, and the whole row stays filled once the run is over.

## Sub-features

- `lamps` lights one lamp per phase, in report-phase order, then `test` and `cleanup`.
- `install` shows the cartridge and lowers it into the socket at step 5, with `h.InstallGateway(…)` in `[data-say]`.
- `cleanup` lifts the cartridge out and hides it.
- `report` shows the complete report line at `cleanup` and `off`. For `gateway-api` it is `README.md`'s recorded run. For `gateway-agentgateway` only `install=` is recorded, so `cluster`, `create`, `ready` and `delete` are absent.
- `progress` keeps every lamp before the lit one filled dim (`[data-lamp][data-done]`), and at `off` all seven carry `data-done` and none `data-on`. On the replay page the lamp a failed run stopped at turns red (`data-fail`): `test` for a `test-failed` cause, else the report's `phase`.

## How to get to it (user POV)

- Click any profile button and any scenario button. Every run starts with the lifecycle.
- Open the page with no parameters. The tour plays every profile and scenario, each with its own lifecycle: G01, G02, G03 under Envoy Gateway, then G01, A01, K01, G02, G03 under agentgateway.

## Driving it with drive.sh

Preconditions:

- Doctor exits 0.

- **Run it.** `scripts/drive.sh lifecycle journeys/lifecycle.json`.
- **Lamps.** The journey waits on `data-phase` `validating`, `provisioning` and `wait-usable`. `lifecycle-validating.png` shows the second lamp lit, and the report eval reads `kubeharness cluster=kh-7630e8a319a4-1 phase=validating`.
- **Install.** At step 5 the say eval starts `h.InstallGateway(kubeharness.EnvoyGateway())`, and the cartridge opacity eval reads `1`. `lifecycle-install.png` shows the cartridge seated.
- **Cleanup.** At `data-phase=cleanup` the report eval reads `kubeharness cluster=kh-7630e8a319a4-1 phase=ready cause=ok cleanup=succeeded remaining=0 create=10.7s ready=17.4s delete=390ms profile=gateway-api install=15.7s`. `lifecycle-cleanup.png` shows the cartridge lifted.
- **Off.** The lit-lamp count eval reads `0`. The journey then waits `until` the lamp row reads `done` seven times, and the last eval prints that row. `lifecycle-off.png` shows an empty socket.
- **Motion.** `scripts/record.sh gateway-api G01` films the drop and the lift. In `.phases.txt`, `new` should be 12 frames at 24 fps.

## Gotchas

- The cartridge fades in before it drops. A shot taken right as step 5 begins can catch it half transparent, so the journey waits 1.2 s.
- `[data-report]` shows only `phase=` (plus `profile=` after install) until cleanup. That is deliberate. The real report line prints once, at the end of the run.
- The figure's lifecycle runs in seconds. The report line's durations are the recorded ones, not timers.
