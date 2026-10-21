# Release assets

A release consists of signed images, the signed OCI chart, the signed Flux and
built-in distributions, and `release-manifest.json` with its Sigstore bundle.
`release.yaml` produces all of these through `cmd/release-assets` using GitHub
OIDC. The producer validates its closed bundle with the production consumer
before publication and signs every child chart and database subject. All bundle
children share the existing public `astronomer-platform-bundles` registry
package, with independent digests and role-specific tags. The signed BOM binds
the OCI lookup name separately from each original chart archive, so publishing
a new release does not require creating private GHCR packages.

The manifest is generated after the immutable chart is published. A manifest
cannot be embedded into the archive whose digest it binds without a circular
hash. The release therefore includes `release-values.yaml`, the chart values
overlay containing the exact signed manifest. Always install with that overlay.
For an existing installation, apply it after the installation's saved values:

```sh
cosign verify-blob --bundle release-manifest.sigstore.json \
  --certificate-identity "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/v2.1.1" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  release-manifest.json
helm upgrade astronomer astronomer-2.1.1.tgz -n astronomer \
  -f installation-values.yaml -f release-values.yaml --wait
```

The chart mounts the manifest as an immutable ConfigMap. At boot the server
checks its version against the binary and projects the exact signed image,
Flux, and built-in distribution identities into runtime configuration. A bad
version or malformed inventory fails boot. The production bundle loader verifies
keyless signatures and the complete catalog/chart/database graph before making
built-ins available; it never substitutes the unsigned development catalog.

For a local preparation proof without registry writes or signing:

```sh
go run ./cmd/release-assets -phase prepare -version v2.1.1
```

The output contains a local OCI layout, the closed archives, and `subjects.json`.
The manifest phase additionally requires six image `.digest` files from the
build jobs, a published chart digest, the source commit, and its timestamp.

Kubernetes Secret key entries are symlinks. The server and worker read delivery
trust through `/etc/astronomer/delivery-trust/..data`, where each key is a regular
file. The verifier retains its no-follow and before/after identity checks, so a
concurrent Secret rotation fails closed and is retried. Public registry blobs
may redirect to public HTTPS CDNs; the distribution client strips credentials
and revalidates every redirect destination. Restricted policies do not permit
those public redirects.
