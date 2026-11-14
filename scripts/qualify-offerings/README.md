# Read-only offering qualification prerequisites

Run from the candidate checkout with a private config file:

```sh
go run ./scripts/qualify-offerings inventory --config /private/config.json --output /private/inventory-unique.json
go run ./scripts/qualify-offerings preflight --config /private/config.json --output /private/preflight-unique.json
```

`inventory` compares the six API registries with the retained 174-case manifest.
Every lifecycle case remains `NOT_RUN`. `preflight` checks whether explicit
member targets meet the prerequisites below. A preflight `PASS` is readiness
only: the v2 report always has `lifecycle_qualified: false`. Neither command
installs, upgrades, uninstalls, labels resources or changes Charlie state. `run`
and `verify` are unsupported in this port. The transport permits GET only and
does not follow redirects. The CLI has a ten-minute overall deadline.

Reports are private (0600) and published atomically without replacing an existing
path. Use a new output path for each run. Keep evidence outside the checkout for
a clean-candidate preflight: tracked changes and all non-ignored untracked files
anywhere in the worktree make it dirty, including reports. Source identity is
checked again after API reads. Git operations resolve the worktree root even
when invoked from a subdirectory.

## Config

```json
{
  "schema_version": "astronomer-offering-qualification-config-v1",
  "base_url": "https://management.example.test",
  "token_file": "/private/access-token",
  "expected_commit": "0123456789abcdef0123456789abcdef01234567",
  "qualification_id": "qualification-20261023",
  "estate_evidence_file": "/private/estate.json",
  "estate_evidence_sha256": "<SHA256 of the reviewed estate.json bytes>",
  "target_ids": {},
  "member_targets": [
    {
      "name": "primary",
      "cluster_id": "00000000-0000-0000-0000-000000000001",
      "project_id": "00000000-0000-0000-0000-000000000011",
      "namespace": "qualification",
      "expected_privilege_profile": "admin"
    },
    {
      "name": "secondary",
      "cluster_id": "00000000-0000-0000-0000-000000000002",
      "project_id": "00000000-0000-0000-0000-000000000012",
      "namespace": "qualification",
      "expected_privilege_profile": "admin"
    }
  ]
}
```

The example IDs are placeholders. Supply the exact clean candidate commit and
real task-owned targets. Token files must be private regular non-symlink files,
contain one credential, and fit within 8 KiB. Config and attestation reads are
bounded at 8 MiB; duplicate fields, unknown fields and trailing JSON reject.
Public origins require HTTPS. `allow_loopback_http` is a test-only opt-in.

Preflight requires two to eight distinct non-local cluster IDs and distinct
project IDs, active/ready registration, a heartbeat no older than two minutes,
and the explicit privilege profile. Kubernetes versions use delivery's shared
stable 1.33–1.35 validator. `v1.35.7+k3s1` passes; 1.30, missing patch versions,
prereleases, malformed versions and unsupported minors fail.

Namespace identity is (cluster, namespace), so the same namespace name on two
clusters is valid. `default`, `kube-*`, `astronomer*` and `flux-*` targets reject.
The project must explicitly include the target namespace. The namespace must be
Active and its `astronomer.io/qualification-id` label must equal the configured
task ID. Provision dedicated projects/namespaces through their supported owners
before qualification; this runner never converts an existing namespace into a
fixture by adding a label.

## Infrastructure attestation

The estate file is a closed JSON document:

```json
{
  "schema_version": "astronomer-estate-attestation-v1",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "base_origin": "https://management.example.test",
  "qualification_id": "qualification-20261023",
  "observed_at": "2026-10-23T08:00:00Z",
  "attested_by": "infrastructure-operator",
  "infrastructure_source": "https://infrastructure.example.test/inventory/capture-id",
  "members": {
    "00000000-0000-0000-0000-000000000001": {
      "project_id": "00000000-0000-0000-0000-000000000011",
      "namespace": "qualification",
      "nodes": [{
        "name": "primary-node",
        "system_uuid": "10000000-0000-0000-0000-000000000001",
        "infrastructure_host_id": "provider/physical-host-one"
      }]
    },
    "00000000-0000-0000-0000-000000000002": {
      "project_id": "00000000-0000-0000-0000-000000000012",
      "namespace": "qualification",
      "nodes": [{
        "name": "secondary-node",
        "system_uuid": "10000000-0000-0000-0000-000000000002",
        "infrastructure_host_id": "provider/physical-host-two"
      }]
    }
  }
}
```

Collect the mapping from the infrastructure owner, including the underlying host
for container/VM nodes. A node name, IP, cluster UUID or invented host label is
not independent-host evidence. The source URL identifies that captured mapping;
the runner does not fetch it or authenticate the named operator. SHA256 pins the
reviewed bytes; it is not a signature or independent proof of their truth.
Operator review of infrastructure provenance remains a trust boundary.

The file must be at most 30 minutes old, no more than 30 seconds in the future,
and bind the current origin, commit and task ID. Every configured member and node
must be present exactly once. Duplicate system UUIDs reject, and host IDs cannot
span member clusters. API reads traverse all node/namespace pages, require explicit
pagination completeness and corroborate each node's name, Ready status and system
UUID. Extra/missing nodes, changed reported system UUIDs and ownership drift block the preflight.
Lists are bounded to 100 pages and 10,000 rows. Unknown API state is a failure,
never an inferred empty or healthy result.

## Remaining qualification boundary

A clean preflight does not prove deployed image digests/signatures, independent
failure-domain recovery, application lifecycle behavior or cleanup. Bind a new
immutable signed candidate and port reviewed mutation executors before executing
the 174 cases. Keep original ui-demo until replacement, data migration and
runtime acceptance are complete.
