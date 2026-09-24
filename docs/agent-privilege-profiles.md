# Agent management and user RBAC

Astronomer agents support full cluster management. The generated installation
binds the agent ServiceAccount to a ClusterRole with Kubernetes API management
permissions. The registration wizard has no agent mode selector or profile badge.
Each user's Astronomer RBAC grants determine which clusters, namespaces,
resources, and actions they can access. Mutation auditing and authorization
remain mandatory before requests reach the agent.

## Existing installations

Re-render and apply the agent installation manifest to upgrade an older Viewer,
Operator, namespace-scoped, or custom installation to full management:

```bash
kubectl apply --server-side --field-manager=astronomer-bootstrap -f agent-install.yaml
```

Use the current manifest downloaded through the cluster's registration workflow.
An image-only upgrade cannot broaden an existing Kubernetes role. Run live
agent diagnostics after applying the manifest to verify its installed permissions.

The legacy `astronomer.io/agent-privilege-profile` annotation,
`Cluster.spec.agent.privilegeProfile`, and `AgentProfile.spec.privilegeProfile`
remain readable for compatibility. They no longer choose the permissions in
newly rendered installation manifests. Historical API profile and capability
fields may describe the prior enrollment; they are not authorization decisions
or a measurement of live Kubernetes permissions. Explicit legacy runtime
profiles remain interpretable until their installation is reapplied.

## Install metadata

Referenced `AgentProfile` objects can still supply `install.image`,
`install.serviceAccountName`, and `install.podLabels`. These customize the agent
installation without choosing a separate access mode.

## Delivery and scanning

Flux reconciles accepted assignments using scoped delivery service accounts.
Central placement, policy, approval, and rollout checks still apply. Full agent
management does not grant users additional permissions or bypass those checks.

Trivy is automatically installed on remote clusters with compatible, ready Flux
inventory unless `astronomer.io/image-scanning` is `disabled`. This includes
existing Ready clusters and clusters without the optional metrics baseline.
See [image scanning](image-scanning.md) for the opt-out and lifecycle details.
