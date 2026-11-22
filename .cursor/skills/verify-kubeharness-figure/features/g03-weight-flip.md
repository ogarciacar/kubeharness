# G03 weight flip

Traffic runs continuously from the test stand. The diverter's lever is the HTTPRoute weight. It points at the back line for blue 100 / green 0 and at the front line for blue 0 / green 100, and flips blue, green, blue as in `g03FlipOrder`. The last step shows the measured convergence from `CONCEPT.md`.

## Sub-features

- `blue` sends the stream to the back post.
- `green` sends the stream to the front post.
- `blue-again` sends it back to the back post.
- `convergence` shows `status median=0.53s · dataplane median=0.05s · transition_err max=0`.

## How to get to it (user POV)

- Click a profile, then `G03`.

## Driving it with drive.sh

Preconditions:

- Doctor exits 0.

- **Run it.** `scripts/drive.sh g03-weight-flip journeys/g03-weight-flip.json`.
- **Blue.** At step 6 the say eval reads `h.Apply(g03-route/blue) · weights blue 100 / green 0`. The hot-chunk evals read a positive count on `a` and `0` on `b`.
- **Green.** At step 7 the say eval reads `h.Apply(g03-route/green) · weights blue 0 / green 100`. The hot counts flip: `0` on `a` and positive on `b`.
- **Blue again.** At step 8 the stream is back on `a`.
- **Convergence.** At step 9 the say eval reads `convergence status median=0.53s · dataplane median=0.05s · transition_err max=0`.
- **Motion.** `scripts/record.sh gateway-api G03` shows the lever swinging under traffic.

## Gotchas

- Pulses emitted just before a flip are still in flight on the old line, so right after the step changes both lines can be hot. The journey waits 0.9 s before counting.
- The real test sends a request every 50 ms. The figure sends one every 0.28 s so the pulses can be seen. The say text keeps the real numbers.
