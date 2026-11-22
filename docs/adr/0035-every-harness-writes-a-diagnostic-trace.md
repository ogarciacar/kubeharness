# 35. Every Harness writes a diagnostic trace

Date: 2026-11-20

## Status

Accepted. Applies [12](0012-cleanup-and-diagnostics-are-core.md). Does not conflict with [13](0013-go-library-only-no-cli-or-schema.md), for the reason given under Decision.

## Context

The figure in `docs/figure` plays scripted steps. The goal is a video of what one real `examples/gateway` `TestG02ReferenceGrant` run does to its cluster: the cluster name, the lifecycle phases, the profile install, the Pods, the route conditions, the requests, the ReferenceGrant apply and delete, and cleanup, all at real relative times. A trace of the same run also helps a person read a failure after the fact.

The test log cannot carry this. `testing` buffers `t.Log` until the test ends, so it holds no timings, and a killed run never prints it. `docs/figure/trace-design.md` considered and rejected three other sources:

- An outside observer of Docker events and proxy access logs. It reconstructs four boundaries from report durations, sees requests only through logs that agentgateway may not write, and needs its own module and goroutines.
- A switch of kubeharness's own, such as an env var, a build tag, or a flag. It adds a knob and a second mode, when `go test -artifacts` already decides whether a test's files survive.
- A wrapper around `Gateway.Client()` in the test. It misses `New`'s phases and every `AwaitResponse` attempt, and it edits the test the video claims to show.

The constraints were no exported API change, no secrets in the file, no measurable slowdown, and one trace per cluster.

## Decision

`New` writes `kubeharness-trace-<cluster>.jsonl` into `t.ArtifactDir()`. `go test -artifacts` keeps that directory under `-outputdir`. Without the flag, Go deletes it when the test ends. kubeharness adds no env var, flag, or build tag, and no exported identifier changes. A trace that cannot be created is logged and skipped, and a failed write disables the trace. Neither fails the test.

The file is JSON Lines with schema version 1. The first line is `{"at":0,"ev":"trace","v":1,...}` with the test name, the cluster name, and the start time. Every line has `at`, the milliseconds since that start on the monotonic clock, stamped under the one lock that serializes writes, so lines are in time order. Each line is one write, so a killed run leaves whole lines that stop at the call that hung. The event kinds are `trace`, `call`, `return`, `phase`, `wait`, `object`, `pod`, `forward`, `request`, `response`, `body`, and `report`, with the fields listed in `docs/figure/trace-design.md`. `in` names the enclosing call and is omitted outside one, as for `Gateway.Client()` requests. A `pod` line's `owner` is kind-qualified, such as `Deployment/shop/orders` or `Service/payments/payments`, in the same form as an `object` line's `obj`. Version 1 later gained an optional `api` field on `object` and `pod` lines: the object's apiVersion, `group/version` such as `gateway.networking.k8s.io/v1`, or `v1` for a core kind such as a Pod. It is additive, so the version stays 1, and a trace written before it simply lacks the field.

`internal/trace` is the only writer and the only place that redacts. It uses only the standard library, so `internal/lifecycle` can import it. A URL is cut to scheme, host, and path. Free text is collapsed to one line, every URL in it is cut the same way, the kubeconfig path is replaced, and the result is capped at 200 characters. Request headers, query strings, userinfo, request bodies, kubeconfig contents, and failure messages, which carry Pod logs, never enter the file. A response body appears only as the first 200 bytes the caller read, which is what a failure message already quotes.

`internal/trace/testdata/golden.jsonl` holds one line of every event kind. A test regenerates it with `-update` and checks it otherwise. A field that changes meaning bumps the version. kubeharness never reads a trace back.

D13 rules out a CLI, a YAML configuration schema, and a plug-in SDK: a second product, and a configuration language that users write and the library reads. The trace's schema is the opposite direction. It describes a file the library writes and never reads, and it configures nothing. It is a diagnostic artifact in the sense of D12, next to the report line, with a version so that `docs/figure` can reject a file it does not understand.

## Consequences

Anyone can rebuild and refilm the figure from a committed trace without Docker. `docs/figure/traces/` holds one recorded trace per profile of `TestG02ReferenceGrant`.

Every e2e run writes the file, and Go deletes it unless `-artifacts` is set. The recorded G02 traces are 14,442 bytes in 147 lines for `gateway-api` and 12,733 bytes in 135 lines for `gateway-agentgateway`.

`WaitDeployment` lists the Deployment's Pods once when its rollout is done, so the trace names them. That is one API call per wait, made only while a trace records.

`Gateway.Client().Transport` is now a recording wrapper rather than a `*http.Transport`. Its docs never promised the concrete type. The base transport is unchanged, so each await attempt still clones it.

The trace cannot see requests that a test sends straight to a `Forward` address, such as G02's payments-counter reads.

A new event kind needs a golden line. The figure side is meant to parse that file, so the new kind also needs figure support.
