# Grafana observability validation — 2026-09-23

The user reported Grafana's application-file error, expected automatic sign-in,
and requested Grafana as the interface for metrics and logs. This report
supersedes the Grafana UI claim in the earlier two-member validation.

## Result

Both retained members, `astro-demo-a` and `astro-demo-b`, now open Grafana from
cluster Metrics, Logging, and Grafana navigation. Metrics opens dashboards;
Logging opens Loki Explore. Pipeline configuration remains behind **Configure
log collection**. No additional custom log query interface was added.

Browser acceptance against the deployed site passed for both members:

- Grafana application assets load and the home page renders.
- `/api/user` matches the signed-in Astronomer account; its organization role
  is Admin. No separate Grafana login or anonymous viewer session is used.
- Prometheus, Loki, and Alertmanager data sources are available, with 27
  provisioned dashboards per member.
- The Kubernetes cluster dashboard displays CPU, memory, and namespace data.
- Grafana's own POST query endpoint returns real Prometheus frames and log
  lines emitted within 90 seconds of the check.
- Loki Explore renders collected logs. Both clusters have logs from eight
  namespaces, including system, monitoring, delivery, Trivy, and demo workloads.
- Final browser checks recorded zero HTTP errors, page exceptions, or failed
  HTTP requests. Grafana Live is disabled; WebSocket tailing is not claimed.
- Requests sent directly to Grafana from another workload with forged
  `X-WEBAUTH-USER` and Admin role headers receive HTTP 401.

The existing K3s and both K3d members are retained. Management Helm revision is
127. The final server image is
`localhost/astronomer-go-server@sha256:8700ee2b7952382e719a4a30ed21269146b8a97cee530fcb1c00595ec1e973cb`;
the frontend image is
`localhost/astronomer-frontend@sha256:d614d75f2dd75c784567e0e55df2f7ddd6e1fb3a53b87139a70e57bd46ed68ca`.
The original K3s process remains PID 938597. Database/Redis StatefulSets,
Services, Secrets, ingress, and PVCs were unchanged by the management upgrades.

## Root causes and changes

Kubernetes' service proxy rewrote Grafana's HTML base and asset URLs with its
private API prefix. The browser consequently fetched JavaScript and CSS from
nonexistent public routes. The response adapter removes that exact prefix and
keeps Grafana's public subpath. The upstream serves root-relative routes behind
the authenticated proxy.

Cluster Grafana previously explicitly enabled anonymous Viewer access. It now
uses a ticket-verifying proxy sidecar and trusts auth-proxy identity headers only
from loopback. Astronomer rechecks cluster permission and issues a fresh one-use
identity ticket on each request. Browser session cookies and caller-supplied
identity headers never pass to Grafana. Cluster monitoring readers map to Viewer,
updaters to Editor, and Astronomer superusers to Admin.

Grafana's fetch/XHR requests need Astronomer's double-submit CSRF header. A small
browser adapter attaches the existing CSRF cookie only to unsafe requests under
the same Grafana origin/path. API CSRF checks remain enabled. Cross-origin,
unrelated-path, and safe-method requests receive no token.

The Helm values initialize data sources before Grafana starts. Loki is provisioned
by `monitoring/astronomer-cluster-loki-datasource` on each member. Managed Fluent
Bit pipelines now collect all namespaces with cluster, namespace, pod, and
container labels. The earlier demo-only pipelines are disabled.

## Validation and limits

Full handler and Grafana proxy tests passed; targeted tests passed after the
final chart configuration changes. Frontend type checking, lint, and 89 tests
across four relevant files passed. Go vet, pinned Go lint (zero issues),
dependency boundaries, complexity ratchets, and whitespace checks passed.
Actual Helm chart rendering caught and corrected an extra-container YAML issue
before the member upgrade. Rendered charts include the authenticated sidecar,
private proxy Service, and data-source initialization container.

The tunnel is an HTTP request/response transport, so Grafana Live and Loki's
WebSocket live-tail are not supported. Regular queries and timed dashboard
refresh work. `live.max_connections=0` uses the documented Grafana setting:
[Grafana configuration](https://grafana.com/docs/grafana/latest/setup-grafana/configure-grafana/#max_connections).

At 21:39:54 UTC the management server lost its embedded local-agent leadership
and restarted once. The site briefly returned 503. It recovered with all three
cluster connections; browser acceptance then passed. The interrupted member
upgrade was retried through the monitoring operation API. Its admission hook
needed the chart-generated ServiceAccount and RBAC resources restored after
interrupted hook cleanup; the Helm release then reached deployed status. The K3s process and
database/Redis did not restart during this work.

An existing pipeline-update SQL issue was also observed: replacing unchanged
output associations triggers `logging_pipeline_outputs_pkey`. This change does
not fix that separate query. All-namespace collection was configured through
canonical create and disable operations, preserving the earlier pipelines.

Private evidence is in `/var/tmp/astronomer-grafana-fix-20260923`: final browser
results, screenshots, Grafana query responses, chart previews, operation status,
image metadata, and checks. No credentials were committed. No commit or push
was performed.
