# Original vendor signatures through offline graph transfer

The operator transfer captures each pinned parent and chart child OCI graph,
including configuration bytes, payload layers and the explicit legacy signature
tag. Each subject authenticates with the canonical verifier before graph capture;
the complete captured graph reauthenticates before archive emission. Protected
registry credentials decorate the same bounded network client. Original
signature bytes are retained rather than replaced with an operator signature.

Offline verification uses a concrete in-memory registry transport backed only
by a bounded, copied, closed blob map. It contains no socket or DNS transport.
The operator-only entry point has no client/network/development/mirror options,
and no runtime configuration field or archive field can select this mode.
Unknown endpoints, writes, redirects, unavailable subjects and external/inline
descriptors cannot delegate to a network client. Every manifest, configuration
and layer must match its exact digest/size; orphan and unconsumed subjects fail.
The canonical loader still authenticates the original parent and every child
with mounted trust and exact digest claims. Caller-pinned release, parent and
catalog identities are independent of the unsigned transport index.

The shared safe regular-archive reader now serves both existing authenticated
archives and the offline graph envelope; existing archive limits are preserved.
Import creates a new private standard OCI store only after canonical graph
authentication. No supplied archive name becomes an output path. It preserves
original manifest/configuration/layer/signature bytes and deterministic local
index references, retaining original repository identities in the transfer
index. These are operator tooling interfaces; runtime network policy is intact.

Actual official Cosign 3.1.3 QA signatures exercise registry capture, offline
verification, archive packing and import. The resulting standard OCI store
resolves original subject and signature digests. Eight unsafe archive cases
and seven graph/cryptographic cases fail, including an actual modified signature
with a valid newly hashed manifest. Wrong expected release creates no output.
The working external proxy control receives one request and zero requests during
the verification chain. Unit cases confirm immutable copied bytes, read-only
transport, cancellation and explicit descriptor URL/inline-data denial.

The [source-bound receipt](offline-transfer-evidence.json) retains the first
broader test failure: actual Cosign exposed concurrent map access in the owned
registry fixture. The fixture now serializes HTTP map operations. The same eight
Go packages pass, as do eight application/runbook cases, existing release-kit
cases, schema assertions and docs. The full release-contract retry is blocked by
shared host capacity at 64 GiB/19% free against the existing 20% requirement.
No capacity override or cache prune was used; the publisher slice's earlier
full gate pass remains a separate historical result.

W03 remains active. Prerequisite OCI child publication, exact DB/mirror rewrites,
future release workflow wiring and the current supplier profile still need
completion. The local QA key is distinct from production vendor OIDC identity.
Published signed verifier assets, W04 fresh-cache/no-egress and W12 object-store
and recovery qualification remain separate requirements. The new runbook
example documents the concrete import interface; its production keyless release
execution is unavailable until those publisher/assets exist.

W03 owns the closed offline-transfer schema and readers. Existing `writeJSON`
is reused for the transport index and derived standard OCI indexes; no new
direct production JSON serializer was introduced. The index grants no trust
without the canonical parent/child verifier and independent caller pins.
