# Verified distribution v2 mirror execution

The opt-in v2 mirror action authenticates the independently pinned offline
parent, every chart/database child and each unchanged vendor legacy signature
through the canonical loader before interpreting the signed BOM or inventory.
It derives one deterministic complete plan from those bytes. The existing
historical `mirror-release.py plan/apply/verify` commands retain their v1
contract; `v2-plan` and `v2-copy` delegate to the same hash-pinned Go verifier.

Each target repository includes a namespace derived from its original registry
host. Two vendors with identical repository paths cannot collide. Chart
repository bases keep their child chart-name relationship, and the parent and
chart/database subject digests do not change. The plan also includes every
signed inventory image, prerequisite role, original member path/hash and
projected prerequisite image. CNPG/operator/server/Barman/tool, RustFS/Valkey,
Dex/Fluent Bit/Charlie, application catalog and registry roles are included when
the corresponding signed profile enables them. Binary/tool/CRD and prerequisite
member bytes remain inside the original signed parent; copying the parent does
not install or take ownership of an external operator.

Preparation writes a new private directory containing `mirror-plan.json` and
`mirror-schedule.json`. These are preparation metadata, not vendor or customer
approval. The operator separately signs the exact plan with its mounted public
endorsement policy. Execution recomputes the complete plan from the original
verified graph and authenticates that operator endorsement before any registry
write. A valid signature on a missing/changed rewrite still fails. The operator
endorsement cannot grant network access or replace the vendor signature policy.

Copy execution preflights every approved destination and inventory image
source. It copies every platform, configuration and layer through ORAS and the
canonical bounded destination client, preserving exact content digests. Metadata
is limited to 1 MiB, 64 index children, 256 manifest layers, depth 8 and 4096
members; a layer is at most 128 MiB and a complete image graph at most 512 MiB.
External descriptor URLs/data, missing content, ambiguous JSON, wrong platform
configuration and unsupported graph shapes fail. Oversized real supplier
images remain an explicit qualification blocker; bounds are not silently raised.

Original signed child graphs and their unchanged `.sig` manifests are copied
before the parent. No vendor subject is resigned. Protected Docker credential
stores decorate the same guarded transport. Captured registry CA material
retains TLS verification. The final canonical runtime loader authenticates the
mirrored parent and every child with the original vendor policy and the verified
operator plan. Failure leaves no accepted new product assignment; retries copy
the same immutable digests. A partial registry copy is not reported as success.

The source/copy policy may permit the finite upstream and target origins needed
for connected preparation. Runtime disconnected policy should permit only the
approved target origins. The generated source/renderer inputs use target
locations; original URLs remain audit metadata. There is no missing-subject
public fallback. Workload image bytes are copied from connected authenticated
sources by this action; the existing signed transfer contains the parent/chart/
database graphs, not all workload image layers. W04 still needs a separately
closed workload image import and a fresh-cache no-egress qualification.

For a prepared canonical verifier whose independent digest is retained, the
v2 preparation interface is:

```sh
python3 scripts/mirror-release.py v2-plan \
  --distribution-verifier "$DISTRIBUTION_VERIFIER" \
  --verifier-sha256 "$DISTRIBUTION_VERIFIER_SHA256" \
  --transfer "$VERIFIED_OFFLINE_TRANSFER" \
  --repository "$ORIGINAL_DISTRIBUTION_REPOSITORY" \
  --distribution-digest "$EXPECTED_DISTRIBUTION_DIGEST" \
  --catalog-digest "$EXPECTED_CATALOG_DIGEST" \
  --release "$EXPECTED_RELEASE" \
  --release-manifest-digest "$EXPECTED_RELEASE_MANIFEST_DIGEST" \
  --trust-directory "$MOUNTED_VENDOR_TRUST" \
  --signature-provider cosign_keyless \
  --certificate-identity "$EXPECTED_VENDOR_IDENTITY" \
  --certificate-oidc-issuer "$EXPECTED_VENDOR_ISSUER" \
  --target "$APPROVED_MIRROR_REPOSITORY_BASE" \
  --output-directory "$NEW_PREPARATION_DIRECTORY"
```

After the operator signs `mirror-plan.json`, `v2-copy` uses the same independent
pins and target, plus `--endorsement`, `--signature`, `--operator-key-ref`,
`--operator-trust-directory`, `--destination-policy` and optional
`--registry-ca-file`. It accepts no private signing key or URL credentials. The
adapter captures and rechecks its hash-pinned executable into an owned temporary
directory; tool diagnostics are discarded to avoid credential-bearing errors.
A locally compiled verifier is QA tooling until a vendor-qualified release
kit publishes its exact binary identity.

Actual selected Cosign 3.1.3 QA signing now exercises original graph capture,
complete planning, separate ECDSA operator endorsement, actual ORAS copies,
canonical mirrored loader, keyed provider and agent Secret/OCI/digest inputs.
The database-inclusive fixture binds all 15 prerequisite roles with small QA
members and one two-platform QA image. It does not qualify actual supplier
operators, database size/freshness, private registry authentication, production
OIDC startup or live Flux reconciliation. The compiled preparation CLI also has
an actual invocation lane selected by `DISTRIBUTION_BIN`; ordinary runs without
that variable explicitly leave that invocation unqualified.

Metadata-only official GHCR discovery on 2026-10-09 found the primary database
layer at 126,378,346 bytes and the Java database layer at 980,383,808 bytes.
The Java layer exceeds the current 128 MiB blob and 512 MiB graph limits; the
combined database payloads cannot fit the current parent archive. Neither
payload was downloaded or qualified for signature/freshness. The sanitized
[discovery receipt](trivy-size-discovery.json) records exact subjects and
layer/config descriptors. A reviewed versioned descriptor-only parent binding
and separately bounded streaming child/kit path are required before those real
supplier profiles can qualify. Current code continues to reject their oversized
bytes.
