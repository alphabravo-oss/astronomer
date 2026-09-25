# Offering qualification fixtures

`cases.json` is the machine-readable Plan 028 denominator. It contains every
case ID in `advisor-plans/028-offering-test-inventory.md` and the initial
runtime registry contracts. The offline test fails if the document and
manifest diverge.

Copy `config.example.json` outside the repository and fill in explicit,
task-owned target IDs. `member_targets` must name at least two distinct member
clusters, projects and namespaces, together with each expected agent privilege
profile. `token_file` must point to an owner-only file; credentials must never
be embedded in the config or evidence.

Before any mutating qualification case, prove the estate through public GET
APIs:

```sh
go run ./scripts/qualify-offerings preflight \
  --config /secure/path/qualification-config.json \
  --output /tmp/offering-qualification/estate-preflight.json
```

The preflight fails closed unless at least two distinct targets are active,
non-local, fully registered, fresh-heartbeat members whose project, namespace
and privilege profile match the explicit configuration. A blocked preflight
writes schema-valid evidence and no mutation is attempted.

Run the read-only inventory gate from the repository root:

```sh
mkdir -p /tmp/offering-qualification
go run ./scripts/qualify-offerings inventory \
  --config /secure/path/qualification-config.json \
  --output /tmp/offering-qualification/inventory.json
```

Inventory success only proves that the frozen source list matches all returned
API pages. Every functional case remains `NOT_RUN`; the comprehensive `verify`
command rejects that report.
