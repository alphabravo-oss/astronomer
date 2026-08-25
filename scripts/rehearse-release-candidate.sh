#!/usr/bin/env bash
# Disposable management plane: install prior -> backup/decrypt -> exact upgrade -> clean restore.
set -euo pipefail

die() { printf 'rc-rehearsal: %s\n' "$*" >&2; exit 1; }
[[ $# == 3 ]] || die "usage: $0 <target-v1.x.y> <source-run-id> <previous-v1.x.y>"
target="$1"; source_run_id="$2"; previous="$3"
[[ "$target" =~ ^v1\.[0-9]+\.[0-9]+$ && "$previous" =~ ^v1\.[0-9]+\.[0-9]+$ ]] || die "exact stable v1 tags required"
[[ "$source_run_id" =~ ^[1-9][0-9]*$ ]] || die "source run id must be numeric"
[[ "$(printf '%s\n%s\n' "$previous" "$target" | sort -V | tail -1)" == "$target" && "$previous" != "$target" ]] || die "target must be newer"
run_id="${GITHUB_RUN_ID:-}"; [[ "$run_id" =~ ^[1-9][0-9]*$ ]] || die "GITHUB_RUN_ID is required"
cluster="astronomer-rc-${run_id}"; ack="${RC_DISPOSABLE_ACK:-}"
[[ "$cluster" =~ ^astronomer-rc-[1-9][0-9]*$ && "$ack" == "destroy-${cluster}" ]] || die "set exact RC_DISPOSABLE_ACK=destroy-${cluster}"
for tool in gh jq k3d kubectl helm cosign sha256sum openssl python3 pg_restore curl tar sort find; do command -v "$tool" >/dev/null || die "missing $tool"; done
k3d cluster list -o json | jq -e --arg name "$cluster" 'any(.[]; .name==$name)' >/dev/null && die "refusing pre-existing cluster"
work="$(mktemp -d)"; chmod 0700 "$work"; created=0; port_forward_pid=""; sink_pid=""; started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
cleanup() {
  status=$?
  [[ -z "$port_forward_pid" ]] || kill "$port_forward_pid" >/dev/null 2>&1 || true
  [[ -z "$sink_pid" ]] || kill "$sink_pid" >/dev/null 2>&1 || true
  if ((created)); then
    if [[ "$cluster" =~ ^astronomer-rc-[1-9][0-9]*$ && "$ack" == "destroy-${cluster}" ]]; then k3d cluster delete "$cluster" >/dev/null || true; else printf 'cleanup fence changed; cluster retained: %s\n' "$cluster" >&2; fi
  fi
  rm -rf -- "$work"
  return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

repo="${RELEASE_REPO:-alphabravo-oss/astronomer}"
run_json="$(gh api "repos/${repo}/actions/runs/${source_run_id}")"
source_commit="$(jq -er '.head_sha|select(test("^[a-f0-9]{40}$"))' <<<"$run_json")"
[[ "$(jq -r .path <<<"$run_json")" == ".github/workflows/release.yaml" && "$(jq -r .head_branch <<<"$run_json")" == "$target" ]] || die "source run identity mismatch"
mkdir "$work/target" "$work/previous" "$work/backups" "$work/evidence"
gh run download "$source_run_id" --repo "$repo" --pattern "*-${target}" --dir "$work/download"
while IFS= read -r -d '' file; do
  name="$(basename "$file")"; [[ ! -e "$work/target/$name" ]] || die "duplicate target artifact $name"; cp -- "$file" "$work/target/$name"
done < <(find "$work/download" -type f -print0)
for file in release-manifest.json release-manifest.sigstore.json "astronomer-${target#v}.tgz" server.digest worker.digest agent.digest migrate.digest frontend.digest shell.digest; do [[ -f "$work/target/$file" ]] || die "missing target artifact $file"; done
identity="https://github.com/${repo}/.github/workflows/release.yaml@refs/tags/${target}"
cosign verify-blob --bundle "$work/target/release-manifest.sigstore.json" --certificate-identity "$identity" --certificate-oidc-issuer https://token.actions.githubusercontent.com "$work/target/release-manifest.json" >/dev/null
gh release download "$previous" --repo "$repo" --dir "$work/previous" --pattern "astronomer-${previous#v}.tgz" --pattern release-manifest.json --pattern release-manifest.sigstore.json
cosign verify-blob --bundle "$work/previous/release-manifest.sigstore.json" --certificate-identity "https://github.com/${repo}/.github/workflows/release.yaml@refs/tags/${previous}" --certificate-oidc-issuer https://token.actions.githubusercontent.com "$work/previous/release-manifest.json" >/dev/null
previous_chart="$work/previous/astronomer-${previous#v}.tgz"
previous_chart_digest="sha256:$(sha256sum "$previous_chart" | awk '{print $1}')"
[[ "$(jq -er '.astronomer.chart.content_digest' "$work/previous/release-manifest.json")" == "$previous_chart_digest" ]] || die "previous chart bytes do not match signed release manifest"
previous_commit="$(jq -er '.release.source_commit|select(test("^[a-f0-9]{40}$"))' "$work/previous/release-manifest.json")"
gh api -H 'Accept: application/vnd.github.raw+json' "repos/${repo}/contents/deploy/chart/values-k3d.yaml?ref=${previous_commit}" >"$work/previous/values-k3d.yaml"
[[ -s "$work/previous/values-k3d.yaml" ]] || die "previous release values are unavailable"

k3d cluster create "$cluster" --image rancher/k3s:v1.35.0-k3s1 --k3s-arg '--disable=traefik@server:*' --wait
created=1; context="k3d-${cluster}"
actual_api="$(kubectl --context "$context" config view --minify --raw -o jsonpath='{.clusters[0].cluster.server}')"; [[ "$actual_api" == https://* ]] || die "unsafe API identity"
kubectl --context "$context" apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.1/standard-install.yaml
helm --kube-context "$context" upgrade --install ngf oci://ghcr.io/nginx/charts/nginx-gateway-fabric --version 2.6.0 --namespace nginx-gateway --create-namespace --wait --timeout 5m
openssl rand -base64 32 >"$work/jwt-key"
python3 - "$work/fernet-key" <<'PY'
import base64, os, sys
open(sys.argv[1], 'wb').write(base64.urlsafe_b64encode(os.urandom(32)) + b'\n')
PY
chmod 0600 "$work/jwt-key" "$work/fernet-key"
previous_ref() { jq -er --arg name "$1" '.astronomer.images[]|select(.name==$name)|.reference' "$work/previous/release-manifest.json"; }
server_ref="$(previous_ref server)"; worker_ref="$(previous_ref worker)"; agent_ref="$(previous_ref agent)"; migrate_ref="$(previous_ref migrate)"; frontend_ref="$(previous_ref frontend)"; shell_ref="$(previous_ref shell)"
helm --kube-context "$context" upgrade --install astronomer "$previous_chart" --namespace astronomer --create-namespace -f "$work/previous/values-k3d.yaml" \
  --set-file secrets.secretKey="$work/jwt-key" --set-file secrets.encryptionKey="$work/fernet-key" --set-file release.manifest="$work/previous/release-manifest.json" \
  --set-string image.server.registry=ghcr.io/alphabravo-oss --set-string image.server.repository=astronomer-go-server --set-string image.server.tag="$previous" --set-string image.server.digest="${server_ref##*@}" \
  --set-string image.worker.registry=ghcr.io/alphabravo-oss --set-string image.worker.repository=astronomer-go-worker --set-string image.worker.tag="$previous" --set-string image.worker.digest="${worker_ref##*@}" \
  --set-string image.agent.registry=ghcr.io/alphabravo-oss --set-string image.agent.repository=astronomer-go-agent --set-string image.agent.tag="$previous" --set-string image.agent.digest="${agent_ref##*@}" \
  --set-string image.migrate.registry=ghcr.io/alphabravo-oss --set-string image.migrate.repository=astronomer-go-migrate --set-string image.migrate.tag="$previous" --set-string image.migrate.digest="${migrate_ref##*@}" \
  --set-string frontend.image.registry=ghcr.io/alphabravo-oss --set-string frontend.image.repository=astronomer-frontend --set-string frontend.image.tag="$previous" --set-string frontend.image.digest="${frontend_ref##*@}" \
  --set-string preflight.image.registry=ghcr.io/alphabravo-oss --set-string preflight.image.repository=astronomer-shell --set-string preflight.image.tag="$previous" --set-string preflight.image.digest="${shell_ref##*@}" \
  --set-string config.agentImageRepository="$agent_ref" --set-string config.agentImageTag="$previous" --set-string kubectlShell.image="$shell_ref" \
  --set-string worker.env.ASTRONOMER_RC_ALLOW_PRIVATE_WEBHOOKS=true \
  --atomic --wait --timeout 15m
postgres="$(kubectl --context "$context" -n astronomer get pods -l app.kubernetes.io/component=postgres -o jsonpath='{.items[0].metadata.name}')"; [[ -n "$postgres" ]] || die "bundled PostgreSQL absent"
kubectl --context "$context" -n astronomer port-forward svc/astronomer-server 18080:8000 >"$work/port-forward.log" 2>&1 & port_forward_pid=$!
for _ in $(seq 1 30); do curl -fsS http://127.0.0.1:18080/health/ >/dev/null 2>&1 && break; sleep 1; done
if ! kill -0 "$port_forward_pid" 2>/dev/null || ! curl -fsS http://127.0.0.1:18080/health/ >/dev/null; then
  die "RC API port-forward did not become ready"
fi
bootstrap_password="$(kubectl --context "$context" -n astronomer get secret astronomer-bootstrap -o jsonpath='{.data.password}' | base64 -d)"; [[ -n "$bootstrap_password" ]] || die "bootstrap credential unavailable"
login_payload="$(jq -cn --arg email admin@astronomer.local --arg password "$bootstrap_password" '{email:$email,password:$password}')"
auth_token="$(curl -fsS -H 'Content-Type: application/json' --data-binary "$login_payload" http://127.0.0.1:18080/api/v1/auth/login/ | jq -er '.data.token')"; unset bootstrap_password login_payload
openssl rand -hex 32 >"$work/webhook-secret"; chmod 0600 "$work/webhook-secret"
webhook_sink="${RC_WEBHOOK_SINK:-scripts/rc-webhook-sink.py}"
"$webhook_sink" --secret-file "$work/webhook-secret" --out "$work/webhook-proof.json" --port 18081 >"$work/webhook-sink.log" 2>&1 & sink_pid=$!
webhook_payload="$(jq -cn --arg secret "$(<"$work/webhook-secret")" '{name:"rc-upgrade-decrypt-proof",url:"http://host.k3d.internal:18081/",secret:$secret,event_filters:["webhook.test_ping"],enabled:true,max_retries:0,timeout_seconds:10}')"
webhook_id="$(curl -fsS -H 'Content-Type: application/json' -H "Authorization: Bearer $auth_token" --data-binary "$webhook_payload" http://127.0.0.1:18080/api/v1/admin/webhooks | jq -er '.data.id')"; unset webhook_payload

KUBE_CONTEXT="$context" EXPECTED_KUBE_API_SERVER="$actual_api" RELEASE_ARTIFACT_DIR="$work/target" BACKUP_ROOT="$work/backups" SANITIZED_EVIDENCE_DIR="$work/evidence" RC_DECRYPT_PROOF_WEBHOOK_ID="$webhook_id" RC_REPLACE_DATABASE_FROM_BACKUP=1 scripts/upgrade-release.sh --yes "$target"
kill "$port_forward_pid" >/dev/null 2>&1 || true; wait "$port_forward_pid" 2>/dev/null || true
kubectl --context "$context" -n astronomer port-forward svc/astronomer-server 18080:8000 >"$work/port-forward-upgraded.log" 2>&1 & port_forward_pid=$!
for _ in $(seq 1 30); do curl -fsS http://127.0.0.1:18080/health/ >/dev/null 2>&1 && break; sleep 1; done
if ! kill -0 "$port_forward_pid" 2>/dev/null || ! curl -fsS http://127.0.0.1:18080/health/ >/dev/null; then
  die "upgraded RC API port-forward did not become ready"
fi
curl -fsS -X POST -H "Authorization: Bearer $auth_token" -H 'Idempotency-Key: rc-upgrade-decrypt-proof' "http://127.0.0.1:18080/api/v1/admin/webhooks/${webhook_id}/test" >/dev/null
for _ in $(seq 1 120); do [[ -f "$work/webhook-proof.json" ]] && break; sleep 1; done
jq -e '.verified == true and (.body_sha256|test("^sha256:[a-f0-9]{64}$"))' "$work/webhook-proof.json" >/dev/null || die "upgraded product did not decrypt and sign the restored proof subscription"
unset auth_token
mapfile -t upgrade_evidence_files < <(find "$work/evidence" -maxdepth 1 -name '*.json' -type f -print)
mapfile -t backup_checksum_files < <(find "$work/backups" -name BACKUP_SHA256SUMS -type f -print)
[[ "${#upgrade_evidence_files[@]}" == 1 && "${#backup_checksum_files[@]}" == 1 ]] || die "expected exactly one upgrade evidence and backup manifest"
upgrade_evidence="${upgrade_evidence_files[0]}"; backup_checksums="${backup_checksum_files[0]}"
inner="$(scripts/validate-rc-rehearsal-inner.py --upgrade-evidence "$upgrade_evidence" --backup-manifest "$backup_checksums" --release-manifest "$work/target/release-manifest.json" --target "$target")" || die "inner upgrade/restore evidence validation failed"
upgrade_digest="$(jq -er '.upgrade_evidence_sha256' <<<"$inner")"; backup_digest="$(jq -er '.backup_manifest_sha256' <<<"$inner")"
output="${RC_EVIDENCE_OUTPUT:-rc-rehearsal-evidence.json}"; [[ "$output" != */* ]] || die "RC_EVIDENCE_OUTPUT must be a local filename"
[[ ! -e "$output" && ! -L "$output" ]] || die "refusing to overwrite RC evidence"
jq -n --arg target "$target" --arg previous "$previous" --arg run "$source_run_id" --arg producer "$run_id" --arg commit "$source_commit" --arg manifest "sha256:$(sha256sum "$work/target/release-manifest.json"|awk '{print $1}')" --arg upgrade "$upgrade_digest" --arg backup "$backup_digest" --arg started "$started" --arg completed "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '{schema_version:1,result:"passed",target_version:$target,previous_version:$previous,source_run_id:$run,producer_run_id:$producer,source_commit:$commit,release_manifest_sha256:$manifest,upgrade_evidence_sha256:$upgrade,backup_manifest_sha256:$backup,backup_restore:"passed",decrypt_proof:"passed",clean_restore:"passed",destructive_fence:"owned_disposable_k3d",started_at:$started,completed_at:$completed}' >"$output"
chmod 0644 "$output"; printf '%s\n' "$output"
