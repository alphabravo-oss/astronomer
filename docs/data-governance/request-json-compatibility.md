# Request JSON compatibility

The canonical reader is `internal/jsoncontract.DecodeRequest`. It reads at most
32 MiB, retains narrower route limits, and accepts exactly one document. The
REST middleware still supplies the existing 30 second socket read deadline;
this reader also enforces the byte bound when a handler runs without middleware.
An empty optional body returns EOF. Top-level JSON null is not an object DTO.
Malformed input is rejected before ordinary product mutations. The shell elevation adapter is a deliberate exception: malformed or empty input denies elevation while the existing authorized read-only session creation remains available. Its actual handler fixture verifies that provisioned Roles contain no write verbs. Optional mutation bodies are inspected even with unknown/chunked content length; an empty EOF retains route defaults.

## Published v1 clients

Previously permissive v1 handlers use the explicit `AdditiveV1` adapter.
Additional properties remain ignored at typed DTO boundaries, rather than being
persisted or interpreted as authority. The CreateUser fixture includes both a
`displayName` property and an additional nested object: it still creates the
same user and defaults the username to the email. Well-typed published requests
retain their field names and additional-property behavior. Inhibition input
hardening deliberately rejects blank equality labels, missing/null matcher
values and explicit null regex flags formerly normalized to Go zero values;
the dated [contract review](../api-breaking-change-review.md) and actual
[released-handler characterization](../assurance/technology-guide/w09/inhibition-contract-review.md)
disclose these changes. Empty matcher values and omitted regex=false remain
valid. The published users schemas describe objects without
`additionalProperties: false`; strict rejection would therefore change the v1
contract. Existing exact readers continue using `ExactProduct`.

SCIM documents, extension data and raw Kubernetes fields are classified
separately in `json-contracts.json`. A product wrapper can be exact while a
nested `json.RawMessage`, map, or provider-defined object retains all producer
properties. This change checks JSON syntax and document boundaries; it does
not apply a product DTO schema to arbitrary Kubernetes or provider content.

## Partial updates

The user PUT/PATCH adapter retains its existing semantics: omitted, null,
empty and whitespace-only email/username values preserve the locked current
row; omitted/null/empty first and last names preserve current values. An
omitted or null `is_active` preserves its current value. Explicit booleans retain
meaning, including false and the existing transactional session revocation.
User strings do not acquire a new removal operation. Required create fields
remain required; email validation returns safe field names and rule categories,
without echoing request values or passwords.

Empty arrays and objects in other product DTOs retain their existing domain
meaning. For example, preference arrays clear their respective selections and
an empty delivery placement selects nothing; these meanings are defined by
the domain validators, not by this request reader. The existing preference and
delivery tests continue running alongside new user patch fixtures.

## Governance and limits of evidence

`tools/json-governance` inventories import-bound JSON calls and canonical sqlc
JSON columns. `json-contracts.json` gives each discovered request owner an
explicit unknown-field policy, and each persistent column a schema owner and
reader policy. `check-json-governance.py` also prevents a new raw handler decoder
from bypassing the canonical reader. Its four stored/response exceptions bind
exact functions and expressions rather than granting a file-wide exception.

This syntax inventory is not whole-program dataflow proof. Canonical SQL,
runtime registry coverage, migration tests, domain behavior tests, and actual
PostgreSQL qualification remain separate requirements. The inventory does not
claim that arbitrary provider fields have a product-owned nested schema or that
historical domain defaults were silently replaced with exact schemas.
