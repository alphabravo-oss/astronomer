#!/usr/bin/env bash
# Run the Playwright live project against disposable PostgreSQL/Redis and the
# current server, worker, and frontend preview processes.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ "${1:-}" == "--validate-only" ]]; then
  echo "test-live-browser: static contract OK"
  exit 0
fi
if [[ $# -ne 0 ]]; then
  echo "Usage: scripts/test-live-browser.sh [--validate-only]" >&2
  exit 2
fi

trivy_enabled="${LIVE_BROWSER_TRIVY_ENABLED:-0}"
if [[ "$trivy_enabled" != 0 && "$trivy_enabled" != 1 ]]; then
	echo "test-live-browser: LIVE_BROWSER_TRIVY_ENABLED must be 0 or 1" >&2
	exit 2
fi
trivy_chart_version="0.36.0"
trivy_chart_digest="sha256:bb0c44bc70e3158cc63ea49d7458ede6db5b6c932f6585a2d787eb9712fd4288"
trivy_rollout_id=""
trivy_target_id=""

for tool in base64 curl docker git go helm k3d kubectl npm openssl python3 setsid sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "test-live-browser: missing required tool: $tool" >&2
    exit 2
  }
done
if [[ ! -x frontend/node_modules/.bin/playwright ]]; then
  echo "test-live-browser: frontend dependencies are missing; run npm ci in frontend/" >&2
  exit 2
fi

suffix="$$-$(date +%s)-$(openssl rand -hex 4)"
artifact_dir="${LIVE_BROWSER_ARTIFACT_DIR:-${TMPDIR:-/tmp}/astronomer-live-browser-$suffix}"
mkdir -p "$artifact_dir/bin" "$artifact_dir/playwright-report" "$artifact_dir/test-results"
artifact_dir="$(cd "$artifact_dir" && pwd)"

postgres_container="astronomer-live-browser-pg-$suffix"
redis_container="astronomer-live-browser-redis-$suffix"
postgres_user="live_browser"
postgres_database="live_browser"
postgres_credential="$(openssl rand -hex 24)"
signing_key="$(openssl rand -hex 32)"
encryption_key="$(openssl rand -base64 32 | tr '+/' '-_')"
admin_password="$(openssl rand -hex 24)"
restricted_password="Live-$(openssl rand -hex 18)-A9!"
server_port=8000
frontend_port="${LIVE_BROWSER_FRONTEND_PORT:-}"
server_metrics_port=""
worker_metrics_port=""
server_pid=""
worker_pid=""
frontend_pid=""
fixture_pid=""
flux_fixture_pid=""
flux_cluster="test-run-live-$((BASHPID % 1000000))-$(openssl rand -hex 2)"
flux_kubeconfig="$artifact_dir/flux-kubeconfig"
direct_kubeconfig="$artifact_dir/direct-kubeconfig"
direct_api_ca="$artifact_dir/direct-api-ca.pem"
direct_api_endpoint=""
direct_ca_sha256=""
flux_fixture_root="$artifact_dir/flux-fixture"
flux_fixture_port_file="$artifact_dir/flux-fixture.port"
flux_tls_cert="$artifact_dir/flux-fixture-ca.pem"
flux_tls_key="$artifact_dir/flux-fixture-key.pem"
flux_created=0
backup_namespace="live-backup-source"
backup_configmap="live-backup-payload"
backup_message="before-backup-round-trip-$suffix"
minio_user="live-browser"
minio_password="$(openssl rand -hex 24)"

port_is_free() {
  python3 - "$1" <<'PY'
import socket
import sys

port = int(sys.argv[1])
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        sock.bind(("127.0.0.1", port))
    except OSError:
        raise SystemExit(1)
PY
}

port_is_free_at() {
  python3 - "$1" "$2" <<'PY'
import socket
import sys

host, port = sys.argv[1], int(sys.argv[2])
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        sock.bind((host, port))
    except OSError:
        raise SystemExit(1)
PY
}

choose_port() {
  python3 <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

process_alive() {
  [[ -n "$1" ]] && kill -0 "$1" >/dev/null 2>&1
}

stop_process() {
  local pid="$1"
  [[ -n "$pid" ]] || return 0
  if process_alive "$pid"; then
    kill -TERM -- "-$pid" >/dev/null 2>&1 || kill -TERM "$pid" >/dev/null 2>&1 || true
    for _ in $(seq 1 50); do
      process_alive "$pid" || return 0
      sleep 0.1
    done
    kill -KILL -- "-$pid" >/dev/null 2>&1 || kill -KILL "$pid" >/dev/null 2>&1 || true
  fi
}

collect_state() {
  local phase="${1:-cleanup}"
  {
    echo "phase=$phase"
    for item in "server:$server_pid" "worker:$worker_pid" "frontend:$frontend_pid" "agent-fixture:$fixture_pid"; do
      name="${item%%:*}"
      pid="${item#*:}"
      if process_alive "$pid"; then
        echo "$name=alive"
      else
        echo "$name=stopped"
      fi
    done
    if docker inspect -f '{{.State.Running}}' "$postgres_container" 2>/dev/null | grep -qx true; then
      echo "postgres=alive"
    else
      echo "postgres=stopped"
    fi
    if docker inspect -f '{{.State.Running}}' "$redis_container" 2>/dev/null | grep -qx true; then
      echo "redis=alive"
    else
      echo "redis=stopped"
    fi
  } >>"$artifact_dir/process-state.log"
}

cleanup() {
  status=$?
  trap - EXIT INT TERM
  set +e
  collect_state "exit-$status"
  rm -f -- "$direct_kubeconfig"
  docker logs "$postgres_container" >"$artifact_dir/postgres.log" 2>&1 || true
  docker logs "$redis_container" >"$artifact_dir/redis.log" 2>&1 || true
	if [[ "$trivy_enabled" == 1 && -n "$trivy_target_id" ]] && docker inspect -f '{{.State.Running}}' "$postgres_container" 2>/dev/null | grep -qx true; then
		docker exec "$postgres_container" psql -X -U "$postgres_user" -d "$postgres_database" -c \
			"SELECT r.id AS rollout_id,r.state,d.phase,d.desired_generation,d.observed_generation,d.desired_spec_digest,d.observed_spec_digest,d.last_error_code FROM delivery_rollouts r LEFT JOIN cluster_deployments d ON d.target_id=r.target_id AND d.cluster_id='$cluster_id' WHERE r.target_id='$trivy_target_id'; SELECT report_name,namespace,image_repo,image_tag,critical_count,high_count,medium_count,low_count,unknown_count,scanned_at FROM image_vulnerability_reports WHERE cluster_id='$cluster_id' AND namespace='live-delivery';" \
			>"$artifact_dir/trivy-database-evidence.log" 2>&1 || true
	fi
	if [[ "$flux_created" == 1 ]]; then
		KUBECONFIG="$flux_kubeconfig" kubectl get gitrepositories.source.toolkit.fluxcd.io,kustomizations.kustomize.toolkit.fluxcd.io -A -o yaml >"$artifact_dir/flux-objects.yaml" 2>&1 || true
		if [[ "$trivy_enabled" == 1 ]]; then
			KUBECONFIG="$flux_kubeconfig" kubectl get helmrepositories.source.toolkit.fluxcd.io,helmreleases.helm.toolkit.fluxcd.io -A -o yaml >"$artifact_dir/trivy-flux-objects.yaml" 2>&1 || true
			KUBECONFIG="$flux_kubeconfig" kubectl get customresourcedefinitions.apiextensions.k8s.io/vulnerabilityreports.aquasecurity.github.io -o yaml >"$artifact_dir/trivy-crd.yaml" 2>&1 || true
			KUBECONFIG="$flux_kubeconfig" kubectl get vulnerabilityreports.aquasecurity.github.io,jobs,pods,deployments -A -o yaml >"$artifact_dir/trivy-workloads.yaml" 2>&1 || true
			KUBECONFIG="$flux_kubeconfig" kubectl -n astronomer-trivy-system logs deployment/trivy-operator --all-containers >"$artifact_dir/trivy-operator.log" 2>&1 || true
		fi
		KUBECONFIG="$flux_kubeconfig" kubectl get configmaps -A -l app.kubernetes.io/managed-by=astronomer-agent -o yaml >"$artifact_dir/flux-workloads.yaml" 2>&1 || true
		KUBECONFIG="$flux_kubeconfig" kubectl -n velero get backup,restore,backupstoragelocation -o yaml >"$artifact_dir/velero-objects.yaml" 2>&1 || true
		KUBECONFIG="$flux_kubeconfig" kubectl -n "$backup_namespace" get configmap "$backup_configmap" -o yaml >"$artifact_dir/velero-restored-workload.yaml" 2>&1 || true
		KUBECONFIG="$flux_kubeconfig" kubectl -n velero logs deployment/velero >"$artifact_dir/velero.log" 2>&1 || true
		KUBECONFIG="$flux_kubeconfig" kubectl get events -A --sort-by=.metadata.creationTimestamp >"$artifact_dir/kubernetes-events.log" 2>&1 || true
	fi
  stop_process "$frontend_pid"
  stop_process "$fixture_pid"
	stop_process "$flux_fixture_pid"
  stop_process "$worker_pid"
  stop_process "$server_pid"
	if [[ "$flux_created" == 1 && "$flux_cluster" == test-run-live-* ]]; then
		k3d cluster delete "$flux_cluster" >/dev/null 2>&1 || true
	fi
  docker rm -f "$postgres_container" "$redis_container" >/dev/null 2>&1 || true
  printf 'exit_status=%d\n' "$status" >"$artifact_dir/result.txt"
  echo "test-live-browser: artifacts: $artifact_dir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

wait_container_ready() {
  local name="$1"
  shift
  for _ in $(seq 1 90); do
    if docker exec "$name" "$@" >/dev/null 2>&1; then
      return 0
    fi
    if ! docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null | grep -qx true; then
      echo "test-live-browser: container exited before readiness: $name" >&2
      return 1
    fi
    sleep 1
  done
  echo "test-live-browser: container readiness timeout: $name" >&2
  return 1
}

wait_http_ready() {
  local name="$1"
  local pid="$2"
  local url="$3"
  for _ in $(seq 1 120); do
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    if ! process_alive "$pid"; then
      echo "test-live-browser: $name exited before readiness" >&2
      return 1
    fi
    sleep 1
  done
  echo "test-live-browser: $name readiness timeout" >&2
  return 1
}

assert_stack() {
  local phase="$1"
  process_alive "$server_pid" || { echo "test-live-browser: server not alive ($phase)" >&2; return 1; }
  process_alive "$worker_pid" || { echo "test-live-browser: worker not alive ($phase)" >&2; return 1; }
  process_alive "$frontend_pid" || { echo "test-live-browser: frontend not alive ($phase)" >&2; return 1; }
  process_alive "$fixture_pid" || { echo "test-live-browser: agent fixture not alive ($phase)" >&2; return 1; }
  docker exec "$postgres_container" pg_isready -U "$postgres_user" -d "$postgres_database" >/dev/null
  docker exec "$redis_container" redis-cli ping | grep -qx PONG
	curl -fsS --max-time 3 "http://127.0.0.1:$server_port/health/" >/dev/null
	curl -fsS --max-time 3 "http://127.0.0.1:$server_metrics_port/metrics" >/dev/null
	curl -fsS --max-time 3 "http://127.0.0.1:$worker_metrics_port/healthz" >/dev/null
	curl -fsS --max-time 3 "http://127.0.0.1:$frontend_port/auth/login" >/dev/null
  collect_state "$phase"
}

port_is_free "$server_port" || {
  echo "test-live-browser: 127.0.0.1:$server_port is already in use; refusing to contact an existing endpoint" >&2
  exit 2
}
if [[ -z "$frontend_port" ]]; then
	frontend_port="$(choose_port)"
fi
port_is_free "$frontend_port" || {
  echo "test-live-browser: requested frontend port is already in use" >&2
	exit 2
}
server_metrics_port="$(choose_port)"
worker_metrics_port="$(choose_port)"
while [[ "$server_metrics_port" == "$frontend_port" || "$worker_metrics_port" == "$frontend_port" || "$worker_metrics_port" == "$server_metrics_port" ]]; do
	server_metrics_port="$(choose_port)"
	worker_metrics_port="$(choose_port)"
done

docker run -d --rm --name "$postgres_container" \
  -e POSTGRES_USER="$postgres_user" \
  -e POSTGRES_PASSWORD="$postgres_credential" \
  -e POSTGRES_DB="$postgres_database" \
  -p 127.0.0.1::5432 postgres:16-alpine >/dev/null
docker run -d --rm --name "$redis_container" \
  -p 127.0.0.1::6379 redis:7-alpine >/dev/null
wait_container_ready "$postgres_container" pg_isready -U "$postgres_user" -d "$postgres_database"
wait_container_ready "$redis_container" redis-cli ping

postgres_port="$(docker port "$postgres_container" 5432/tcp | awk -F: 'NR==1 {print $NF}')"
redis_port="$(docker port "$redis_container" 6379/tcp | awk -F: 'NR==1 {print $NF}')"
database_url="postgres://$postgres_user:$postgres_credential@127.0.0.1:$postgres_port/$postgres_database?sslmode=disable"
redis_url="redis://127.0.0.1:$redis_port/0"

direct_api_host="$(docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}')"
[[ -n "$direct_api_host" ]] || {
	echo "test-live-browser: Docker bridge has no routable gateway for direct API validation" >&2
	exit 1
}
port_is_free_at "$direct_api_host" 443 || {
	echo "test-live-browser: $direct_api_host:443 is already in use; direct API acceptance requires a supported production port" >&2
	exit 2
}
direct_api_endpoint="https://$direct_api_host:443"

echo "test-live-browser: creating isolated Flux member cluster $flux_cluster"
mkdir -p "$flux_fixture_root/git" "$flux_fixture_root/helm" "$artifact_dir/flux-worktree"
cp -R scripts/fixtures/flux/kustomize "$artifact_dir/flux-worktree/kustomize"
if [[ "$trivy_enabled" == 1 ]]; then
	cp scripts/testdata/live-browser-fixture/trivy-scan-target.yaml "$artifact_dir/flux-worktree/kustomize/"
	printf '  - trivy-scan-target.yaml\n' >>"$artifact_dir/flux-worktree/kustomize/kustomization.yaml"
	helm pull trivy-operator --repo https://aquasecurity.github.io/helm-charts/ \
		--version "$trivy_chart_version" --destination "$flux_fixture_root/helm" \
		>"$artifact_dir/trivy-chart-pull.log"
	helm repo index "$flux_fixture_root/helm"
	trivy_chart_archive="$flux_fixture_root/helm/trivy-operator-$trivy_chart_version.tgz"
	[[ -f "$trivy_chart_archive" ]] || { echo "test-live-browser: pinned Trivy chart archive is missing" >&2; exit 1; }
	actual_trivy_chart_digest="sha256:$(sha256sum "$trivy_chart_archive" | awk '{print $1}')"
	[[ "$actual_trivy_chart_digest" == "$trivy_chart_digest" ]] || {
		echo "test-live-browser: pinned Trivy chart digest mismatch" >&2
		exit 1
	}
fi
git -C "$artifact_dir/flux-worktree" init --initial-branch=main >/dev/null
git -C "$artifact_dir/flux-worktree" config user.name "Astronomer Live Browser"
git -C "$artifact_dir/flux-worktree" config user.email "live-browser@example.invalid"
git -C "$artifact_dir/flux-worktree" add kustomize
git -C "$artifact_dir/flux-worktree" commit -m "live browser Flux fixture" >/dev/null
git_revision="$(git -C "$artifact_dir/flux-worktree" rev-parse HEAD)"
git clone --bare "$artifact_dir/flux-worktree" "$flux_fixture_root/git/repository.git" >/dev/null
git_digest="sha256:$(git -C "$artifact_dir/flux-worktree" archive HEAD | sha256sum | awk '{print $1}')"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
	-subj '/CN=host.k3d.internal' \
	-addext 'subjectAltName=DNS:host.k3d.internal,DNS:localhost,IP:127.0.0.1' \
	-keyout "$flux_tls_key" -out "$flux_tls_cert" >"$artifact_dir/flux-tls.log" 2>&1
chmod 0600 "$flux_tls_key" "$flux_tls_cert"

go build -trimpath -o "$artifact_dir/bin/flux-fixture-server" ./scripts/fixtures/flux/server >"$artifact_dir/build-flux-fixture.log" 2>&1
setsid "$artifact_dir/bin/flux-fixture-server" --root "$flux_fixture_root" --listen 0.0.0.0:0 \
	--port-file "$flux_fixture_port_file" --tls-cert "$flux_tls_cert" --tls-key "$flux_tls_key" \
	>"$artifact_dir/flux-fixture-server.log" 2>&1 &
flux_fixture_pid=$!
for _ in $(seq 1 50); do
	[[ -s "$flux_fixture_port_file" ]] && break
	process_alive "$flux_fixture_pid" || { echo "test-live-browser: Flux fixture server exited" >&2; exit 1; }
	sleep 0.1
done
[[ -s "$flux_fixture_port_file" ]] || { echo "test-live-browser: Flux fixture server did not publish its port" >&2; exit 1; }
flux_fixture_port="$(tr -d '\n' <"$flux_fixture_port_file")"
curl -fsS --connect-timeout 2 --max-time 5 --cacert "$flux_tls_cert" "https://localhost:$flux_fixture_port/healthz" >/dev/null
GIT_SSL_CAINFO="$flux_tls_cert" git ls-remote "https://localhost:$flux_fixture_port/git/repository.git" refs/heads/main >/dev/null

k3d cluster create "$flux_cluster" --servers 1 --agents 0 --no-lb \
	--image "${K3S_IMAGE:-rancher/k3s:v1.35.0-k3s1}" \
	--api-port "$direct_api_host:443" \
	--k3s-arg '--disable=traefik@server:0' \
	--k3s-arg "--tls-san=$direct_api_host@server:0" \
	--kubeconfig-update-default=false --kubeconfig-switch-context=false \
	--wait --timeout 4m >"$artifact_dir/k3d-create.log" 2>&1
flux_created=1
k3d kubeconfig get "$flux_cluster" >"$flux_kubeconfig"
chmod 0600 "$flux_kubeconfig"
KUBECONFIG="$flux_kubeconfig" kubectl config view --raw \
	-o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d >"$direct_api_ca"
chmod 0600 "$direct_api_ca"
direct_ca_sha256="$(openssl x509 -in "$direct_api_ca" -outform DER | sha256sum | awk '{print $1}')"
direct_probe_status="$(curl -sS --connect-timeout 2 --max-time 5 --cacert "$direct_api_ca" \
	-o "$artifact_dir/direct-api-version.json" -w '%{http_code}' "$direct_api_endpoint/version")"
[[ "$direct_probe_status" =~ ^[1-4][0-9][0-9]$ ]] || {
	echo "test-live-browser: direct API TLS probe returned HTTP $direct_probe_status" >&2
	exit 1
}
KUBECONFIG="$flux_kubeconfig" kubectl apply -f deploy/flux/install.yaml >"$artifact_dir/flux-install.log"
sed "s/__FIXTURE_PORT__/$flux_fixture_port/g" \
	scripts/testdata/live-browser-fixture/flux-fixture-egress.yaml.tmpl >"$artifact_dir/flux-fixture-egress.yaml"
KUBECONFIG="$flux_kubeconfig" kubectl apply -f "$artifact_dir/flux-fixture-egress.yaml" >/dev/null
for controller in source-controller kustomize-controller helm-controller; do
	KUBECONFIG="$flux_kubeconfig" kubectl -n astronomer-delivery-system rollout status "deployment/$controller" --timeout=5m
done
KUBECONFIG="$flux_kubeconfig" kubectl create namespace astronomer-system >/dev/null
KUBECONFIG="$flux_kubeconfig" kubectl create namespace live-delivery >/dev/null
KUBECONFIG="$flux_kubeconfig" kubectl create configmap live-config -n default \
	--from-literal=message=before --dry-run=client -o yaml | \
	KUBECONFIG="$flux_kubeconfig" kubectl label --local -f - fixture-stage=initial -o yaml | \
	KUBECONFIG="$flux_kubeconfig" kubectl apply --server-side --field-manager=astronomer -f - >/dev/null

echo "test-live-browser: installing disposable MinIO and Velero backup stack"
KUBECONFIG="$flux_kubeconfig" kubectl create namespace minio >/dev/null
KUBECONFIG="$flux_kubeconfig" kubectl -n minio create secret generic minio-root \
	--from-literal=username="$minio_user" --from-literal=password="$minio_password" >/dev/null
KUBECONFIG="$flux_kubeconfig" kubectl apply \
	-f scripts/testdata/live-browser-fixture/velero-minio.yaml >"$artifact_dir/minio-install.log"
KUBECONFIG="$flux_kubeconfig" kubectl -n minio rollout status deployment/minio --timeout=3m
KUBECONFIG="$flux_kubeconfig" kubectl -n minio run minio-mc-bootstrap \
	--image=minio/mc:RELEASE.2025-04-16T18-13-26Z --restart=Never --rm -i \
	--command -- sh -c "mc alias set local http://minio.minio.svc.cluster.local:9000 '$minio_user' '$minio_password' >/dev/null && mc mb --ignore-existing local/velero" \
	>"$artifact_dir/minio-bootstrap.log"
helm repo add vmware-tanzu https://vmware-tanzu.github.io/helm-charts >/dev/null 2>&1 || true
helm repo update vmware-tanzu >"$artifact_dir/helm-repo-update.log"
KUBECONFIG="$flux_kubeconfig" helm upgrade --install velero vmware-tanzu/velero \
	--version 11.4.0 --namespace velero --create-namespace \
	--set credentials.useSecret=false \
	--set backupsEnabled=false \
	--set snapshotsEnabled=false \
	--set deployNodeAgent=false \
	--set upgradeCRDs=false \
	--set 'initContainers[0].name=velero-plugin-for-aws' \
	--set 'initContainers[0].image=velero/velero-plugin-for-aws:v1.13.1' \
	--set 'initContainers[0].imagePullPolicy=IfNotPresent' \
	--set 'initContainers[0].volumeMounts[0].mountPath=/target' \
	--set 'initContainers[0].volumeMounts[0].name=plugins' \
	>"$artifact_dir/velero-install.log"
KUBECONFIG="$flux_kubeconfig" kubectl -n velero rollout status deployment/velero --timeout=5m
velero_cloud_credentials=$'[default]\naws_access_key_id='"$minio_user"$'\naws_secret_access_key='"$minio_password"
KUBECONFIG="$flux_kubeconfig" kubectl -n velero create secret generic live-browser-credentials \
	--from-literal=cloud="$velero_cloud_credentials" >/dev/null
KUBECONFIG="$flux_kubeconfig" kubectl apply \
	-f scripts/testdata/live-browser-fixture/velero-storage.yaml >"$artifact_dir/velero-storage-install.log"
for _ in $(seq 1 120); do
	bsl_phase="$(KUBECONFIG="$flux_kubeconfig" kubectl -n velero get backupstoragelocation live-browser -o jsonpath='{.status.phase}' 2>/dev/null || true)"
	[[ "$bsl_phase" == "Available" ]] && break
	sleep 1
done
[[ "${bsl_phase:-}" == "Available" ]] || {
	echo "test-live-browser: Velero backup storage did not become Available" >&2
	exit 1
}
KUBECONFIG="$flux_kubeconfig" kubectl create namespace "$backup_namespace" >/dev/null
KUBECONFIG="$flux_kubeconfig" kubectl -n "$backup_namespace" create configmap "$backup_configmap" \
	--from-literal=message="$backup_message" >/dev/null

echo "test-live-browser: building current migrator, server, worker, live agent fixture, and frontend"
go build -trimpath -o "$artifact_dir/bin/migrator" ./cmd/migrator >"$artifact_dir/build-migrator.log" 2>&1
go build -trimpath -o "$artifact_dir/bin/server" ./cmd/server >"$artifact_dir/build-server.log" 2>&1
go build -trimpath -o "$artifact_dir/bin/worker" ./cmd/worker >"$artifact_dir/build-worker.log" 2>&1
go build -trimpath -o "$artifact_dir/bin/live-browser-fixture" ./scripts/testdata/live-browser-fixture >"$artifact_dir/build-live-browser-fixture.log" 2>&1
npm --prefix frontend run build >"$artifact_dir/frontend-build.log" 2>&1

"$artifact_dir/bin/live-browser-fixture" direct-rbac | \
	KUBECONFIG="$flux_kubeconfig" kubectl apply -f - >"$artifact_dir/direct-reader-install.log"
[[ "$(KUBECONFIG="$flux_kubeconfig" kubectl -n astronomer-system get serviceaccount astronomer-direct-reader -o jsonpath='{.automountServiceAccountToken}')" == "false" ]] || {
	echo "test-live-browser: direct reader ServiceAccount must disable automounted credentials" >&2
	exit 1
}

"$artifact_dir/bin/migrator" -database "$database_url" -path internal/db/migrations up >"$artifact_dir/migrations.log" 2>&1
expected_schema="$(find internal/db/migrations -maxdepth 1 -name '*.up.sql' -printf '%f\n' | sort -V | tail -1 | cut -d_ -f1)"
expected_schema="$((10#$expected_schema))"
actual_schema="$(docker exec "$postgres_container" psql -X -U "$postgres_user" -d "$postgres_database" -Atc "SELECT version::text || ':' || dirty::text FROM schema_migrations")"
if [[ "$actual_schema" != "$expected_schema:false" ]]; then
  echo "test-live-browser: schema version mismatch" >&2
  exit 1
fi

runtime_env=(
  "DATABASE_URL=$database_url"
  "REDIS_URL=$redis_url"
  "SECRET_KEY=$signing_key"
  "ASTRONOMER_ENCRYPTION_KEY=$encryption_key"
  "ASTRONOMER_BOOTSTRAP_PASSWORD=$admin_password"
  "ENV=development"
  "DEBUG=true"
  "ALLOWED_HOSTS=127.0.0.1,localhost"
  "CORS_ALLOWED_ORIGINS=http://127.0.0.1:$frontend_port"
	"LOG_LEVEL=debug"
	"SERVER_METRICS_ADDR=127.0.0.1:$server_metrics_port"
	"WORKER_METRICS_ADDR=127.0.0.1:$worker_metrics_port"
	"DELIVERY_ENABLED=true"
  "MANAGEMENT_BACKUP_ENABLED=false"
)

env "${runtime_env[@]}" setsid "$artifact_dir/bin/server" >"$artifact_dir/server.log" 2>&1 &
server_pid=$!
wait_http_ready server "$server_pid" "http://127.0.0.1:$server_port/health/"
wait_http_ready server-metrics "$server_pid" "http://127.0.0.1:$server_metrics_port/metrics"

json_field() {
  python3 -c 'import json,sys; value=json.load(sys.stdin); value=value.get("data", value); print(value[sys.argv[1]])' "$1"
}

backend_url="http://127.0.0.1:$server_port"
admin_token="$(curl -fsS --max-time 10 -H 'Content-Type: application/json' \
  -d "{\"email\":\"admin@astronomer.local\",\"password\":\"$admin_password\"}" \
  "$backend_url/api/v1/auth/login/" | json_field token)"
cluster_payload="$(python3 - "$direct_api_endpoint" "$direct_api_ca" <<'PY'
import json
import pathlib
import sys

print(json.dumps({
    "name": "live-browser-cluster",
    "display_name": "Live Browser Cluster",
    "description": "authenticated tunnel fixture",
    "environment": "development",
    "provider": "other",
    "distribution": "kubernetes",
    "api_server_url": sys.argv[1],
    "ca_certificate": pathlib.Path(sys.argv[2]).read_text(),
}))
PY
)"
cluster_id="$(curl -fsS --max-time 10 -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $admin_token" \
  -d "$cluster_payload" \
  "$backend_url/api/v1/clusters/" | json_field id)"
unset cluster_payload
agent_token="$(curl -fsS --max-time 10 -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $admin_token" -d '{}' \
  "$backend_url/api/v1/clusters/$cluster_id/register/" | json_field token)"
curl -fsS --max-time 10 -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $admin_token" \
  -d "{\"email\":\"restricted-live@astronomer.local\",\"username\":\"restricted-live\",\"first_name\":\"Restricted\",\"last_name\":\"Operator\",\"password\":\"$restricted_password\",\"is_active\":true,\"is_staff\":false,\"is_superuser\":false}" \
  "$backend_url/api/v1/users/" >"$artifact_dir/restricted-user.json"

LIVE_FIXTURE_SERVER_URL="$backend_url" \
  LIVE_FIXTURE_CLUSTER_ID="$cluster_id" \
  LIVE_FIXTURE_AGENT_TOKEN="$agent_token" \
	LIVE_FIXTURE_KUBECONFIG="$flux_kubeconfig" \
  setsid "$artifact_dir/bin/live-browser-fixture" agent >"$artifact_dir/agent-fixture.log" 2>&1 &
fixture_pid=$!
for _ in $(seq 1 60); do
  connected="$(docker exec "$postgres_container" psql -X -U "$postgres_user" -d "$postgres_database" -Atc "SELECT count(*) FROM agent_connections WHERE cluster_id='$cluster_id' AND status='connected' AND disconnected_at IS NULL" 2>/dev/null || true)"
  [[ "$connected" == "1" ]] && break
  process_alive "$fixture_pid" || { echo "test-live-browser: agent fixture exited before connection" >&2; exit 1; }
  sleep 1
done
[[ "${connected:-0}" == "1" ]] || { echo "test-live-browser: agent fixture connection timeout" >&2; exit 1; }

DATABASE_URL="$database_url" LIVE_FIXTURE_CLUSTER_ID="$cluster_id" \
	ASTRONOMER_ENCRYPTION_KEY="$encryption_key" \
	LIVE_FIXTURE_GIT_URL="https://host.k3d.internal:$flux_fixture_port/git/repository.git" \
	LIVE_FIXTURE_GIT_CA="$(<"$flux_tls_cert")" \
	LIVE_FIXTURE_GIT_REVISION="$git_revision" LIVE_FIXTURE_GIT_DIGEST="$git_digest" \
	LIVE_FIXTURE_TRIVY_ENABLED="$trivy_enabled" \
	LIVE_FIXTURE_TRIVY_HELM_URL="https://host.k3d.internal:$flux_fixture_port/helm" \
	LIVE_FIXTURE_TRIVY_CHART_VERSION="$trivy_chart_version" \
	LIVE_FIXTURE_TRIVY_CHART_DIGEST="$trivy_chart_digest" \
  "$artifact_dir/bin/live-browser-fixture" seed >"$artifact_dir/fixture.json" 2>"$artifact_dir/fixture-seed.log"
project_id="$(json_field project_id <"$artifact_dir/fixture.json")"
rollout_id="$(json_field rollout_id <"$artifact_dir/fixture.json")"
rollback_rollout_id="$(json_field rollback_rollout_id <"$artifact_dir/fixture.json")"
logging_output_name="$(json_field logging_output_name <"$artifact_dir/fixture.json")"
if [[ "$trivy_enabled" == 1 ]]; then
	trivy_rollout_id="$(json_field trivy_rollout_id <"$artifact_dir/fixture.json")"
	trivy_target_id="$(json_field trivy_target_id <"$artifact_dir/fixture.json")"
	[[ -n "$trivy_rollout_id" && -n "$trivy_target_id" ]] || { echo "test-live-browser: Trivy fixture IDs are missing" >&2; exit 1; }
fi

env "${runtime_env[@]}" setsid "$artifact_dir/bin/worker" >"$artifact_dir/worker.log" 2>&1 &
worker_pid=$!
wait_http_ready worker "$worker_pid" "http://127.0.0.1:$worker_metrics_port/healthz"

if [[ "$trivy_enabled" == 1 ]]; then
	echo "test-live-browser: waiting for Flux-native Trivy generation-current reconciliation"
	trivy_delivery_state=""
	for _ in $(seq 1 720); do
		trivy_delivery_state="$(docker exec "$postgres_container" psql -X -U "$postgres_user" -d "$postgres_database" -Atc \
			"SELECT r.state || ':' || d.phase || ':' || (d.desired_generation=d.observed_generation)::text || ':' || (d.desired_spec_digest=d.observed_spec_digest)::text FROM delivery_rollouts r JOIN cluster_deployments d ON d.target_id=r.target_id AND d.cluster_id='$cluster_id' WHERE r.id='$trivy_rollout_id' AND r.target_id='$trivy_target_id'" 2>/dev/null || true)"
		[[ "$trivy_delivery_state" == "succeeded:ready:true:true" ]] && break
		if ! process_alive "$worker_pid" || ! process_alive "$fixture_pid"; then
			echo "test-live-browser: runtime exited while waiting for Trivy delivery" >&2
			exit 1
		fi
		sleep 1
	done
	printf '%s\n' "$trivy_delivery_state" >"$artifact_dir/trivy-durable-state.log"
	[[ "$trivy_delivery_state" == "succeeded:ready:true:true" ]] || { echo "test-live-browser: Trivy delivery did not become generation-current" >&2; exit 1; }
	KUBECONFIG="$flux_kubeconfig" kubectl wait --for=condition=Ready --timeout=3m \
		helmrepositories.source.toolkit.fluxcd.io -A -l app.kubernetes.io/managed-by=astronomer-agent
	KUBECONFIG="$flux_kubeconfig" kubectl wait --for=condition=Ready --timeout=8m \
		helmreleases.helm.toolkit.fluxcd.io -A -l app.kubernetes.io/managed-by=astronomer-agent
	KUBECONFIG="$flux_kubeconfig" kubectl -n astronomer-trivy-system rollout status deployment/trivy-operator --timeout=5m
	KUBECONFIG="$flux_kubeconfig" kubectl wait --for=condition=Established --timeout=2m \
		customresourcedefinition/vulnerabilityreports.aquasecurity.github.io
	trivy_report_name=""
	for _ in $(seq 1 600); do
		trivy_report_name="$(KUBECONFIG="$flux_kubeconfig" kubectl -n live-delivery get vulnerabilityreports.aquasecurity.github.io -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
		[[ -n "$trivy_report_name" ]] && break
		sleep 1
	done
	[[ -n "$trivy_report_name" ]] || { echo "test-live-browser: real Trivy VulnerabilityReport was not produced" >&2; exit 1; }
	printf '%s\n' "$trivy_report_name" >"$artifact_dir/trivy-report-name.log"
	trivy_ingested=""
	for _ in $(seq 1 180); do
		trivy_ingested="$(docker exec "$postgres_container" psql -X -U "$postgres_user" -d "$postgres_database" -Atc \
			"SELECT namespace || ':' || image_repo || ':' || (critical_count+high_count+medium_count+low_count+unknown_count)::text FROM image_vulnerability_reports WHERE cluster_id='$cluster_id' AND namespace='live-delivery' ORDER BY scanned_at DESC LIMIT 1" 2>/dev/null || true)"
		[[ "$trivy_ingested" == live-delivery:* && "$trivy_ingested" == *alpine* ]] && break
		sleep 1
	done
	printf '%s\n' "$trivy_ingested" >"$artifact_dir/trivy-ingestion-state.log"
	[[ "$trivy_ingested" == live-delivery:* && "$trivy_ingested" == *alpine* ]] || { echo "test-live-browser: Trivy report was not ingested" >&2; exit 1; }
fi

BACKEND_URL="http://127.0.0.1:$server_port" \
  setsid npm --prefix frontend run preview -- --host 127.0.0.1 --port "$frontend_port" --strictPort \
  >"$artifact_dir/frontend-preview.log" 2>&1 &
frontend_pid=$!
wait_http_ready frontend "$frontend_pid" "http://127.0.0.1:$frontend_port/auth/login"
assert_stack before-playwright

echo "test-live-browser: running Playwright live project"
set +e
(
  cd frontend
  BACKEND_URL="http://127.0.0.1:$server_port" \
  LIVE_ADMIN_PASSWORD="$admin_password" \
  LIVE_RESTRICTED_EMAIL="restricted-live@astronomer.local" \
  LIVE_RESTRICTED_PASSWORD="$restricted_password" \
  LIVE_FIXTURE_CLUSTER_ID="$cluster_id" \
  LIVE_FIXTURE_PROJECT_ID="$project_id" \
  LIVE_FIXTURE_ROLLOUT_ID="$rollout_id" \
  LIVE_FIXTURE_ROLLBACK_ROLLOUT_ID="$rollback_rollout_id" \
	LIVE_FIXTURE_TRIVY_ENABLED="$trivy_enabled" \
	LIVE_FIXTURE_TRIVY_ROLLOUT_ID="$trivy_rollout_id" \
  LIVE_FIXTURE_LOGGING_OUTPUT="$logging_output_name" \
	LIVE_FIXTURE_BACKUP_NAMESPACE="$backup_namespace" \
	LIVE_FIXTURE_BACKUP_CONFIGMAP="$backup_configmap" \
	LIVE_FIXTURE_BACKUP_MESSAGE="$backup_message" \
  LIVE_FIXTURE_DIRECT_ENDPOINT="$direct_api_endpoint" \
  LIVE_FIXTURE_DIRECT_CA_SHA256="$direct_ca_sha256" \
  LIVE_FIXTURE_DIRECT_KUBECONFIG_PATH="$direct_kubeconfig" \
  PLAYWRIGHT_PORT="$frontend_port" \
  PLAYWRIGHT_REUSE_EXISTING_SERVER=1 \
  PLAYWRIGHT_OUTPUT_DIR="$artifact_dir/test-results" \
  PLAYWRIGHT_HTML_OUTPUT_DIR="$artifact_dir/playwright-report" \
    npx playwright test --project=live
) 2>&1 | tee "$artifact_dir/playwright.log"
playwright_status=${PIPESTATUS[0]}
set -e

assert_stack after-playwright
if [[ "$playwright_status" -ne 0 ]]; then
  echo "test-live-browser: Playwright failed" >&2
  exit "$playwright_status"
fi
echo "test-live-browser: validating direct read-only credential against the member API"
[[ -f "$direct_kubeconfig" && "$(stat -c '%a' "$direct_kubeconfig")" == "600" ]] || {
	echo "test-live-browser: direct kubeconfig is missing or has unsafe permissions" >&2
	exit 1
}
kubectl --kubeconfig "$direct_kubeconfig" get --raw /version >/dev/null
kubectl --kubeconfig "$direct_kubeconfig" -n "$backup_namespace" get configmap "$backup_configmap" -o name \
	>"$artifact_dir/direct-read-validation.log"
[[ "$(kubectl --kubeconfig "$direct_kubeconfig" auth can-i get configmaps --all-namespaces)" == "yes" ]]
[[ "$(kubectl --kubeconfig "$direct_kubeconfig" auth can-i create configmaps --all-namespaces)" == "no" ]]
[[ "$(kubectl --kubeconfig "$direct_kubeconfig" auth can-i get secrets --all-namespaces)" == "no" ]]
if kubectl --kubeconfig "$direct_kubeconfig" -n "$backup_namespace" create configmap direct-write-must-fail \
	--from-literal=message=forbidden --dry-run=server >"$artifact_dir/direct-write-denial.log" 2>&1; then
	echo "test-live-browser: direct read-only credential unexpectedly performed a write" >&2
	exit 1
fi
grep -qi 'forbidden' "$artifact_dir/direct-write-denial.log" || {
	echo "test-live-browser: direct write denial was not enforced by Kubernetes RBAC" >&2
	exit 1
}
if kubectl --kubeconfig "$direct_kubeconfig" -n velero get secret live-browser-credentials \
	>"$artifact_dir/direct-secret-denial.log" 2>&1; then
	echo "test-live-browser: direct read-only credential unexpectedly read a Secret" >&2
	exit 1
fi
grep -qi 'forbidden' "$artifact_dir/direct-secret-denial.log" || {
	echo "test-live-browser: direct Secret denial was not enforced by Kubernetes RBAC" >&2
	exit 1
}
direct_token="$(kubectl --kubeconfig "$direct_kubeconfig" config view --raw -o jsonpath='{.users[0].user.token}')"
[[ "${#direct_token}" -ge 20 ]] || {
	echo "test-live-browser: direct kubeconfig token is structurally invalid" >&2
	exit 1
}
if grep -aRFl --exclude="$(basename "$direct_kubeconfig")" -- "$direct_token" "$artifact_dir" \
	>"$artifact_dir/direct-token-leak-paths.log"; then
	echo "test-live-browser: direct credential leaked into retained logs or artifacts" >&2
	exit 1
fi
unset direct_token
rm -f -- "$direct_kubeconfig"
{
	echo 'endpoint_tls=verified'
	echo 'token_ttl=15m'
	echo 'configmaps_read=yes'
	echo 'configmaps_write=no'
	echo 'secrets_read=no'
	echo 'token_logs=clean'
} >"$artifact_dir/direct-access-validation.log"
echo "test-live-browser: validating production Flux reconciliation effects"
KUBECONFIG="$flux_kubeconfig" kubectl wait --for=condition=Ready --timeout=3m \
	gitrepositories.source.toolkit.fluxcd.io -A -l app.kubernetes.io/managed-by=astronomer-agent
KUBECONFIG="$flux_kubeconfig" kubectl wait --for=condition=Ready --timeout=3m \
	kustomizations.kustomize.toolkit.fluxcd.io -A -l app.kubernetes.io/managed-by=astronomer-agent
[[ "$(KUBECONFIG="$flux_kubeconfig" kubectl -n live-delivery get configmap flux-kustomize-managed -o jsonpath='{.data.message}')" == "desired" ]] || {
	echo "test-live-browser: Flux did not reconcile the durable workload" >&2
	exit 1
}
[[ "$(KUBECONFIG="$flux_kubeconfig" kubectl get gitrepositories.source.toolkit.fluxcd.io -A -l app.kubernetes.io/managed-by=astronomer-agent --no-headers | wc -l)" -eq 1 ]]
[[ "$(KUBECONFIG="$flux_kubeconfig" kubectl get kustomizations.kustomize.toolkit.fluxcd.io -A -l app.kubernetes.io/managed-by=astronomer-agent --no-headers | wc -l)" -eq 1 ]]
echo "test-live-browser: validating durable Velero backup and restore effects"
KUBECONFIG="$flux_kubeconfig" kubectl -n velero wait --for=jsonpath='{.status.phase}'=Completed --timeout=3m \
	backup -l app.kubernetes.io/managed-by=astronomer-go
KUBECONFIG="$flux_kubeconfig" kubectl -n velero wait --for=jsonpath='{.status.phase}'=Completed --timeout=3m \
	restore -l app.kubernetes.io/managed-by=astronomer-go
[[ "$(KUBECONFIG="$flux_kubeconfig" kubectl -n velero get backup -l app.kubernetes.io/managed-by=astronomer-go --no-headers | wc -l)" -eq 1 ]]
[[ "$(KUBECONFIG="$flux_kubeconfig" kubectl -n velero get restore -l app.kubernetes.io/managed-by=astronomer-go --no-headers | wc -l)" -eq 1 ]]
[[ "$(KUBECONFIG="$flux_kubeconfig" kubectl -n "$backup_namespace" get configmap "$backup_configmap" -o jsonpath='{.data.message}')" == "$backup_message" ]] || {
	echo "test-live-browser: Velero did not restore the deleted workload data" >&2
	exit 1
}
velero_backup_name="$(KUBECONFIG="$flux_kubeconfig" kubectl -n velero get backup -l app.kubernetes.io/managed-by=astronomer-go -o jsonpath='{.items[0].metadata.name}')"
KUBECONFIG="$flux_kubeconfig" kubectl -n minio run minio-mc-verify \
	--image=minio/mc:RELEASE.2025-04-16T18-13-26Z --restart=Never --rm -i \
	--command -- sh -c "mc alias set local http://minio.minio.svc.cluster.local:9000 '$minio_user' '$minio_password' >/dev/null && mc find local/velero/live-browser --name '*$velero_backup_name.tar.gz'" \
	| tee "$artifact_dir/minio-backup-artifacts.log"
grep -Fq -- "$velero_backup_name.tar.gz" "$artifact_dir/minio-backup-artifacts.log" || {
	echo "test-live-browser: Velero backup artifact is missing from object storage" >&2
	exit 1
}
docker exec "$postgres_container" psql -X -U "$postgres_user" -d "$postgres_database" -Atc \
	"SELECT 'snapshot=' || phase || ',restore=' || (SELECT phase FROM cluster_restores WHERE snapshot_id = cluster_snapshots.id ORDER BY created_at DESC LIMIT 1) FROM cluster_snapshots WHERE cluster_id='$cluster_id' ORDER BY created_at DESC LIMIT 1" \
	| tee "$artifact_dir/velero-durable-state.log"
grep -Fxq 'snapshot=Completed,restore=Completed' "$artifact_dir/velero-durable-state.log" || {
	echo "test-live-browser: durable snapshot or restore state is not Completed" >&2
	exit 1
}
echo "test-live-browser: PASS"
