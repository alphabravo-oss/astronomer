# Support bundle collection runbook

Use this procedure to collect a bounded, audited Astronomer support bundle for
incident diagnosis. A bundle is sensitive operational evidence even after
automatic redaction; it is not safe to attach directly to a public issue or
unrestricted chat.

## Authorization and scope

Only a superuser can download `/api/v1/support-bundle/`. Create a short-lived,
narrow administrative token for the collection, record the incident/change
reference, and revoke it immediately afterward. Decide the time window and
problem scope before collecting additional Kubernetes logs or object YAML.

The built-in bundle is the preferred first artifact because its collectors,
size limits, and secret redaction are product-controlled. Do not substitute
`kubectl cluster-info dump`, a database dump, full Secret export, browser local
storage, or raw Helm values.

## Collect

Use a private directory with restrictive permissions and disable shell tracing:

```bash
umask 077
mkdir ./astronomer-support-incident-1234
export ASTRO_SERVER='https://astronomer.example.com'
export ASTRO_API_TOKEN='<short-lived-superuser-token>'
curl --fail --silent --show-error \
  -H "Authorization: Bearer ${ASTRO_API_TOKEN}" \
  --output ./astronomer-support-incident-1234/support-bundle.zip \
  "${ASTRO_SERVER}/api/v1/support-bundle/"
unset ASTRO_API_TOKEN
sha256sum ./astronomer-support-incident-1234/support-bundle.zip \
  > ./astronomer-support-incident-1234/SHA256SUMS
```

Record the HTTP request ID from the response/management logs and confirm a
corresponding audit event exists. A 401 means the token is absent/expired; a
403 means the actor is not a superuser. Do not weaken route policy to collect
the artifact.

## Inspect before sharing

List the archive before extraction and reject absolute paths or parent
traversal entries:

```bash
unzip -l ./astronomer-support-incident-1234/support-bundle.zip
mkdir ./astronomer-support-incident-1234/review
unzip -q ./astronomer-support-incident-1234/support-bundle.zip \
  -d ./astronomer-support-incident-1234/review
```

Search the extracted text for site-specific secret markers, bearer/JWT token
prefixes, private keys, connection strings, passwords, cloud access keys,
cookie values, and unapproved customer workload content. Review false positives
manually; do not upload until a second authorized reviewer signs off.

If any real secret is present:

1. stop distribution and preserve access logs;
2. rotate/revoke the exposed credential according to its runbook;
3. record the redaction defect as a security issue with only a safe fingerprint
   and file path, not the value; and
4. produce a manually redacted copy, recompute its checksum, and clearly mark
   it as modified.

## Add narrow supplemental evidence

Collect only what the built-in bundle lacks. Prefer object names, conditions,
events, resource usage, rollout status, and bounded log windows. Redact Secret
data, environment values, authorization headers, cookies, DSNs, URLs containing
credentials, source credentials, and customer payloads.

Useful bounded examples:

```bash
kubectl -n astronomer get deploy,pod,job,cronjob,pdb -o wide
kubectl -n astronomer get events --sort-by=.lastTimestamp | tail -200
kubectl -n astronomer logs deployment/astronomer-server \
  --since=30m --all-pods --prefix --tail=2000
kubectl -n astronomer logs deployment/astronomer-worker \
  --since=30m --all-pods --prefix --tail=2000
helm -n astronomer history astronomer
```

Do not collect Kubernetes Secrets, ConfigMaps known to carry credentials,
database dumps, Redis snapshots, encryption keys, signing keys, agent
registration manifests, complete audit exports, or arbitrary member-cluster
objects unless incident command and security explicitly approve that expansion.

## Transfer, retention, and destruction

Encrypt the reviewed artifact for the named recipient, use an access-controlled
support channel with expiry and download logs, and communicate the decryption
secret through a different channel. Record checksum, collector, reviewers,
recipient, incident ID, upload time, expiry, and deletion owner.

Delete local extracted copies and revoke the collection token after confirmed
receipt. Retain the encrypted artifact only for the approved incident/legal
window, then verify deletion at both ends. The support ticket should retain the
checksum and disposition, not the bundle content.
