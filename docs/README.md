# Astronomer documentation map

Current operator, API, architecture and runbook guidance for the supported
release line. [CURRENT.md](../advisor-plans/CURRENT.md) is the sole working plan.
Historical plans and evidence are preserved under
[`advisor-plans/archive/`](../advisor-plans/archive/); their old instructions
and completion claims are historical.

## Start here

- [Resume development on a new machine](resuming-development.md)
- [Architecture decision records](decisions/index.md)
- [Threat model](threat-model.md)
- [Control-plane state and ownership](control-plane-state-contract.md)
- [Flux-native delivery decision](architecture/decisions/flux-native-delivery.md)
- [Cluster registration API](cluster-registration-api.md)
- [CRD API](crd-api.md)
- [Secret handling policy](secret-handling-policy.md)
- [Local telemetry stack](local-telemetry.md)
- [Air-gapped installation](airgapped-install.md)
- [Runbook index](runbooks/README.md)

## Generated contracts

- `openapi.yaml` is the HTTP contract; `make generate` regenerates the embedded
  spec, the frontend types/client and the Go SDK from it (until ADR-0007
  replaces it with Connect-RPC).
