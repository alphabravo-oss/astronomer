# Vendored chart dependencies

`charts/argo-cd-9.5.21.tgz` is the pinned dependency declared in `Chart.yaml`
and `Chart.lock`, downloaded from the official Argo Helm repository:
`https://argoproj.github.io/argo-helm`.

Upstream source tag `argo-cd-9.5.21` resolves to commit
`4c9fe87dd72dd8de1d36928546a2716d016af337`. The upstream Apache-2.0
license is vendored at `licenses/argo-helm-APACHE-2.0.txt` and included in
the embedded chart archive.

Archive SHA-256:

```text
5e440d83c763360e16cd93b48f41450cc0d688ec83ee444840faa271ac536443  argo-cd-9.5.21.tgz
```

Regenerate with `helm dependency build deploy/chart`, verify the version and
checksum deliberately, and run `go test ./deploy -run AstronomerChartArchive`.
The archive is committed because the management server's embedded Helm repo
must render the bundled Argo dependency without network access.
