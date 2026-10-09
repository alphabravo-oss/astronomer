# JSON contract ownership inventory

`json-contracts.json` is the checked source inventory. It classifies each product
HTTP decoder and each JSON-capable canonical sqlc storage field as strict product,
versioned internal, raw Kubernetes, extension data, opaque external or a narrow
non-JSON exclusion. Published v1 unknown-field decisions and malformed-reader
behavior are explicit. No provider document gains a product nested schema merely
because its envelope is JSON.

The initial W09 inventory covers 189 request boundaries, 205 storage candidates
(171 RawMessage, 9 byte slots and 25 encrypted slots), 383 serializer/reader
modules and 1,125 import-bound JSON call sites (1,102 unique function/operation/
argument bindings). Fifteen exact binary/scalar exclusions cover artifact bytes,
refresh hashes and encrypted scalar credentials/keys. Ciphertext holding a JSON
record remains classified with its decrypted domain owner; the read-only database
inventory does not unseal it. Runtime registry versions, including existing v2
component/security schemas, are retained rather than flattened to v1.

Canonical query bindings identify named SQL candidates. Canonical query-extension
templates are also inventoried, including the idempotency response writer that
returns a marshal error before SQL. Physical column candidates come from sqlc's
canonical generated models. A shared field name or a query containing SELECT *
is a candidate source binding, not proof of typed dataflow; the manifest records
that limitation explicitly. Seven raw/helper paths missing a named-query binding
have explicit canonical helper/partition bindings.

The critical owned boundaries have application behavior evidence: inhibition
writer/worker/handler readers, pre-enrichment and post-enrichment task envelopes,
audit marshal/dispatch/publish, frozen-plan prewrite/read, and system rollout
strategy read. Canonical delivery model validators continue owning placement,
strategy and frozen identity rules. Opaque task business properties remain owned
by their existing task handlers; a second task registry was not introduced.

Existing unrelated v1 defaults are recorded, not silently changed into strict
schemas. Examples include repair-job telemetry's empty-object marshal fallback,
extension list presentation's existing zero-value decode fallback (proxy and
compatibility readers still fail on malformed manifests), and built-in compliance
baseline specs projected from the registered slug rather than arbitrary database
spec authority. Per-site annotations preserve these named-function behaviors;
`discarded_result_requires_domain_review` is syntax metadata indicating a baseline
ignored result, not a claim that arbitrary reader errors were fixed. The W09
malformed-inhibition requirement is fully enforced on every inhibition reader.

A concrete compatibility stop was identified: cluster annotations already accept
non-string producer metadata in the impersonation-mode-off fixture, and the
existing domain/registry require only an object. That fixture intentionally
round-trips a numeric value. The cluster metadata adapter therefore retains
additive v1 objects and its existing key-specific authorization checks; 072 does
not add a string-map constraint to clusters or normalize existing metadata.
The narrower logging/placement label schemas retain their own stable string
shape. Existing request patch null/omission semantics are characterized separately
in request-json-compatibility.md.

`make data-governance-check` runs the closed inventory gate. A new storage field,
serializer module, or JSON call inside an already owned module requires explicit
schema ownership and reader behavior. A new raw handler decoder cannot hide
behind a recognized helper name: exact response/stored-reader exceptions bind
function and arguments. Discovery binds import aliases and HTTP parameter objects,
so local alias variables and shadowed request names in unrelated nested callbacks
are excluded. It understands generic canonical helper calls. The gate is bounded
syntax discovery, not whole-program or runtime-generated SQL proof.

Verification includes six Go AST fixtures and ten negative gate fixtures.
Real PostgreSQL tests independently check runtime constraints, existing-v1 shape,
audit dedupe, expand/validation refusal and new malformed writes. The row inventory
and reviewed repair policy are in durable-json-repair.md. Column/site counts alone
are not evidence that every legacy opaque payload has a validated nested schema.
