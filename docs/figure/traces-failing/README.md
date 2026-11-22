# A failing run

`G02-gateway-api.jsonl` is a real `TestG02ReferenceGrant/gateway-api` run with a broken fixture, recorded to prove the replay's red check box and FAIL badge. Before the run, `examples/gateway/testdata/g02-grant` had `namespace: shop` in its `from` changed to `namespace: shop-broken`. The grant then admits no route, so the `AwaitRoute shop/pay` after the apply never sees `ResolvedRefs=True`, returns with `failed`, and the report reads `cause=test-failed`. The fixture was restored right after the run.

```sh
cd examples/gateway
go test -tags e2e -count=1 -run '^TestG02ReferenceGrant$/^gateway-api$' -v -artifacts
```

It lives outside `traces/` because `build.mjs --traces traces` takes one trace per profile and scenario. Build it on its own with `node build.mjs --traces traces-failing`, then rebuild from `traces`.
