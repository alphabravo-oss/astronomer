# Air-gapped install — Astronomer Go management plane

This runbook walks through installing Astronomer into a Kubernetes
cluster that has **no outbound internet access**. The output is the
same shape as a normal `helm install`, except every container image is
served from an internal registry inside the airgap.

## Audience

- Operators landing Astronomer in regulated / restricted environments
  (DoD, healthcare, finance, classified networks).
- Anyone who needs reproducible installs from a fixed image set rather
  than pulling from public registries on demand.

## Prerequisites on the egress side

You'll need a workstation that can reach **both** the public registries
(to pull images) **and** your internal registry (to push them). This
machine doesn't need to reach the Kubernetes cluster itself.

- [`helm`](https://helm.sh/docs/intro/install/) 3.13+
- [`skopeo`](https://github.com/containers/skopeo) (preferred) or
  `docker buildx imagetools` / `crane`
- An auth-credential pair for your internal registry
- The chart source: clone `astronomer-go` so `deploy/chart/` is local

## Step 1 — Get the image list

`deploy/chart/images.txt` is regenerated from the chart by
`make images.txt`. The committed file is the authoritative list at the
time of the last release; regenerate if you're tracking `main`:

```bash
make images.txt
grep -v '^#' deploy/chart/images.txt > /tmp/images.txt
cat /tmp/images.txt
```

You should see the first-party images (server, worker, migrate, agent,
frontend, shell) plus third-party deps used by default *and* by production
optional components: postgres, redis/valkey, busybox, Argo CD, **Dex**,
**pgdump-s3** (management backup / restore drill), and **fluent-bit**
(management logging). The extractor unions a default render with a
production-like render so air-gapped prod installs don't miss Dex/backup/
logging images that stay off in bare `values.yaml`.

## Step 2 — Mirror images into your internal registry

```bash
INTERNAL_REGISTRY="internal.example.com/astronomer"

while read -r img; do
  echo "Mirroring $img → $INTERNAL_REGISTRY/$img"
  skopeo copy --all \
    "docker://$img" \
    "docker://$INTERNAL_REGISTRY/$img"
done < /tmp/images.txt
```

`--all` copies the full manifest list (every platform variant). Drop
it if you only need linux/amd64.

For air-gapped environments that *truly* can't reach the public
registries even from the egress workstation, do the mirror in two
hops:

```bash
# Hop 1: workstation that can reach the public internet
skopeo copy --all "docker://postgres:16-alpine" "dir:./postgres-16-alpine"
# ... repeat for every image, then ship the directories on physical media

# Hop 2: machine inside the airgap that can reach the internal registry
skopeo copy --all "dir:./postgres-16-alpine" "docker://$INTERNAL_REGISTRY/postgres:16-alpine"
```

## Step 3 — Install the chart

```bash
helm install astronomer ./deploy/chart \
  -n astronomer --create-namespace \
  -f ./deploy/chart/values.yaml \
  -f ./deploy/chart/values-production.yaml \
  --set "image.registry=$INTERNAL_REGISTRY" \
  --set 'image.pullSecrets[0].name=internal-registry-creds' \
  --set 'postgres.external.dsn=postgres://...' \
  --set 'redis.external.address=...' \
  --set 'config.serverURL=https://astronomer.internal.example.com' \
  # …plus the rest of the production preflight inputs (see the
  # chart README "Dev vs. Production Posture" section).
```

The single `image.registry` flag is enough to redirect every image —
the third-party images (postgres, redis, kubectl, busybox, frontend,
pgdump-s3) use the same fallback prefix unless you give them an
individual `<component>.image.registry` override.

If you need different mirrored locations for different images
(e.g. Postgres lives in `vendor-mirror.example.com` but everything
else in `astronomer.example.com/...`), set per-image overrides:

```yaml
image:
  registry: astronomer.example.com
postgres:
  image:
    registry: vendor-mirror.example.com
redis:
  image:
    registry: vendor-mirror.example.com
```

## Step 4 — Create the registry pull secret

If your internal registry requires auth:

```bash
kubectl -n astronomer create secret docker-registry internal-registry-creds \
  --docker-server="$INTERNAL_REGISTRY" \
  --docker-username="$INTERNAL_REGISTRY_USER" \
  --docker-password="$INTERNAL_REGISTRY_PASSWORD"
```

Reference it via `image.pullSecrets[0].name` (see Step 3).

## Step 5 — Member cluster install

When you onboard a new member cluster, the management plane renders
`install.yaml.template` with the agent image's pull spec. The chart
emits the agent image's prefix from the same `image.registry`, so the
generated template already points at your internal registry. The
target cluster needs to be able to pull from that registry too.

If member clusters use a *different* internal registry, you can
override the agent image specifically:

```yaml
image:
  registry: management.internal.example.com   # used by server/worker/etc.
  agent:
    repository: astronomer-go-agent
    tag: v1.0.0
    # No per-component .registry knob today — agent uses image.registry.
    # If you need a separate location, mirror the agent image to both
    # registries.
```

## Step 6 — Verify

```bash
# Pods should all be Ready
kubectl -n astronomer get pods

# Image refs match your internal registry
kubectl -n astronomer get pods -o jsonpath='{.items[*].spec.containers[*].image}' | tr ' ' '\n' | sort -u

# Server health
kubectl -n astronomer port-forward svc/astronomer-server 8080:8000 &
curl http://localhost:8080/health/
```

Every image listed by the second command should start with
`internal.example.com/astronomer/` (or whatever you set). Any line
NOT prefixed is a chart bug — open an issue.

## Re-mirroring on upgrade

Each chart release ships a regenerated `deploy/chart/images.txt`.
Diff against the previous version to learn which images changed
between releases:

```bash
git diff v1.2.0 v1.3.0 -- deploy/chart/images.txt
```

Then mirror only the new / changed images. The chart's image-tag
overrides (`image.<component>.tag`) let you pin to a specific
release-known-good even if the helm chart moved forward.

## Related

- [`scripts/extract-images.sh`](../scripts/extract-images.sh) — script
  behind `make images.txt`
- [`deploy/chart/README.md`](../deploy/chart/README.md) — base chart
  values reference + production posture checklist
- [`docs/upgrade-runbook.md`](upgrade-runbook.md) — applies after the
  air-gapped install is live
- [`docs/verify-images.md`](verify-images.md) — cosign verification
  after mirroring (image signatures travel with manifest copies; verify
  against the original public key)
