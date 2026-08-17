# Built-in delivery bundles

`catalog.json` is the immutable input used to seed the optional platform
components on a fresh v1 install. These are ordinary delivery sources, bundle
versions, targets, and rollouts; there is no private baseline controller or
alternate lifecycle.

Every chart has an exact semantic version and verified archive SHA-256. Every
image enabled by the shipped values has a multi-platform manifest digest. The
release builder downloads the chart archives, verifies those hashes, and packs
them with this catalog into a reproducible signed OCI artifact. A disconnected
mirror may rewrite registries and source kind only through the signed mapping
manifest; it cannot change a digest.

Build and verify locally:

```sh
./scripts/build-builtin-bundles.sh --output dist/astronomer-builtin-bundles-v1.0.0.tar.gz
./scripts/build-builtin-bundles.sh --check dist/astronomer-builtin-bundles-v1.0.0.tar.gz
```

Publishing requires `oras` and `cosign` and is intentionally explicit:

```sh
./scripts/build-builtin-bundles.sh --publish oci://registry.example/astronomer/bundles:v1.0.0
```
