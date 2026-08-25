# Sanitized provider API contract fixtures

These fixtures retain only the documented control-plane allow-list fields used
by Astronomer. Names, subscriptions, projects, resource groups, cluster IDs,
addresses, request IDs, and credentials are synthetic. They model these public
provider operations:

- AWS EKS `DescribeCluster` and `UpdateClusterConfig`;
- Google Kubernetes Engine `projects.locations.clusters.get/update` using
  `controlPlaneEndpointsConfig.ipEndpointsConfig.authorizedNetworksConfig`;
- Azure Managed Clusters `GET/PUT` with `apiServerAccessProfile` and `ETag`;
- DigitalOcean Kubernetes cluster `GET/PUT` with
  `control_plane_firewall.allowed_addresses`.

Contract tests compare outgoing JSON semantically, verify method/path/query and
optimistic-concurrency headers, and run every provider's 403 response through
the shared authorization taxonomy. No fixture contains a real tenant value.
