# Offering qualification fixtures

`cases.json` is the machine-readable Plan 028 denominator. It contains every
case ID in `advisor-plans/028-offering-test-inventory.md` and the initial
runtime registry contracts. The offline test fails if the document and
manifest diverge.

Copy `config.example.json` outside the repository and fill in explicit,
task-owned target IDs. `token_file` must point to an owner-only file; credentials
must never be embedded in the config or evidence.

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
