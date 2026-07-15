#!/usr/bin/env bash
# Reproducible live-apiserver acceptance matrix for agent credential ownership.
#
# The script never prints Secret data. It uses a disposable namespace and
# deletes it on exit. AGENT_IDENTITY_TEST_CONTEXT is mandatory. The standard
# enterprise gate passes `--if-available`, which skips only when that variable
# is absent. Once a context is explicit, missing tools, connectivity failures,
# and acceptance failures are always fatal.

set -Eeuo pipefail

optional=false
if [[ "${1:-}" == "--if-available" ]]; then
  optional=true
elif [[ $# -ne 0 ]]; then
  printf 'Usage: AGENT_IDENTITY_TEST_CONTEXT=<context> %s [--if-available]\n' "$0" >&2
  exit 2
fi

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

context="${AGENT_IDENTITY_TEST_CONTEXT:-}"
if [[ -z "$context" ]]; then
  if [[ "$optional" == true ]]; then
    printf 'SKIP: AGENT_IDENTITY_TEST_CONTEXT is not explicitly set\n'
    exit 0
  fi
  fail "AGENT_IDENTITY_TEST_CONTEXT is required"
fi

command -v kubectl >/dev/null 2>&1 || fail "kubectl is unavailable"
command -v python3 >/dev/null 2>&1 || fail "python3 is unavailable"
kubectl --context "$context" version --request-timeout=10s >/dev/null 2>&1 \
  || fail "Kubernetes context $context is unreachable"

namespace="astronomer-agent-identity-${RANDOM}-$$"
service_account="astronomer-agent"
subject="system:serviceaccount:${namespace}:${service_account}"

cleanup() {
  kubectl --context "$context" delete namespace "$namespace" \
    --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

phase() {
  printf '  - %s\n' "$1"
}

random_token() {
  # Test-only material; never emitted to stdout or command diagnostics.
  printf 'test-%s' "$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
}

secret_value() {
  kubectl --context "$context" -n "$namespace" get secret "$1" \
    -o jsonpath='{.data.token}' | base64 -d
}

apply_token_as_agent() {
  local name="$1"
  local value="$2"
  local encoded
  encoded="$(printf '%s' "$value" | base64 | tr -d '\n')"
  printf '{"apiVersion":"v1","kind":"Secret","metadata":{"name":"%s","namespace":"%s"},"data":{"token":"%s"}}\n' \
    "$name" "$namespace" "$encoded" \
    | kubectl --context "$context" --as="$subject" apply --server-side \
      --field-manager=astronomer-agent-identity --force-conflicts -f - >/dev/null
}

assert_can_i() {
  local verb="$1"
  local name="$2"
  local expected="$3"
  local got
  got="$(kubectl --context "$context" --as="$subject" -n "$namespace" auth can-i "$verb" "secret/$name" || true)"
  [[ "$got" == "$expected" ]] || fail "auth can-i $verb secret/$name = $got, want $expected"
}

legacy_initial="$(random_token)"
legacy_rotated="$(random_token)"
cached_old="$(random_token)"
identity_rotated="$(random_token)"
bootstrap="$(random_token)"

server_version="$(kubectl --context "$context" version -o json | python3 -c 'import json,sys; print(json.load(sys.stdin)["serverVersion"]["gitVersion"])')"
printf 'Agent identity live acceptance (%s, server %s)\n' "$context" "$server_version"
kubectl --context "$context" create namespace "$namespace" >/dev/null
kubectl --context "$context" -n "$namespace" create serviceaccount "$service_account" >/dev/null

phase "image-first old env/RBAC compatibility"
kubectl --context "$context" apply -f - >/dev/null <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: astronomer-agent-token
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["astronomer-agent-token"]
    verbs: ["get", "update", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: astronomer-agent-token
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: astronomer-agent-token
subjects:
  - kind: ServiceAccount
    name: ${service_account}
    namespace: ${namespace}
---
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-agent-token
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
type: Opaque
stringData:
  token: ${legacy_initial}
EOF

assert_can_i get astronomer-agent-identity no
assert_can_i get astronomer-agent-token yes
assert_can_i patch astronomer-agent-token yes
kubectl --context "$context" -n "$namespace" get secret astronomer-agent-token -o json \
  | python3 -c '
import json, sys
obj = json.load(sys.stdin)
key = "kubectl.kubernetes.io/last-applied-configuration"
if key not in obj.get("metadata", {}).get("annotations", {}):
    raise SystemExit("client-side legacy apply did not create the expected annotation")
' >/dev/null
kubectl --context "$context" --as="$subject" -n "$namespace" patch secret astronomer-agent-token \
  --type=merge \
  -p '{"metadata":{"annotations":{"kubectl.kubernetes.io/last-applied-configuration":null}}}' \
  >/dev/null
kubectl --context "$context" -n "$namespace" get secret astronomer-agent-token -o json \
  | python3 -c '
import json, sys
obj = json.load(sys.stdin)
key = "kubectl.kubernetes.io/last-applied-configuration"
if key in obj.get("metadata", {}).get("annotations", {}):
    raise SystemExit("static legacy annotation scrub did not remove the annotation")
' >/dev/null
apply_token_as_agent astronomer-agent-token "$legacy_rotated"
[[ "$(secret_value astronomer-agent-token)" == "$legacy_rotated" ]] \
  || fail "image-first legacy rotation was not persisted"

phase "fresh current-layout apply and distinct credential RBAC"
kubectl --context "$context" apply --server-side --field-manager=astronomer-bootstrap -f - >/dev/null <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: astronomer-agent-identity
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["astronomer-agent-registration-token", "astronomer-agent-identity", "astronomer-agent-token", "astronomer-agent-ca"]
    verbs: ["get"]
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["astronomer-agent-identity", "astronomer-agent-token"]
    verbs: ["patch"]
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["astronomer-agent-registration-token", "astronomer-agent-identity", "astronomer-agent-token", "astronomer-agent-ca"]
    verbs: ["delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: astronomer-agent-identity
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: astronomer-agent-identity
subjects:
  - kind: ServiceAccount
    name: ${service_account}
    namespace: ${namespace}
---
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-agent-registration-token
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
type: Opaque
stringData:
  token: ${bootstrap}
---
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-agent-identity
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
    astronomer.io/agent-credential-purpose: durable-identity-container
type: Opaque
---
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-agent-ca
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
type: Opaque
EOF

assert_can_i get astronomer-agent-identity yes
assert_can_i get astronomer-agent-ca yes
assert_can_i patch astronomer-agent-identity yes
assert_can_i patch astronomer-agent-registration-token no
assert_can_i patch arbitrary-secret no

phase "current-layout accepted legacy migration"
apply_token_as_agent astronomer-agent-identity "$legacy_rotated"
[[ "$(secret_value astronomer-agent-identity)" == "$legacy_rotated" ]] \
  || fail "legacy migration did not populate active identity"

phase "bootstrap reapply preserves agent-owned token"
kubectl --context "$context" apply --server-side --field-manager=astronomer-bootstrap -f - >/dev/null <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-agent-registration-token
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
type: Opaque
stringData:
  token: ${bootstrap}
---
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-agent-identity
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
    astronomer.io/agent-credential-purpose: durable-identity-container
type: Opaque
EOF
[[ "$(secret_value astronomer-agent-identity)" == "$legacy_rotated" ]] \
  || fail "bootstrap reapply changed active identity"

phase "cached old manifest cannot overwrite new RBAC or active identity"
kubectl --context "$context" apply -f - >/dev/null <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: astronomer-agent-token
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["astronomer-agent-token"]
    verbs: ["get", "update", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: astronomer-agent-token
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: astronomer-agent-token
subjects:
  - kind: ServiceAccount
    name: ${service_account}
    namespace: ${namespace}
---
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-agent-token
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: astronomer
type: Opaque
stringData:
  token: ${cached_old}
EOF
assert_can_i get astronomer-agent-identity yes
[[ "$(secret_value astronomer-agent-identity)" == "$legacy_rotated" ]] \
  || fail "cached old manifest changed active identity"
kubectl --context "$context" -n "$namespace" get secret astronomer-agent-token -o json \
  | python3 -c '
import json, sys
obj = json.load(sys.stdin)
key = "kubectl.kubernetes.io/last-applied-configuration"
if key not in obj.get("metadata", {}).get("annotations", {}):
    raise SystemExit("cached old apply did not restore its legacy annotation")
' >/dev/null
kubectl --context "$context" --as="$subject" -n "$namespace" patch secret astronomer-agent-token \
  --type=merge \
  -p '{"metadata":{"annotations":{"kubectl.kubernetes.io/last-applied-configuration":null}}}' \
  >/dev/null
kubectl --context "$context" -n "$namespace" get secret astronomer-agent-token -o json \
  | python3 -c '
import json, sys
obj = json.load(sys.stdin)
key = "kubectl.kubernetes.io/last-applied-configuration"
if key in obj.get("metadata", {}).get("annotations", {}):
    raise SystemExit("post-cache legacy annotation scrub did not remove the annotation")
' >/dev/null

phase "active identity rotation and managedFields isolation"
apply_token_as_agent astronomer-agent-identity "$identity_rotated"
[[ "$(secret_value astronomer-agent-identity)" == "$identity_rotated" ]] \
  || fail "active identity rotation was not persisted"
kubectl --context "$context" -n "$namespace" get secret astronomer-agent-identity -o json --show-managed-fields \
  | python3 -c '
import json, sys
obj = json.load(sys.stdin)
entries = {e.get("manager"): e for e in obj.get("metadata", {}).get("managedFields", []) if e.get("operation") == "Apply"}
agent = entries.get("astronomer-agent-identity")
bootstrap = entries.get("astronomer-bootstrap")
if not agent or not bootstrap:
    raise SystemExit("required managedFields managers are absent; available=" + ",".join(sorted(entries)))
af = agent.get("fieldsV1", {})
bf = bootstrap.get("fieldsV1", {})
if "f:token" not in af.get("f:data", {}):
    raise SystemExit("agent manager does not own data.token")
if "f:labels" in af.get("f:metadata", {}) or "f:type" in af:
    raise SystemExit("agent manager owns installer fields")
if "f:data" in bf:
    raise SystemExit("bootstrap manager owns active token data")
' >/dev/null

phase "exact-name delete authorization and cleanup"
for name in astronomer-agent-registration-token astronomer-agent-identity astronomer-agent-token astronomer-agent-ca; do
  assert_can_i delete "$name" yes
done
assert_can_i delete arbitrary-secret no
for name in astronomer-agent-identity astronomer-agent-registration-token astronomer-agent-token astronomer-agent-ca; do
  kubectl --context "$context" --as="$subject" -n "$namespace" delete secret "$name" >/dev/null
done
for name in astronomer-agent-registration-token astronomer-agent-identity astronomer-agent-token astronomer-agent-ca; do
  if kubectl --context "$context" -n "$namespace" get secret "$name" >/dev/null 2>&1; then
    fail "credential cleanup left managed Secret $name"
  fi
done

printf 'PASS: agent identity live acceptance matrix\n'
