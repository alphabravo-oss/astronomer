#!/usr/bin/env bash
# Execute the real RC rehearsal and upgrade scripts against deterministic CLI
# doubles. This is a behavior test: the scripts parse real manifests, build
# real backup/evidence files, and make every lifecycle decision themselves.
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
work="$(mktemp -d)"
cleanup() {
  status=$?
  rm -rf -- "$work"
  rm -f -- "$root/rc-rehearsal-evidence.json"
  exit "$status"
}
trap cleanup EXIT

bin="$work/bin"
state="$work/state"
trace="$work/trace.log"
mkdir -p "$bin" "$state/target" "$state/previous" "$work/chart/astronomer/files"
: >"$trace"
export RC_HARNESS_STATE="$state" RC_HARNESS_TRACE="$trace"

target=v1.2.0
previous=v1.1.0
source_run=777
producer_run=888
source_commit="$(printf 'b%.0s' {1..40})"
previous_commit="$(printf 'c%.0s' {1..40})"
webhook_id=11111111-1111-1111-1111-111111111111

printf '{"postgresql":{"minimum_upgrade_schema":1,"target_schema":2}}\n' \
  >"$work/chart/astronomer/files/release-compatibility.json"
tar -czf "$state/target/astronomer-1.2.0.tgz" -C "$work/chart" astronomer
cp "$state/target/astronomer-1.2.0.tgz" "$state/previous/astronomer-1.1.0.tgz"

image_refs=()
for component in server worker agent migrate frontend shell; do
  ordinal=$((${#image_refs[@]} + 1))
  digest="sha256:$(printf '%064d' "$ordinal")"
  repository="astronomer-go-${component}"
  [[ "$component" == frontend || "$component" == shell ]] && repository="astronomer-${component}"
  ref="ghcr.io/alphabravo-oss/${repository}@${digest}"
  image_refs+=("$ref")
  printf '%s\n' "$ref" >"$state/target/${component}.digest"
done
image_json="$(printf '%s\n' "${image_refs[@]}" | jq -R . | jq -s \
  'to_entries | map({name:(["server","worker","agent","migrate","frontend","shell"][.key]),reference:.value})')"
target_chart="sha256:$(sha256sum "$state/target/astronomer-1.2.0.tgz" | awk '{print $1}')"
previous_chart="sha256:$(sha256sum "$state/previous/astronomer-1.1.0.tgz" | awk '{print $1}')"
jq -n --arg commit "$source_commit" --arg chart "$target_chart" --argjson images "$image_json" '{
  schema_version:1,
  release:{version:"v1.2.0",source_commit:$commit},
  astronomer:{
    chart:{kind:"helm_chart",content_digest:$chart}, images:$images,
    runtime_images:[
      {source_reference:"busybox:1.36",reference:("fixture@sha256:"+("7"*64))},
      {source_reference:"postgres:16-alpine",reference:("fixture@sha256:"+("8"*64))},
      {source_reference:"valkey/valkey:8-alpine",reference:("fixture@sha256:"+("9"*64))},
      {source_reference:"dexidp/dex:v2.41.1",reference:("fixture@sha256:"+("a"*64))},
      {source_reference:"fluent/fluent-bit:3.2.4",reference:("fixture@sha256:"+("d"*64))}
    ]
  }
}' >"$state/target/release-manifest.json"
jq -n --arg commit "$previous_commit" --arg chart "$previous_chart" --argjson images "$image_json" \
  '{release:{version:"v1.1.0",source_commit:$commit},astronomer:{chart:{content_digest:$chart},images:$images}}' \
  >"$state/previous/release-manifest.json"
printf '{}\n' | tee "$state/target/release-manifest.sigstore.json" \
  "$state/previous/release-manifest.sigstore.json" >/dev/null

make_stub() {
  name="$1"
  sed '1s|.*|#!/usr/bin/env bash|' >"$bin/$name"
  chmod 0755 "$bin/$name"
}

make_stub gh <<'STUB'
set -euo pipefail
printf 'gh %s\n' "$*" >>"$RC_HARNESS_TRACE"
if [[ "$1 $2" == "api repos/alphabravo-oss/astronomer/actions/runs/777" ]]; then
  branch=v1.2.0
  [[ "${RC_HARNESS_BAD_RUN:-0}" == 0 ]] || branch=main
  printf '{"head_sha":"%s","path":".github/workflows/release.yaml","head_branch":"%s"}\n' \
    "$(printf 'b%.0s' {1..40})" "$branch"
elif [[ "$1" == api && "$*" == *contents/deploy/chart/values-k3d.yaml* ]]; then
  printf 'replicaCount: 1\n'
elif [[ "$1 $2" == "run download" ]]; then
  while (($#)); do [[ "$1" == --dir ]] && { output="$2"; break; }; shift; done
  mkdir -p "$output/artifact-one"
  cp -a "$RC_HARNESS_STATE/target/." "$output/artifact-one/"
  if [[ "${RC_HARNESS_DUP_TARGET:-0}" == 1 ]]; then
    mkdir -p "$output/artifact-two"
    cp "$RC_HARNESS_STATE/target/release-manifest.json" "$output/artifact-two/"
  fi
elif [[ "$1 $2" == "release download" ]]; then
  while (($#)); do [[ "$1" == --dir ]] && { output="$2"; break; }; shift; done
  mkdir -p "$output"
  cp -a "$RC_HARNESS_STATE/previous/." "$output/"
elif [[ "$1 $2" == "attestation verify" ]]; then
  :
else
  exit 2
fi
STUB

make_stub k3d <<'STUB'
set -euo pipefail
printf 'k3d %s\n' "$*" >>"$RC_HARNESS_TRACE"
if [[ "$1 $2" == "cluster list" ]]; then
  if [[ "${RC_HARNESS_PREEXISTING:-0}" == 1 ]]; then
    printf '[{"name":"astronomer-rc-888"}]\n'
  else
    printf '[]\n'
  fi
elif [[ "$1 $2" == "cluster create" || "$1 $2" == "cluster delete" ]]; then
  :
else
  exit 2
fi
STUB

make_stub cosign <<'STUB'
set -euo pipefail
printf 'cosign %s\n' "$*" >>"$RC_HARNESS_TRACE"
STUB

make_stub pg_restore <<'STUB'
set -euo pipefail
printf 'pg_restore %s\n' "$*" >>"$RC_HARNESS_TRACE"
[[ "$1" == --list ]] && printf 'archive inventory\n'
STUB

make_stub curl <<'STUB'
set -euo pipefail
url="${*: -1}"
printf 'curl %s\n' "$*" >>"$RC_HARNESS_TRACE"
case "$url" in
  */api/v1/auth/login/) printf '{"data":{"token":"rc-auth-token"}}\n' ;;
  */api/v1/admin/webhooks) printf '{"data":{"id":"11111111-1111-1111-1111-111111111111"}}\n' ;;
  *) printf '{}\n' ;;
esac
STUB

make_stub helm <<'STUB'
set -euo pipefail
printf 'helm %s\n' "$*" >>"$RC_HARNESS_TRACE"
if [[ "$*" == *"upgrade --help"* ]]; then
  printf '%s\n' --reset-then-reuse-values
elif [[ "$*" == *" status "* && "$*" == *"--output json"* ]]; then
  printf '{"info":{"status":"deployed"}}\n'
elif [[ "$*" == *" status "* ]]; then
  printf 'deployed\n'
elif [[ "$*" == *" get metadata "* ]]; then
  printf '{"chart":"astronomer","version":"1.1.0"}\n'
elif [[ "$*" == *" get values "* ]]; then
  printf 'replicaCount: 1\n'
elif [[ "$*" == *" history "* ]]; then
  printf '[{"revision":1}]\n'
elif [[ "$*" == *" get manifest "* ]]; then
  jq -r '.astronomer.images[].reference' "$RC_HARNESS_STATE/target/release-manifest.json"
  printf 'AGENT_IMAGE_REPOSITORY: "%s"\nAGENT_IMAGE_TAG: "v1.2.0"\n' \
    "$(cat "$RC_HARNESS_STATE/target/agent.digest")"
elif [[ "$*" == *" show chart "* ]]; then
  printf 'version: 1.2.0\n'
elif [[ "$*" == *" pull "* ]]; then
  args=("$@")
  for ((i=0; i<${#args[@]}; i++)); do
    [[ "${args[i]}" == --destination ]] && output="${args[i+1]}"
  done
  cp "$RC_HARNESS_STATE/target/astronomer-1.2.0.tgz" "$output/"
elif [[ "$*" == *"--dry-run=server"* ]]; then
  printf 'dry-run\n'
elif [[ "$*" == *"--atomic --cleanup-on-fail --wait --wait-for-jobs"* ]]; then
  printf 'target-migration\n' >>"$RC_HARNESS_TRACE"
  touch "$RC_HARNESS_STATE/target-upgraded"
  [[ "${RC_HARNESS_DUP_EVIDENCE:-0}" == 0 ]] || {
    mkdir -p "$SANITIZED_EVIDENCE_DIR"
    printf '{}\n' >"$SANITIZED_EVIDENCE_DIR/extra.json"
  }
  [[ "${RC_HARNESS_DUP_BACKUP:-0}" == 0 ]] || {
    mkdir -p "$BACKUP_ROOT/extra"
    printf 'duplicate\n' >"$BACKUP_ROOT/extra/BACKUP_SHA256SUMS"
  }
elif [[ "$*" == *"upgrade --install astronomer "* ]]; then
  printf 'previous-install\n' >>"$RC_HARNESS_TRACE"
elif [[ "$*" == *"upgrade --install ngf "* || "$*" == *" rollback "* ]]; then
  :
else
  exit 2
fi
STUB

make_stub kubectl <<'STUB'
set -euo pipefail
printf 'kubectl %s\n' "$*" >>"$RC_HARNESS_TRACE"
if [[ "$*" == *"config view"* ]]; then
  printf 'https://127.0.0.1:6443'
elif [[ "$*" == *"auth can-i"* ]]; then
  printf 'yes\n'
elif [[ "$*" == *" get nodes "* ]]; then
  printf '{"items":[{"spec":{},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}\n'
elif [[ "$*" == *"get deployments"* ]]; then
  printf '{"items":[{"metadata":{"name":"astronomer-server","generation":1,"labels":{"app.kubernetes.io/component":"server"}},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":1,"updatedReplicas":1}},{"metadata":{"name":"astronomer-worker","generation":1,"labels":{"app.kubernetes.io/component":"worker"}},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":1,"updatedReplicas":1}}]}\n'
elif [[ "$*" == *"get poddisruptionbudgets"* ]]; then
  printf '{"items":[{"status":{"disruptionsAllowed":1}}]}\n'
elif [[ "$*" == *"get secret astronomer-bootstrap"* ]]; then
  printf 'cGFzc3dvcmQ='
elif [[ "$*" == *"get secrets"* ]]; then
  printf 'apiVersion: v1\nitems: []\n'
elif [[ "$*" == *"get pods"* ]]; then
  printf 'postgres-0'
elif [[ "$*" == *" get service "* ]]; then
  printf 'astronomer-server\n'
elif [[ "$*" == *" apply "* || "$*" == *" rollout status "* ]]; then
  :
elif [[ "$*" == *" scale "* ]]; then
  [[ "$*" != *"--replicas=0"* ]] || printf 'quiesce %s\n' "$*" >>"$RC_HARNESS_TRACE"
elif [[ "$*" == *"port-forward"* ]]; then
  while :; do sleep 10; done
elif [[ "$*" == *" exec "* ]]; then
  [[ "$*" != *pg_database_size* ]] || printf '4096\n'
  [[ "$*" != *"pg_dump --format=custom"* ]] || printf 'archive\n'
  if [[ "$*" == *webhook_subscriptions* ]]; then
    [[ "${RC_HARNESS_MISSING_RESTORED_SECRET:-0}" == 0 ]] && printf '1\n' || printf '0\n'
  fi
  [[ "$*" != *"ALTER DATABASE"* ]] || printf 'restore-live\n' >>"$RC_HARNESS_TRACE"
  if [[ "$*" == *schema_migrations* ]]; then
    [[ -f "$RC_HARNESS_STATE/target-upgraded" ]] && printf '1|2|f\n' || printf '1|1|f\n'
  fi
else
  exit 2
fi
STUB

sink="$work/webhook-sink"
cat >"$sink" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
while (($#)); do
  [[ "$1" == --out ]] && { output="$2"; shift 2; continue; }
  shift
done
printf '{"verified":true,"body_sha256":"sha256:%064d"}\n' 1 >"$output"
while :; do sleep 10; done
STUB
chmod 0755 "$sink"

run_rehearsal() {
  rm -f "$state/target-upgraded" "$root/rc-rehearsal-evidence.json"
  : >"$trace"
  env PATH="$bin:$PATH" GITHUB_RUN_ID="$producer_run" \
    RC_DISPOSABLE_ACK="destroy-astronomer-rc-${producer_run}" \
    RC_WEBHOOK_SINK="$sink" "$@" \
    "$root/scripts/rehearse-release-candidate.sh" "$target" "$source_run" "$previous"
}

line_of() {
  grep -n -m1 -F "$1" "$trace" | cut -d: -f1
}

run_rehearsal >/dev/null
jq -e --arg target "$target" --arg previous "$previous" --arg commit "$source_commit" '
  .result == "passed" and .target_version == $target and .previous_version == $previous and
  .source_commit == $commit and .backup_restore == "passed" and .decrypt_proof == "passed" and
  .clean_restore == "passed" and .destructive_fence == "owned_disposable_k3d"
' "$root/rc-rehearsal-evidence.json" >/dev/null

# Artifact/source identity and previous-vs-target separation are observable in
# the actual command stream, including distinct trust identities and values.
grep -Fq "gh run download $source_run" "$trace"
grep -Fq "certificate-identity https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/$target" "$trace"
grep -Fq "certificate-identity https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/$previous" "$trace"
grep -Fq "ref=$previous_commit" "$trace"
grep -Fq 'previous-install' "$trace"
grep -Fq 'image.server.tag=v1.1.0' "$trace"
grep -Fq 'image.server.tag=v1.2.0' "$trace"

# The safety-critical order is quiesce old workloads, promote the verified
# restored database, then run the target chart migration.
quiesce_line="$(line_of 'quiesce ')"
restore_line="$(line_of 'restore-live')"
migration_line="$(line_of 'target-migration')"
((quiesce_line < restore_line && restore_line < migration_line))

# Product proof is invoked after upgrade and the exact fenced cluster is the
# only destructive cleanup target.
grep -Fq "/api/v1/admin/webhooks/${webhook_id}/test" "$trace"
grep -Fq "k3d cluster delete astronomer-rc-${producer_run}" "$trace"

expect_failure() {
  setting="$1"
  message="$2"
  : >"$trace"
  if run_rehearsal "$setting=1" >"$work/failure.out" 2>&1; then
    printf 'expected failure for %s\n' "$setting" >&2
    exit 1
  fi
  grep -Fq "$message" "$work/failure.out"
  [[ ! -e "$root/rc-rehearsal-evidence.json" ]]
}

expect_failure RC_HARNESS_BAD_RUN 'source run identity mismatch'
expect_failure RC_HARNESS_PREEXISTING 'refusing pre-existing cluster'
expect_failure RC_HARNESS_DUP_TARGET 'duplicate target artifact release-manifest.json'
expect_failure RC_HARNESS_MISSING_RESTORED_SECRET 'temporary restore verification failed'
expect_failure RC_HARNESS_DUP_EVIDENCE 'expected exactly one upgrade evidence and backup manifest'
expect_failure RC_HARNESS_DUP_BACKUP 'expected exactly one upgrade evidence and backup manifest'

# A wrong destructive acknowledgement must fail before cluster creation and
# therefore cannot authorize cleanup of any cluster.
: >"$trace"
if env PATH="$bin:$PATH" GITHUB_RUN_ID="$producer_run" RC_DISPOSABLE_ACK=wrong \
  RC_WEBHOOK_SINK="$sink" "$root/scripts/rehearse-release-candidate.sh" \
  "$target" "$source_run" "$previous" >"$work/bad-ack.out" 2>&1; then
  printf 'wrong destructive acknowledgement unexpectedly succeeded\n' >&2
  exit 1
fi
grep -Fq 'set exact RC_DISPOSABLE_ACK' "$work/bad-ack.out"
if grep -Fq 'cluster create' "$trace" || grep -Fq 'cluster delete' "$trace"; then
  printf 'wrong destructive acknowledgement reached cluster lifecycle commands\n' >&2
  exit 1
fi

printf 'RC rehearsal executable stub harness passed\n'
