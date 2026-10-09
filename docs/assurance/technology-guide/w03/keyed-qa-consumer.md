# Captured public-key QA consumer

The constructor-only keyed QA lane captures a mounted public key through the
canonical resolver's bounded regular-file reader. It validates a single public
PEM block and captures at most the protocol's 256 KiB credential limit. The
canonical trust reader checks the opened inode and limits bytes before
allocation. Missing, symbolic, malformed, oversized or changed inputs fail.
The captured public key resides in a new owned trust directory; input mutation
cannot change it, and its lifecycle cleans up that directory.

`provider.NewBuiltinQAConsumer` creates and returns the canonical loader used by
its provider. It forces the loader's trust directory and exact KeyRef to this
captured snapshot, rejects development/unsigned/other signature policies, and
keeps the raw-key adapter private. The provisioner can receive that same returned
loader. This prevents pairing a provider's different key with an already cached
loader. No production configuration or command-line flag selects this lane;
the server's distribution constructor continues to require its exact vendor
OIDC identity and issuer.

Production CA and keyed QA consumers share the same builtin source authorizer.
It checks deterministic cluster/project/source ownership, exact chart location,
version and subject digest, signature policy, and the independent canonical
destination policy. The keyed provider additionally matches the stored exact
KeyRef to its captured reference. Snapshot-local authorization bindings govern
hydration, so an unrelated platform source cannot inherit the captured public
key or CA. Original encrypted user source material retains its existing path.

Only validated public certificate/signing-key fingerprints enter the existing
credential receipt projection (`public_ca`/`public_signer`). The global epoch
invalidates old acknowledgments on rotation. Captured and returned bytes are
cloned. Agent materialization emits `Secret.cosign.pub`, mandatory OCI signature
verification against that Secret, and the unchanged digest. There is no secret
hash, unsigned escape, database schema, public API or root dependency change.

Actual official Cosign 3.1.3 QA publication now reaches the provider and the
agent object builder for both a parent/three-chart graph and a graph with two
additional database subjects. The same captured-key loader authenticates these
subjects before snapshot hydration. Changed source ownership, digest and KeyRef
fail. Source snapshot mutation, malformed key inputs, signer rotation and
factory policy/directory mismatches have separate focused tests.

This proves keyed consumer composition with an owned QA identity. It does not
prove production server candidate startup, production OIDC signing or live Flux
reconciliation. A full production builtin candidate still requires real OIDC
signed parent/child subjects and later runtime qualification. Actual registry
authentication and supplier database sizes/freshness remain separate explicit
prerequisites. This source evidence cannot mark W03 or a release complete.
