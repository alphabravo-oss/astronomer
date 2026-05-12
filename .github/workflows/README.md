# GitHub Actions

**Currently disabled during development.**

The release workflow that builds + signs + attests container images lives at
[`../workflows-disabled/release.yaml`](../workflows-disabled/release.yaml).
GitHub Actions only auto-loads `.yml`/`.yaml` files from `.github/workflows/`,
so files in `workflows-disabled/` are inert.

To re-enable:

```bash
mv .github/workflows-disabled/release.yaml .github/workflows/release.yaml
git add .github/workflows
```

The verifier-side runbook in [`../../docs/verify-images.md`](../../docs/verify-images.md)
documents how to verify the signatures + SBOMs once the workflow is live and
has published its first signed images.
