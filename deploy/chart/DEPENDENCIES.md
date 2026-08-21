# Chart dependencies

The Astronomer 1.1.0 management-plane chart has no Helm dependencies. A
packaged chart can therefore be linted, rendered, and installed without Helm
contacting a chart repository.

Flux controllers are a managed-cluster dependency, not a management-plane
chart dependency. The release pipeline publishes their signed, digest-pinned
distribution alongside the chart and records the supported Flux, Kubernetes,
and agent protocol versions in `deploy/release/compatibility.yaml`. Online
installations resolve that release artifact through OCI; disconnected
installations use the release's verified local asset. Neither path installs
Flux into the management cluster.

Release provenance, signatures, software-bill-of-materials documents, and
third-party license notices are distributed with the release bundle. Changing
an artifact version or digest requires rebuilding and signing the release
bundle; it is not a chart dependency update.
