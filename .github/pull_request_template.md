## Summary

Describe the user-visible change and the main implementation boundary.

## Validation

- [ ] `go test ./...`
- [ ] Frontend type-check or relevant UI tests
- [ ] Helm render/lint when chart values/templates change
- [ ] Manual validation noted, or not applicable

## Security Review

- [ ] Threat model impact reviewed against `docs/threat-model.md`, or N/A
- [ ] New or changed route has auth, RBAC, API-token scope, and audit behavior reviewed, or N/A
- [ ] New secret/token/credential storage is hashed or encrypted and listed in `docs/secret-column-inventory.md`, or N/A
- [ ] New tunnel/proxy path strips caller credentials, blocks impersonation/SSRF where relevant, and has route tests, or N/A
- [ ] New external HTTP client has timeout, TLS posture, redirect behavior, and credential handling reviewed, or N/A
- [ ] New Kubernetes RBAC uses the narrowest feasible verbs/resources and documents any cluster-admin requirement, or N/A
- [ ] Browser session, cookie, CSRF, CSP, or stream-token behavior is covered by tests when touched, or N/A
- [ ] CRD/Postgres/ArgoCD ownership changes update `docs/control-plane-state-contract.md`, or N/A
- [ ] Helm/chart changes preserve NetworkPolicy, container security contexts, production preflight, and render tests, or N/A
- [ ] New images/charts/Argo sources document pinning, registry/mirror behavior, and required runtime permissions, or N/A
- [ ] Image, Dockerfile, or release-workflow changes preserve Trivy scan, SBOM, signature, and provenance coverage for every affected first-party image, or N/A
