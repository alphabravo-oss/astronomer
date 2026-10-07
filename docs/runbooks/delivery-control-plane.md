# Flux-native delivery control-plane operations

This runbook covers the Astronomer-owned delivery API, durable rollout engine,
cluster-agent protocol, and the exact signed Flux distribution. Treat
PostgreSQL as the control-plane authority and the agent-reported Flux inventory
as downstream evidence. Never repair state by editing Flux resources, rollout
rows, generation counters, or agent checkpoints by hand.

Before any intervention, record the release manifest digest, rollout and target
IDs, frozen plan digest, affected cluster IDs, current generation/fence,
controller inventory, recent bounded event codes, and the relevant metric
window. Pause the rollout when continued cohort release could enlarge impact.
Do not include source URLs, rendered values, credentials, Secrets, or raw
manifests in tickets or support bundles.

## Stuck rollout

1. Inspect the rollout timeline and distinguish approval, maintenance-window,
   worker/outbox, assignment acknowledgment, and downstream readiness waits.
2. Confirm a healthy worker owns or can reclaim the expired lease. Check
   `astronomer_task_outbox_*` and `astronomer_delivery_worker_events_total`.
3. Confirm the cluster agent has acknowledged the exact snapshot generation
   and ETag. Follow [delivery assignment lag](delivery-assignment-lag.md) when it
   has not.
4. Confirm source and reconciler objects report the assignment generation and
   digest. Resume or retry through the rollout API with its current ETag; never
   modify a cohort or lease directly.

## Failed rollout or failure budget

The frozen strategy and failure budget are immutable. Group failures by the
bounded reason code, correct the source, compatibility, admission, quota, or
workload problem, then use `retry-failed`. If policy selected rollback, let the
same rollout drive the exact previous bundle assignment. Creating a second
overlapping rollout is not recovery.

## Rollback failure

Pause other rollouts to the affected targets. Confirm the previous bundle
version and source revision still exist in the release/air-gap mirror and that
their verification identity is valid. Inspect admission-policy and immutable
field failures. Retry the failed rollback assignment through the API. If the
previous artifact is unavailable or its signature no longer verifies, stop and
restore the verified mirror or backup; do not substitute a rebuilt artifact
under the old digest.

## Source authentication, signature, or render failure

Use the source resolution’s stable error code to choose the path:

- authentication: rotate only the referenced credential, then verify;
- network policy: validate the explicit hostname plus CIDR allowlist, DNS
  answers, optional HTTPS proxy, and custom CA;
- signature: verify the exact Git object or OCI/chart digest against the
  operator-mounted key or keyless issuer/identity;
- digest mismatch: quarantine the mutable upstream reference and investigate;
- limit exceeded: inspect the artifact rather than raising limits blindly;
- render/health: reproduce with the immutable revision and bounded renderer
  contract, never with a moving branch or tag.

Resolver errors deliberately omit upstream output and secret-bearing URLs.
Inspect sensitive upstream diagnostics only in the source system.

## Helm remediation and Kustomize health timeout

For Helm, inspect the normalized HelmRelease conditions and bounded failure
code, then correct chart inputs or cluster prerequisites in a new immutable
bundle version. Let Helm remediation use the frozen retry/rollback policy. For
Kustomize, identify the unhealthy inventory entries and correct health checks,
dependencies, or admission failures in source. Do not force a reconcile with a
different revision or patch generated objects in place.

## Drift loop or stuck deletion

Repeated drift normally means another actor owns the same field or resource.
Use managed fields and the assignment inventory to identify that actor, then
remove the ownership conflict or narrow the bundle. During deletion, the agent
prunes only objects in the accepted checkpoint after the replacement apply has
succeeded. Never remove finalizers broadly. If a finalizer owner is gone,
follow the component’s recovery procedure and record every exact object before
a narrowly scoped finalizer change.

## Flux distribution or controller outage

Confirm all and only the pinned source, kustomize, and helm controllers are
present with the signed distribution digest and supported API versions. Check
resource pressure, leader election, admission, DNS, and registry reachability.
Use a canaried system-release rollout to repair or upgrade the distribution.
Do not install Flux CLI bootstrap output or a fourth controller alongside the
managed distribution.

## Agent disconnect or stale status

Follow [delivery status stale](delivery-status-stale.md). Verify tunnel session
fencing, clock, protocol version, snapshot acknowledgment, queue/coalescing
counters, and full-resync behavior. Reconnecting must retain the accepted
checkpoint and must not prune workloads while assignments are unavailable.

## Credential rotation and revocation

Write the replacement credential through the source API, verify the source,
and confirm its credential epoch advances. Existing assignments receive only
the new encrypted projection and snapshot epoch; their immutable intent digest
does not change. Revocation fails closed for new resolution. Never copy a
credential between database rows or place it in a rollout/event payload.

## Air-gap mirror drift

Run the release mirror verifier against the signed release manifest. Every
chart, image, Flux distribution, and built-in bundle must retain its exact
digest and provenance reference. Restore missing blobs from the release kit.
Do not retag or rebuild content to satisfy an existing digest.

## Database restore and fresh reinstall

Use the management-plane backup/restore runbook and verify the single v1 schema,
assignment generations, rollout fences, audit chain, and release manifest
before reopening traffic. A v0.3.x database is intentionally rejected without
mutation; greenfield v1 requires a fresh install, with only explicitly exported
configuration and credentials re-entered through supported APIs.

## Disposable resilience drill recovery

`scripts/run-delivery-resilience-drill.py` exercises only the existing bounded
mutation actions in an explicitly disposable, pre-provisioned namespace. It
requires the exact confirmation below and both qualification ownership labels
on the namespace and every mutation target. Its report always has
`release_eligible: false`: successful actions and cleanup do not establish
change-to-observed freshness, reconnect correctness, or release qualification.

Validate the checked-in example without contacting Kubernetes:

```bash
python3 scripts/run-delivery-resilience-drill.py \
  --manifest scripts/testdata/delivery-resilience-drill.example.json --validate-only
```

After deliberately preparing the example's disposable cluster and labeled
resources, use a private, durable directory on the runner host. This example is
an invocation pattern, not an instruction to mutate a shared cluster:

```bash
mkdir -m 700 drill-evidence
python3 scripts/run-delivery-resilience-drill.py \
  --manifest scripts/testdata/delivery-resilience-drill.example.json \
  --evidence drill-evidence/report.json \
  --confirm delete-only-owned:k3d-astronomer-qualification-example-001:astronomer-qualification-example-001:example-001
```

The runner writes `report.json.ledger.json` with mode 0600, atomically replacing
and fsyncing the file and parent directory before each mutation. Retain this
ledger and the report on durable storage. The ledger contains the manifest
digest, run identity, context, cluster identity (the `kube-system` namespace
UID), target namespace UID, resource GVK/name/UID, operation nonce, and only the
replica count or restart annotation needed for restoration. It never stores Job
templates, pod environment, Secrets, credentials, or remote command output.
Restart annotations must be timestamps; other preexisting values are refused.
Inputs are bounded to 1 MiB for manifests and 4 MiB for kubectl input/output and
individual checkpoints. A limit or command error fails closed with a fixed
classification; raw kubectl stderr is discarded.

After a crash, use the **same host, absolute ledger location, manifest, context,
and namespace** to clean up without replaying scenarios:

```bash
python3 scripts/run-delivery-resilience-drill.py \
  --manifest scripts/testdata/delivery-resilience-drill.example.json \
  --evidence drill-evidence/report.json --resume-cleanup \
  --confirm delete-only-owned:k3d-astronomer-qualification-example-001:astronomer-qualification-example-001:example-001
```

Recovery preserves the original scenario history and run status. A run without
a completed report remains `interrupted`; cleanup success is reported
separately as `cleanup_status: passed`. A resume command exits successfully when
cleanup succeeds, which does not mean the original drill passed. A conflicting
or unverifiable cleanup remains unresolved, retains the ledger and cluster
lock, and requires investigation. Do not delete the ledger or lock to bypass
that refusal. An absent object after an ambiguous create with no recorded UID,
or an unapplied-looking field after an ambiguous mutation, cannot establish
that the request will never finish; these cases deliberately remain unresolved.

Coordination is deliberately limited. A private local `flock`, keyed by cluster
and namespace UID across evidence paths, excludes concurrent runners on the
same host and Unix user. Its fixed `/tmp/astronomer-drill-locks-<uid>` directory
is independent of `TMPDIR`, `TEMP`, and `TMP`; runners must share the host
filesystem namespace. Separate containers with private `/tmp` directories are
not supported recovery peers. A create-only, labeled `qualification-drill-lock` ConfigMap excludes
new runs while recovery is pending. Recovery accepts only its recorded nonce
and UID; it never steals or recreates a missing or replaced lock. The persisted
host and ledger-location hashes are accidental-misuse guards, not authentication.
Cross-host recovery, copied ledgers, cloned host identities, and remote concurrent
resume are unsupported. This is not a distributed lease or fencing service.
The host must provide `/etc/machine-id`, Linux `flock`, and a durable filesystem
with atomic rename/fsync semantics. Failure to read cluster identity prevents
all mutations.

The additional coordination permissions are `get/create/delete` on ConfigMaps
in the disposable namespace (create is needed for `qualification-drill-lock`),
and `get` on the target and `kube-system` Namespace objects. Existing actions
also need resource-specific `get`, workload `patch` and rollout watches, pod
`list/delete`, NetworkPolicy and Job `create/delete`, Job wait watches, and
CronJob `get`. No Secret access, namespace deletion, new cluster-wide mutation
permission, or automatic RBAC installation is required. Grant only the actions
selected by the manifest. The runner uses `kubectl delete --raw=... -f -` with
Kubernetes DeleteOptions UID and resourceVersion preconditions, and JSON Patch
UID/resourceVersion tests for field updates; the installed kubectl must support
these options. These client paths have hermetic tests, not a claim of live
cluster qualification.

Cleanup runs in reverse order and stops at the first unresolved operation.
Create-only Jobs and NetworkPolicies refuse preexisting objects, even if their
labels match. Successful create responses supply the UID receipt, persisted
before verification; even a replacement copying the same labels and nonce is
rejected. When the response is lost or a timeout leaves no UID receipt,
reconciliation can only bind an object with the exact persisted operation nonce
and ownership labels. That ambiguous-response path cannot distinguish an
external actor copying the nonce before the first successful read; it is not
cryptographic proof of creation. The disposable namespace must remain under
the operator's exclusive control.
Fresh ownership, UID, and field checks prevent deleting replacements or
restoring over externally changed replicas or restart annotations. Restoring
the restart annotation cannot undo restarted processes or rollout history.
Deleting a Pod is explicitly irreversible: cleanup verifies/removes only the
recorded Pod UID and never recreates it. A controller may replace it; a same-name
replacement causes refusal rather than adoption. There is no namespace cleanup.

### Controlled annotation-to-SSE observation

The optional `observe_annotation` action uses this same runner and ledger to
measure **mutation dispatch to receipt on the server's SSE endpoint**. It does
not measure UI rendering, delivery convergence, an entire estate, or reconnect
recovery. It creates no fixture and grants no permissions. Prepare a dedicated,
owned Deployment in the disposable namespace; never relabel an installed agent
or shared workload to make it eligible.

Start from `scripts/testdata/delivery-observation-drill.example.json`. Its
`observer` configuration requires a credential-free origin `base_url`, the
actual registered `cluster_id`, and `bearer_token_file`; optional `ca_file`
supplies PEM trust roots. The listener's bearer token is a private regular file
owned by the runner user, mode 0600, at most 8 KiB. It may differ from the
kubectl actor's credentials. Token values must never appear in the manifest,
command line, environment, report, or issue attachments. Validation does not
open credentials or contact either endpoint:

```bash
python3 scripts/run-delivery-resilience-drill.py \
  --manifest scripts/testdata/delivery-observation-drill.example.json --validate-only
```

Run or resume using the existing `--evidence`, exact `--confirm`, and
`--resume-cleanup` pattern above, substituting the observation manifest. The
same host, private ledger, namespace identity, ownership, and recovery rules
apply. Use a new evidence path for a new execution.

Verified HTTPS is the normal connection. Numeric-loopback HTTP, such as
`http://127.0.0.1:8001` or `http://[::1]:8001`, is accepted for an explicitly
configured local port-forward development setup, matching the estate harness.
Other cleartext origins, URL credentials, paths, query strings, fragments, and
redirects are refused. The client ignores environment proxy settings and never
forwards credentials to a redirected origin. Custom CA files must be regular,
nonsymlink PEM files of at most 1 MiB. There is no TLS verification bypass.

Before each trial, the runner GETs the `kube-system` and disposable Namespace
objects through `/api/v1/clusters/{cluster_id}/k8s/api/v1/namespaces/{name}` and
compares their UIDs with the direct kubectl context and ledger. Failure or
mismatch prevents annotation mutation. The listener needs access to this
mapping proof and `/api/v1/events/stream/`. Under the current proxy parser,
named Namespace GETs are classified as `clusters:list`; use a grant scoped to
the selected cluster. SSE authorization accepts `clusters:read` or
`clusters:list`. The downstream proxy identity must be able to get those
Namespace objects. The separate kubectl actor needs `get/patch` on the fixture
Deployment plus the existing ledger permissions. This feature makes no RBAC
changes, installs no agents, and needs no Secret access.

Each trial requires a settled Deployment with 1–100 desired replicas and
matching observed generation, updated/ready/available counts. Two reads two
seconds apart must have identical UID and resourceVersion. The listener then
consumes the complete `: connected` SSE comment, which the server flushes after
installing its subscription. Only then does a UID/RV-guarded patch set the fixed
**top-level metadata** annotation
`delivery.astronomer.io/qualification-observation` to a unique nonce. The pod
template is unchanged. An existing annotation must be absent or in the
runner's `q-<32 hexadecimal digits>` format; other values are refused rather
than copied into the ledger.

The successful patch's JSON response supplies the authoritative UID/RV receipt.
The reader timestamps frames as they arrive, even before that response returns.
A successful trial requires an exact `cluster.k8s_changed` envelope matching
cluster, Deployment/apps/v1, namespace, name and opaque resourceVersion, plus an
independent GET verifying the same UID and nonce. RVs are compared as strings;
a later RV or GET success alone is insufficient. The event does not carry a
UID. Agent/server coalescing, filtering, disconnects, and throttling can prevent
the exact event from reaching the listener; these remain unobserved rather
than being inferred successful. A baseline without this event contract cannot
produce equivalent evidence, and its validation must not be weakened.

Limits are explicit: 1–100 sequential trials; a 30-second connection/barrier
budget; up to 120 seconds per trial; and a total step budget of at most 1,800
seconds (600 by default). The step budget includes setup, mapping checks, quiet
periods, patch, observation, verification, and cleanup. Once exhausted, later
trials remain `not_run`. Safety cleanup is still attempted with the existing
bounded kubectl drain after measurement time expires; this can overrun the
measurement budget and cannot turn that trial into a pass. A missing exact
event may allow another trial after successful cleanup; other errors or an
unresolved cleanup stop the step. Expanded repetitions count toward the
ledger's 10,001-operation ceiling including its lock.

Frames are capped at 64 KiB, each stream at 8 MiB/10,000 frames, and matching
scope candidates at 256. Ordinary observer responses are capped at 1 MiB.
Malformed data, overflow, transport failure, or a failed subscription barrier
is explicit failed evidence, with fixed sanitized reason codes. There are no
silent retries, reconnects, dropped candidates, or arbitrary cleartext fallbacks.
Socket ownership is retained through TLS handshakes and `Connection: close`
responses so deadlines can interrupt reads. OS DNS resolution cannot be
force-cancelled: a daemon resolver may finish after the caller's bounded wait,
but it only returns addresses and cannot create a late application connection
or send credentials. The runner does not claim that every OS resolver worker
has joined when a failed trial returns.

Reports retain every requested trial with `succeeded`, `timeout`, `missed`,
`error`, or `not_run`, and expose attempted and per-status counts. P95 is absent
(`null`) unless at least 20 requested trials all succeeded; successful subsets
are never presented as overall freshness. Timing starts just before dispatching
kubectl and therefore includes process/request overhead, not just server
propagation. Exact resource names, nonce, returned RV and monotonic timestamps
stay in the private ledger. Public trial entries contain indices, fixed codes,
duration, cleanup status, and ledger operation references. The report binds its
final ledger digest and the source digest of the runner/ledger/observer files.

Restoration uses the existing guarded ledger path, including after parser or
transport failure and after a crash. A replacement UID, changed ownership, or
externally changed annotation causes unresolved cleanup rather than an
overwrite. Failed/interrupted run history remains intact on recovery. Neither
successful observation nor successful cleanup changes `release_eligible:false`.
