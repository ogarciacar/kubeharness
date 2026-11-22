# 36. A usable cluster resolves Service names

Date: 2026-11-22

## Status

Accepted.

## Context

`New` returned once the API server answered, every node was Ready and the default ServiceAccount existed. On a fresh Kind cluster CoreDNS is still starting at that moment. A probe that read `kube-system` right after `New` returned found CoreDNS at 0 of 2 available replicas and the `kube-dns` Service with no ready endpoint, in each of 4 parallel clusters and in a single cluster alone.

Most tests survived the gap because their first rollout takes longer than CoreDNS needs. The M01 migration Job does not: it runs `psql -h postgres` right after the database rollout with `backoffLimit: 0`. On CI it failed with `could not translate host name "postgres" to address: Try again` in 3 of 5 runs of one PR, in `TestCreateOrder` and `TestHelperBrokenMigration`.

## Decision

The `wait-usable` phase also waits until the `kube-dns` Service in `kube-system` has at least one ready endpoint, read from its EndpointSlices. Until then the readiness observation reads `cluster DNS not ready: kube-dns has no ready endpoint`, and a readiness timeout reports it as the last observation, as it does for nodes and the ServiceAccount.

## Consequences

A test can resolve a Service name from its first Pod. Setup takes about a second longer: `ready=16.2s` before and `ready=17.2s` after, one local run each. The check names Kind's CoreDNS Service, `kube-dns`, which every supported Kind node image deploys; a provider without it would never become usable and would fail at the readiness timeout with that message.
