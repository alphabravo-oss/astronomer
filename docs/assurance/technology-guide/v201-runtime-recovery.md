# v2.0.1 runtime qualification recovery

Status: Active

On 2026-10-12, release run [38199835448](https://github.com/alphabravo-oss/astronomer/actions/runs/38199835448)
built, scanned and signed all seven application images and both offline toolkits.
The later transitive-image scan found CVE-2026-78667, CVE-2026-78669 and
CVE-2026-97031 in retained upstream Go binaries. The owner explicitly instructed
that upstream findings do not hold publication and approved the same policy for
v2.0.1. First-party static-analysis qualification passed and is not deferred.

## Upstream availability

Fresh Trivy 0.75.0 scans on 2026-10-12 confirmed all three CVEs still occur in each
latest upstream release below. Scanner database updates were enabled before the
scan series. Availability was checked against each repository's release API.

| Upstream | Latest release checked | Scan SHA-256 |
| --- | --- | --- |
| dexidp/dex | [v2.46.0](https://github.com/dexidp/dex/releases/tag/v2.46.0) | `eeb2dd17a1e2229ca3cc286f6d99a1eeadeb470f560b0064190230b6dc14128a` |
| fluxcd/helm-controller | [v1.6.5](https://github.com/fluxcd/helm-controller/releases/tag/v1.6.5) | `4d4a8b8537bc6121c6c410b1ce4bda63f2adf64c69858e4ae74187645115a649` |
| fluxcd/kustomize-controller | [v1.9.6](https://github.com/fluxcd/kustomize-controller/releases/tag/v1.9.6) | `4f3e75aff0a3266beb64e61efa581b9b265800a85909b2ab3774f919ba2cef12` |
| fluxcd/source-controller | [v1.9.6](https://github.com/fluxcd/source-controller/releases/tag/v1.9.6) | `dff9781b17d15dda2160f6706de9be30aafad940babc15eed0d2e43de722f508` |
| prometheus/node_exporter | [v1.12.1](https://github.com/prometheus/node_exporter/releases/tag/v1.12.1) | `168ff7c4e9f3506b7b889a0a277ca63614af5531bea6506fb570047acd2c5886` |
| kubernetes/kube-state-metrics | [v2.20.0](https://github.com/kubernetes/kube-state-metrics/releases/tag/v2.20.0) | `742477f8dafd9de5f7784ae571ed4c14785326aa75348ba950540f7dad4cb31c` |
| aquasecurity/trivy | [v0.75.0](https://github.com/aquasecurity/trivy/releases/tag/v0.75.0) | `55086ba744d8545b72c175ae490c7657f1b0ca3a5865c4ca44cb0f9adf792192` |
| aquasecurity/trivy-operator | [v0.35.0](https://github.com/aquasecurity/trivy-operator/releases/tag/v0.35.0) | `bacac82b84719b1f388b29a139a95cf8b6f783ba63976490fa39c5ad07bc82d5` |

The same CVEs also occur in the official kubectl binary inside the shell image,
the retained legacy PostgreSQL 16 utility binaries, and the qualified Charlie
v1.0.63 upstream Go dependencies. The shell build separately verified the exact
official kubectl hashes under `deploy/release/upstream-binary-waivers.json`.
CNPG's retained PostgreSQL 17.11 image scans clean; CNPG remains the local and
production database path. The legacy PostgreSQL image remains only for supported
upgrade/restore compatibility. Available major/minor changes do not replace an
already signed release's exact runtime dependencies; Dex 2.46.0 in particular
still has these findings despite its newer authentication/storage behavior.

All eleven affected immutable image references have exact CVE records in
`deploy/release/runtime-image-waivers.json`. No first-party application source
finding is waived. No additional CVE beyond the three above was added. The
expiry remains **2026-11-06T00:00:00Z**. All other CVEs, changed image digests,
unapproved releases and expired records fail the gate. License findings remain
retained and disclosed as deferred, not license-qualified.

## Preserve original artifact trust

The tag remains `9fee3c5ddc59353413c9f5574d6f119e283cc808`. Original application
images, delivery bundles, chart, manifest and toolkits are reused without
changing their contents. The manifest signature bundle was lost with the failed
runner, but its original public signature, certificate and signed inclusion
promise remain in [Rekor entry 3220778135](https://rekor.sigstore.dev/api/v1/log/entries?logIndex=3220778135).
The bounded recovery helper checks the exact manifest SHA-256
`88af461a142186fc7e6009c13db1218eb712eaea6ad0f041016b07b50c322ec4`
and verifies the reconstructed bundle using cosign against the original
`release.yaml@refs/tags/v2.0.1` identity and GitHub issuer. This does not create a
new manifest signature or change what the released installer trusts.

Only runtime qualification uses the existing `resume-release.yaml@refs/heads/main`
signer. Its separate signed proof binds the exact tag, source commit, original
run, producer run/commit, original manifest and fresh report/archive hashes.
Manifest and chart verification retain their original tag signer. Incomplete,
wrong-source or modified proofs fail closed. The recovery workflow still must
pass Kubernetes compatibility, upgrade, backup, decrypt and clean-restore checks
before creating the GitHub release. The release notes disclose the recovery.
