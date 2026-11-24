# G01 routing

The diverter's lever is the HTTPRoute. For each of `g01Routes` in `examples/gateway/gateway_test.go`, a pulse runs from the test stand through the port-forward and the cartridge to the diverter. The lever swings to the back line (`orders`) or the front line (`payments`), and the post the request reaches lights its lamp. A request no rule matches stops at the diverter, and the diverter flashes.

## Sub-features

- `orders` sends `shop.example/orders` and `/orders/42` to the back post.
- `payments` sends `shop.example/payments` and `payments.example/refunds` to the front post.
- `no-backend` stops `shop.example/unrouted` and `unknown.example/orders` at the diverter.

## How to get to it (user POV)

- Click a profile, then `G01`.
- Press `←` or `→` with the figure focused until the readout shows `G01`. The keys cycle `G01`, `G02`, `G03`, `A01`, `K01`. Passing `A01` or `K01` switches the profile to agentgateway, and it stays there when the cycle returns to `G01`.

## Driving it with drive.sh

Preconditions:

- Doctor exits 0.

- **Run it.** `scripts/drive.sh g01-routing journeys/g01-routing.json`. Add `--light` for the light theme.
- **Orders.** At step 7 the journey waits for `[data-post="postA"][data-on]`, and the say eval reads `GET http://shop.example/orders → orders`. `g01-orders.png` shows the back post's lamp lit and the lever toward the back line.
- **Payments.** At step 9 it waits for `[data-post="postB"][data-on]`, and the say eval reads `GET http://shop.example/payments → payments`.
- **No backend.** At step 11 it waits for `[data-kh="div"][data-block]`, and the say eval reads `GET http://shop.example/unrouted → no backend`. The last two evals count hot chunks on lines `a` and `b`, and both read `0`.
- **Both profiles.** Edit the first click in a copy of the journey to `gateway-agentgateway` and run it again. The steps are the same.

## Gotchas

- Post lamps flash for 0.5 s after a pulse arrives. Wait on the attribute with `until`. A late shot misses it.
- The lever eases toward its target over about half a second, so a shot taken at the very start of a step can show it mid-swing.
