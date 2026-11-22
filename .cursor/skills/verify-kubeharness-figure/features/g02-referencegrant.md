# G02 ReferenceGrant

The front post stands on a second namespace tray, and the gate valve on its line is the ReferenceGrant. With no grant the valve is closed: `[data-say]` shows `RefNotPermitted`, and a burst of 5 requests stops at the valve. Applying the grant raises the valve stem and requests reach `payments`. Deleting the grant closes the valve again. The sequence follows `TestG02ReferenceGrant`.

## Sub-features

- `denied` shows the closed valve and `route shop/pay ResolvedRefs=False reason=RefNotPermitted`.
- `burst-blocked` stops 5 pulses at the valve and shows `payments counter flat`.
- `grant` raises the stem on `h.Apply(testdata/g02-grant)`.
- `allowed` delivers `/payments` to the front post with `counter rises`.
- `revoke` closes the valve again on `h.Delete(testdata/g02-grant)`.

## How to get to it (user POV)

- Click a profile, then `G02`.

## Driving it with drive.sh

Preconditions:

- Doctor exits 0.

- **Run it.** `scripts/drive.sh g02-referencegrant journeys/g02-referencegrant.json`.
- **Denied.** At step 6 the say eval reads `route shop/pay ResolvedRefs=False reason=RefNotPermitted`, and the gate transform eval reads `translate(0.00 -0.00)`.
- **Burst blocked.** At step 8 the journey waits for `[data-kh="gate"][data-block]`. The say eval reads `burst of 5 to /payments · payments counter flat`. `g02-blocked-at-gate.png` shows pulses on the front line ending at the flashing valve.
- **Allowed.** At step 10 it waits for `[data-post="postB"][data-on]`. The say eval reads `route shop/pay RouteReady · GET /payments → payments, counter rises`, and the gate transform has a non-zero y (`translate(0.00 -11.10)` on the default build).
- **Revoked.** At step 12 the valve blocks again, and the gate transform is back to `translate(0.00 -0.00)`.
- **Report.** At `off` the report eval reads the full `gateway-api` line ending `install=15.7s`.

## Gotchas

- The stem's y offset depends on the camera and the stroke in `build.mjs`. Assert zero versus non-zero, not the exact number.
- The tray outline is highlighted only during G02. It is part of the explanation, not a fault.
