# Astronomer documentation map

The root of `docs/` contains current product, operator, API, architecture, and
runbook guidance for the supported release line. Documents under
`docs/archive/` describe superseded pre-v1 behavior and must not be used as an
operator contract. Documents under `docs/plans/` are proposals or execution
history; accepted architecture lives under `docs/architecture/decisions/`.

## Start here

- [Product boundary and capability comparison](rancher-astronomer-comparison.md)
- [Control-plane state and ownership](control-plane-state-contract.md)
- [Release compatibility](architecture/compatibility.md)
- [Flux-native delivery decision](architecture/decisions/flux-native-delivery.md)
- [Cluster registration API](cluster-registration-api.md)
- [CRD API](crd-api.md)
- [Threat model](threat-model.md)
- [Secret handling policy](secret-handling-policy.md)
- [Enterprise ownership and review boundaries](engineering-ownership.md)
- [Accessibility release-candidate checklist](accessibility-release-checklist.md)
- [Test flake and quarantine policy](test-flake-policy.md)
- [Runbook index](runbooks/README.md)

## Generated contracts

Do not edit generated contract pages by hand:

- `architecture/compatibility.md` comes from
  `deploy/release/compatibility.yaml`.
- `openapi.yaml`, `routes.json`, and `generated-route-inventory.json` are the
  HTTP contract and executable-route inventories.
- `rancher-quality-phase0-operation-task-inventory.md` and
  `direct-enqueue-classifications.json` are the task ownership inventories.
- `error-codes.md` is generated from the API error catalog.

`node scripts/check-docs.mjs` validates relative links, current/archive
classification, and legacy delivery terminology. It is part of the enterprise
release gate.
