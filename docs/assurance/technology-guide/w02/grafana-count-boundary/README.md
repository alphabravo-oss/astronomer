# Grafana metadata numeric boundary

Stored monitoring-stack replica metadata now rejects values that cannot be represented exactly as an int32 before either metadata reconciler makes Kubernetes requests or database writes. Missing values retain zero; representable existing negative values retain their old meaning. This slice covers metadata reconciliation, not a direct Deployment scaling path.

The [packet](packet.json) preserves four immutable qualification stages and all original bytes as deterministic gzip. Each entry binds the original external path, uncompressed byte count and SHA256, compressed byte count and SHA256. Four frozen Git maps bind the complete tracked source at each run. The original external directories remain unchanged.

- `03449c87`: the first before fixture failed to compile because an encrypted-auth field is a string. No test result is inferred from this failure.
- `5c1f7bed`: after that fixture-only correction, both numeric-boundary parents and all six invalid-value leaves failed against the old implementation. The package failed, with zero skips.
- `2c1367fd`: seven parents and 29 leaves passed under the race detector, with one package pass and zero skips. Handler vet, cached JSON governance and dependency boundaries passed. The full runner then failed on the stale complexity baseline, which still recorded 865 lines after the source had decreased to 861.
- `72b996a7`: the canonical baseline decrease was the only change. A separate metadata-only run passed the complexity gate over 2207 production files, 41 file ceilings and 64 function ceilings. It launched no Go command and made no database mutation.

Every launched process group was held and drained. All preflight, maintained and final capacity guards passed, and each run retained stable source and selected Go identities. The final metadata check had one Node group and six numeric capacity guards. Original failures remain indexed alongside the successful scoped checks.

This packet does not qualify a current repository-wide native security scan, close every W02 finding, or establish whole-plan completion. Separate targeted runs are joined by exact immutable source identities; no later source change is attributed to an earlier receipt.
