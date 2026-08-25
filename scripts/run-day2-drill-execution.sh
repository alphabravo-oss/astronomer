#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

profile_path="${LOADTEST_PROFILE:?LOADTEST_PROFILE is required}"
output="${DAY2_EXECUTION_ARTIFACT_DIR:?DAY2_EXECUTION_ARTIFACT_DIR is required}"
source_run_id="${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}"
source_commit="${GITHUB_SHA:?GITHUB_SHA is required}"
images="${LOADTEST_IMAGES:?LOADTEST_IMAGES is required}"
environment="${LOADTEST_ENVIRONMENT:?LOADTEST_ENVIRONMENT is required}"
driver_root="${DAY2_DRIVER_DIR:?DAY2_DRIVER_DIR is required}"
driver_manifest_digest="${DAY2_DRIVER_MANIFEST_SHA256:?DAY2_DRIVER_MANIFEST_SHA256 is required}"
[[ "$images" =~ ^sha256:[a-f0-9]{64}$ ]] || { echo "LOADTEST_IMAGES must be the exact release-manifest digest" >&2; exit 2; }
[[ "$driver_manifest_digest" =~ ^[a-f0-9]{64}$ ]] || { echo "DAY2_DRIVER_MANIFEST_SHA256 is invalid" >&2; exit 2; }
[[ "$environment" == "scale-certification" ]] || { echo "unexpected execution environment" >&2; exit 2; }
[[ -f "$profile_path" ]]
[[ -f "$driver_root/manifest.json" && ! -L "$driver_root/manifest.json" ]]
[[ "$(sha256sum "$driver_root/manifest.json" | cut -d' ' -f1)" == "$driver_manifest_digest" ]]

profile="$(awk -F': ' '$1 == "name" {print $2; exit}' "$profile_path")"
mapfile -t drills < <(awk '/^day2FailureDrills:/{inside=1; next} inside && /^  - /{sub(/^  - /, ""); print; next} inside && NF{exit}' "$profile_path")
(( ${#drills[@]} > 0 )) || { echo "profile has no drills" >&2; exit 2; }
install -d -m 0700 "$output"
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT

load_log="$work/profile-loadtest.log"
load_report="$work/profile-loadtest.md"
started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
go run ./scripts/loadtest -profile "$profile_path" -out "$load_report" 2>&1 | tee "$load_log"
grep -q '^VERDICT: pass$' "$load_report"

run_drill() {
  local drill="$1" log="$output/$1.log" result="$work/$1-result.json" driver="$driver_root/$1" drill_started completed digest driver_digest
  drill_started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  [[ -f "$driver" && -x "$driver" && ! -L "$driver" ]] || { echo "unsupported drill: pinned driver missing for $drill" >&2; return 2; }
  driver_digest="$(jq -er --arg drill "$drill" '
    if (keys == ["drivers", "schema_version"] and .schema_version == 1 and
        (.drivers | type == "object") and (.drivers[$drill] | type == "object") and
        (.drivers[$drill] | keys == ["sha256"]))
    then .drivers[$drill].sha256 else error("invalid driver manifest") end
  ' "$driver_root/manifest.json")"
  [[ "$driver_digest" =~ ^[a-f0-9]{64}$ && "$(sha256sum "$driver" | cut -d' ' -f1)" == "$driver_digest" ]]
  "$driver" \
    --profile "$profile" \
    --release-manifest-digest "$images" \
    --environment "$environment" \
    --result "$result" 2>&1 | tee "$log"
  completed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  digest="$(sha256sum "$log" | cut -d' ' -f1)"
  python3 - "$output/$drill.json" "$drill" "$profile" "$source_run_id" "$source_commit" \
    "$images" "$environment" "$drill_started" "$completed" "$digest" "$result" <<'PY'
import json
import pathlib
import sys
from datetime import datetime

path, drill, profile, run_id, commit, images, environment, started, completed, digest, result_path = sys.argv[1:]
result = json.loads(pathlib.Path(result_path).read_text(encoding="utf-8"))
if set(result) != {"schema_version", "drill", "status", "target", "precondition", "injection", "recovery", "cleanup"}:
    raise SystemExit(f"{drill}: driver result violates closed schema")
if result["schema_version"] != 1 or result["drill"] != drill or result["status"] != "pass":
    raise SystemExit(f"{drill}: driver did not report an exact pass")
if result["target"] != {"environment": environment, "profile": profile, "release_manifest_digest": images}:
    raise SystemExit(f"{drill}: driver target does not bind the certification estate")
contracts = {
    "precondition": {"observed_at", "metric", "value"},
    "injection": {"operation_id", "injected_at", "effect_observed_at", "metric", "value"},
    "recovery": {"recovered_at", "metric", "value"},
    "cleanup": {"completed_at", "restored"},
}
for section, keys in contracts.items():
    value = result.get(section)
    if not isinstance(value, dict) or set(value) != keys or any(v in (None, "") for v in value.values()):
        raise SystemExit(f"{drill}: {section} evidence is incomplete")
if result["cleanup"]["restored"] is not True:
    raise SystemExit(f"{drill}: target was not restored")
times = [result["precondition"]["observed_at"], result["injection"]["injected_at"],
         result["injection"]["effect_observed_at"], result["recovery"]["recovered_at"],
         result["cleanup"]["completed_at"]]
parsed = [datetime.fromisoformat(value.replace("Z", "+00:00")) for value in times]
if any(value.tzinfo is None for value in parsed) or parsed != sorted(parsed):
    raise SystemExit(f"{drill}: driver evidence timeline is invalid")
document = {
    "schema_version": "astronomer-day2-drill-evidence-v2",
    "drill": drill, "status": "pass", "commit": commit, "profile": profile,
    "source_workflow": ".github/workflows/day2-drill-execution.yaml",
    "source_run_id": run_id, "source_commit": commit, "source_conclusion": "success",
    "source_event": "workflow_dispatch", "images": images, "environment": environment,
    "started_at": started, "completed_at": completed,
    "execution_evidence_path": f"{drill}.log", "execution_evidence_sha256": digest,
    "driver_result": result,
}
pathlib.Path(path).write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

for drill in "${drills[@]}"; do
  run_drill "$drill"
done

printf 'day-2 execution passed: profile=%s drills=%d started=%s completed=%s\n' \
  "$profile" "${#drills[@]}" "$started" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
