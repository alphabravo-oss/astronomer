#!/usr/bin/env python3
"""Run bounded failure/restore drills against an explicitly disposable namespace.

The manifest is data, not a shell program. Only the small action allowlist below
can execute. Every mutating target must carry the qualification run label, and
the namespace must carry both the run label and the disposable=true label.
"""

from __future__ import annotations

import argparse
import copy
import datetime as dt
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable


SCHEMA = "astronomer-delivery-resilience-drill/v1"
REPORT_SCHEMA = "astronomer-delivery-resilience-report/v1"
OWNER_KEY = "delivery.astronomer.io/qualification-run"
DISPOSABLE_KEY = "delivery.astronomer.io/disposable"
RUN_RE = re.compile(r"^[a-z0-9](?:[-a-z0-9]{0,38}[a-z0-9])?$")
NAME_RE = re.compile(r"^[a-z0-9](?:[-a-z0-9.]{0,251}[a-z0-9])?$")
SELECTOR_RE = re.compile(r"^[A-Za-z0-9./_-]+=[-A-Za-z0-9._]+(?:,[A-Za-z0-9./_-]+=[-A-Za-z0-9._]+)*$")
MUTABLE_KINDS = {"deployment", "statefulset"}
ASSERT_KINDS = {"deployment", "statefulset", "pod", "job", "cronjob", "service", "networkpolicy", "configmap"}


class DrillError(RuntimeError):
    pass


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def require_keys(value: dict[str, Any], required: set[str], allowed: set[str], where: str) -> None:
    missing = required - value.keys()
    unknown = value.keys() - allowed
    if missing:
        raise DrillError(f"{where} is missing: {', '.join(sorted(missing))}")
    if unknown:
        raise DrillError(f"{where} has unknown fields: {', '.join(sorted(unknown))}")


def bounded_name(value: Any, where: str, maximum: int = 253) -> str:
    if not isinstance(value, str) or len(value) > maximum or not NAME_RE.fullmatch(value):
        raise DrillError(f"{where} must be a bounded DNS name")
    return value


def bounded_timeout(value: Any, where: str) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < 1 or value > 1800:
        raise DrillError(f"{where} must be an integer in 1..1800")
    return value


def validate_step(step: Any, where: str) -> dict[str, Any]:
    if not isinstance(step, dict):
        raise DrillError(f"{where} must be an object")
    action = step.get("action")
    common = {"action", "timeout_seconds"}
    if action in {"restart_workload", "scale_workload", "wait_rollout"}:
        allowed = common | {"kind", "name"}
        if action == "scale_workload":
            allowed.add("replicas")
        require_keys(step, {"action", "kind", "name"}, allowed, where)
        kind = step["kind"]
        if kind not in MUTABLE_KINDS:
            raise DrillError(f"{where}.kind must be deployment or statefulset")
        bounded_name(step["name"], f"{where}.name")
        if action == "scale_workload" and (not isinstance(step.get("replicas"), int) or isinstance(step.get("replicas"), bool) or not 0 <= step["replicas"] <= 100):
            raise DrillError(f"{where}.replicas must be an integer in 0..100")
    elif action == "delete_pod":
        require_keys(step, {"action", "selector"}, common | {"selector"}, where)
        if not isinstance(step["selector"], str) or len(step["selector"]) > 512 or not SELECTOR_RE.fullmatch(step["selector"]):
            raise DrillError(f"{where}.selector must be a bounded equality-label selector")
    elif action == "network_partition":
        require_keys(step, {"action", "duration_seconds"}, common | {"duration_seconds"}, where)
        duration = step["duration_seconds"]
        if not isinstance(duration, int) or isinstance(duration, bool) or duration < 1 or duration > 300:
            raise DrillError(f"{where}.duration_seconds must be an integer in 1..300")
    elif action == "run_cronjob":
        require_keys(step, {"action", "name"}, common | {"name"}, where)
        bounded_name(step["name"], f"{where}.name")
    elif action == "assert_resource":
        require_keys(step, {"action", "kind", "name", "json_pointer", "expected"}, common | {"kind", "name", "json_pointer", "expected"}, where)
        if step["kind"] not in ASSERT_KINDS:
            raise DrillError(f"{where}.kind is not allowlisted")
        bounded_name(step["name"], f"{where}.name")
        if not isinstance(step["json_pointer"], str) or len(step["json_pointer"]) > 512 or not step["json_pointer"].startswith("/"):
            raise DrillError(f"{where}.json_pointer must be a bounded JSON pointer")
        if not isinstance(step["expected"], (str, int, bool)):
            raise DrillError(f"{where}.expected must be a scalar")
    else:
        raise DrillError(f"{where}.action is unsupported")
    if "timeout_seconds" in step:
        bounded_timeout(step["timeout_seconds"], f"{where}.timeout_seconds")
    return step


def validate_manifest(document: Any) -> dict[str, Any]:
    if not isinstance(document, dict):
        raise DrillError("manifest must be an object")
    require_keys(document, {"schema_version", "run_id", "context", "namespace", "scenarios"},
                 {"schema_version", "run_id", "context", "namespace", "scenarios"}, "manifest")
    if document["schema_version"] != SCHEMA:
        raise DrillError(f"schema_version must be {SCHEMA}")
    run_id = document["run_id"]
    if not isinstance(run_id, str) or not RUN_RE.fullmatch(run_id):
        raise DrillError("run_id must be a bounded lowercase DNS label")
    context = bounded_name(document["context"], "context")
    namespace = bounded_name(document["namespace"], "namespace", 63)
    if namespace != f"astronomer-qualification-{run_id}":
        raise DrillError("namespace must equal astronomer-qualification-<run_id>")
    if any(marker in context.lower() for marker in ("prod", "production")):
        raise DrillError("contexts containing prod/production are never accepted by this disposable-fixture harness")
    scenarios = document["scenarios"]
    if not isinstance(scenarios, list) or not 1 <= len(scenarios) <= 100:
        raise DrillError("scenarios must contain 1..100 entries")
    seen: set[str] = set()
    for scenario_index, scenario in enumerate(scenarios):
        where = f"scenarios[{scenario_index}]"
        if not isinstance(scenario, dict):
            raise DrillError(f"{where} must be an object")
        require_keys(scenario, {"id", "steps"}, {"id", "steps"}, where)
        scenario_id = bounded_name(scenario["id"], f"{where}.id", 40)
        if scenario_id in seen:
            raise DrillError(f"duplicate scenario id {scenario_id}")
        seen.add(scenario_id)
        if not isinstance(scenario["steps"], list) or not 1 <= len(scenario["steps"]) <= 100:
            raise DrillError(f"{where}.steps must contain 1..100 entries")
        for step_index, step in enumerate(scenario["steps"]):
            validate_step(step, f"{where}.steps[{step_index}]")
    return document


def canonical_digest(document: dict[str, Any]) -> str:
    raw = json.dumps(document, sort_keys=True, separators=(",", ":")).encode()
    return "sha256:" + hashlib.sha256(raw).hexdigest()


@dataclass
class Kubectl:
    context: str
    namespace: str

    def run(self, arguments: list[str], *, stdin: dict[str, Any] | None = None, timeout: int = 60) -> str:
        command = ["kubectl", "--context", self.context, "--namespace", self.namespace, *arguments]
        payload = None if stdin is None else json.dumps(stdin, sort_keys=True).encode()
        try:
            completed = subprocess.run(command, input=payload, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                       check=False, timeout=timeout)
        except (OSError, subprocess.TimeoutExpired) as error:
            raise DrillError(f"kubectl invocation failed: {error}") from error
        if completed.returncode != 0:
            # Kubernetes error text can echo sensitive values. Evidence retains
            # only a bounded classification; raw stderr stays in the terminal.
            sys.stderr.buffer.write(completed.stderr[:4096])
            raise DrillError(f"kubectl exited {completed.returncode}")
        return completed.stdout.decode(errors="strict")

    def get(self, kind: str, name: str) -> dict[str, Any]:
        raw = self.run(["get", kind, name, "-o", "json"])
        value = json.loads(raw)
        if not isinstance(value, dict):
            raise DrillError(f"kubectl returned invalid {kind}/{name}")
        return value


def labels(resource: dict[str, Any]) -> dict[str, str]:
    value = resource.get("metadata", {}).get("labels", {})
    return value if isinstance(value, dict) else {}


def require_owned(resource: dict[str, Any], run_id: str, description: str) -> None:
    if labels(resource).get(OWNER_KEY) != run_id or labels(resource).get(DISPOSABLE_KEY) != "true":
        raise DrillError(f"REFUSE {description}: missing exact disposable qualification ownership labels")


def json_pointer(document: Any, pointer: str) -> Any:
    current = document
    for encoded in pointer[1:].split("/"):
        token = encoded.replace("~1", "/").replace("~0", "~")
        if isinstance(current, list):
            if not token.isdigit() or int(token) >= len(current):
                raise DrillError("JSON pointer does not exist")
            current = current[int(token)]
        elif isinstance(current, dict) and token in current:
            current = current[token]
        else:
            raise DrillError("JSON pointer does not exist")
    return current


class Runner:
    def __init__(self, manifest: dict[str, Any], evidence_path: Path):
        self.manifest = manifest
        self.run_id = manifest["run_id"]
        self.kubectl = Kubectl(manifest["context"], manifest["namespace"])
        self.evidence_path = evidence_path
        self.cleanup: list[tuple[str, Callable[[], None]]] = []
        self.report: dict[str, Any] = {
            "schema_version": REPORT_SCHEMA, "run_id": self.run_id,
            "manifest_digest": canonical_digest(manifest), "context": manifest["context"],
            "namespace": manifest["namespace"], "status": "running", "started_at": utc_now(),
            "ownership_verified": False, "scenarios": [], "cleanup": [], "errors": [],
            "release_eligible": False,
        }

    def checkpoint(self) -> None:
        write_json_atomic(self.evidence_path, self.report)

    def verify_namespace(self) -> None:
        namespace = self.kubectl.get("namespace", self.manifest["namespace"])
        require_owned(namespace, self.run_id, f"namespace/{self.manifest['namespace']}")
        self.report["ownership_verified"] = True

    def owned(self, kind: str, name: str) -> dict[str, Any]:
        resource = self.kubectl.get(kind, name)
        require_owned(resource, self.run_id, f"{kind}/{name}")
        return resource

    def run(self) -> dict[str, Any]:
        try:
            self.verify_namespace()
            self.checkpoint()
            for scenario in self.manifest["scenarios"]:
                scenario_report = {"id": scenario["id"], "status": "running", "started_at": utc_now(), "steps": []}
                self.report["scenarios"].append(scenario_report)
                self.checkpoint()
                for index, step in enumerate(scenario["steps"]):
                    started = time.monotonic()
                    step_report = {"index": index, "action": step["action"], "status": "running", "started_at": utc_now()}
                    scenario_report["steps"].append(step_report)
                    self.checkpoint()
                    self.execute_step(scenario["id"], index, step)
                    step_report.update(status="passed", completed_at=utc_now(), duration_ms=int((time.monotonic() - started) * 1000))
                    self.checkpoint()
                scenario_report.update(status="passed", completed_at=utc_now())
            self.report["status"] = "passed"
        except Exception as error:  # cleanup must run for validation/runtime failures alike
            self.report["status"] = "failed"
            self.report["errors"].append(type(error).__name__)
            raise
        finally:
            self.run_cleanup()
            self.report["completed_at"] = utc_now()
            self.checkpoint()
        return self.report

    def execute_step(self, scenario_id: str, index: int, step: dict[str, Any]) -> None:
        timeout = step.get("timeout_seconds", 600)
        action = step["action"]
        if action == "restart_workload":
            self.owned(step["kind"], step["name"])
            self.kubectl.run(["rollout", "restart", f"{step['kind']}/{step['name']}", "--field-manager=astronomer-qualification"], timeout=timeout)
            self.kubectl.run(["rollout", "status", f"{step['kind']}/{step['name']}", f"--timeout={timeout}s"], timeout=timeout + 5)
        elif action == "wait_rollout":
            self.owned(step["kind"], step["name"])
            self.kubectl.run(["rollout", "status", f"{step['kind']}/{step['name']}", f"--timeout={timeout}s"], timeout=timeout + 5)
        elif action == "scale_workload":
            resource = self.owned(step["kind"], step["name"])
            original = resource.get("spec", {}).get("replicas")
            if not isinstance(original, int) or isinstance(original, bool) or not 0 <= original <= 100:
                raise DrillError("owned workload has an invalid original replica count")
            self.cleanup.append((f"restore {step['kind']}/{step['name']} replicas={original}",
                                 lambda s=step, r=original: self.kubectl.run(["scale", f"{s['kind']}/{s['name']}", f"--replicas={r}"], timeout=timeout)))
            self.kubectl.run(["scale", f"{step['kind']}/{step['name']}", f"--replicas={step['replicas']}"], timeout=timeout)
        elif action == "delete_pod":
            raw = self.kubectl.run(["get", "pods", "-l", step["selector"], "-o", "json"], timeout=timeout)
            items = json.loads(raw).get("items", [])
            if not isinstance(items, list) or not items:
                raise DrillError("delete_pod selector matched no pod")
            items.sort(key=lambda item: item.get("metadata", {}).get("name", ""))
            pod = items[0]
            name = bounded_name(pod.get("metadata", {}).get("name"), "selected pod name", 63)
            require_owned(pod, self.run_id, f"pod/{name}")
            self.kubectl.run(["delete", "pod", name, "--wait=false"], timeout=timeout)
        elif action == "network_partition":
            name = bounded_name(f"qualification-deny-{scenario_id}-{index}", "network policy name", 63)
            policy = {"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
                      "metadata": {"name": name, "namespace": self.manifest["namespace"],
                                   "labels": {OWNER_KEY: self.run_id, DISPOSABLE_KEY: "true"}},
                      "spec": {"podSelector": {"matchLabels": {OWNER_KEY: self.run_id, DISPOSABLE_KEY: "true"}},
                               "policyTypes": ["Ingress", "Egress"]}}
            self.kubectl.run(["apply", "--server-side", "--field-manager=astronomer-qualification", "-f", "-"], stdin=policy, timeout=timeout)
            self.cleanup.append((f"delete networkpolicy/{name}", lambda n=name: self.kubectl.run(["delete", "networkpolicy", n, "--ignore-not-found=true"], timeout=timeout)))
            time.sleep(step["duration_seconds"])
            _, callback = self.cleanup.pop()
            callback()
        elif action == "run_cronjob":
            cronjob = self.owned("cronjob", step["name"])
            job_name = bounded_name(f"q-{self.run_id[:20]}-{scenario_id[:20]}-{index}", "generated job name", 63)
            template = cronjob.get("spec", {}).get("jobTemplate", {}).get("spec")
            if not isinstance(template, dict):
                raise DrillError("owned CronJob has no job template")
            job = {"apiVersion": "batch/v1", "kind": "Job",
                   "metadata": {"name": job_name, "namespace": self.manifest["namespace"],
                                "labels": {OWNER_KEY: self.run_id, DISPOSABLE_KEY: "true"}},
                   "spec": copy.deepcopy(template)}
            self.kubectl.run(["create", "-f", "-"], stdin=job, timeout=timeout)
            self.cleanup.append((f"delete job/{job_name}", lambda n=job_name: self.kubectl.run(["delete", "job", n, "--ignore-not-found=true"], timeout=timeout)))
            self.kubectl.run(["wait", "--for=condition=complete", f"job/{job_name}", f"--timeout={timeout}s"], timeout=timeout + 5)
        elif action == "assert_resource":
            resource = self.owned(step["kind"], step["name"])
            if json_pointer(resource, step["json_pointer"]) != step["expected"]:
                raise DrillError("resource assertion failed")
        else:  # validate_step makes this unreachable
            raise DrillError(f"unsupported action {action}")

    def run_cleanup(self) -> None:
        while self.cleanup:
            description, callback = self.cleanup.pop()
            outcome = {"action": description, "status": "running", "started_at": utc_now()}
            self.report["cleanup"].append(outcome)
            try:
                callback()
                outcome["status"] = "passed"
            except Exception:
                outcome["status"] = "failed"
                self.report["status"] = "failed"
                self.report["errors"].append("CleanupError")
            outcome["completed_at"] = utc_now()


def write_json_atomic(path: Path, value: dict[str, Any]) -> None:
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise DrillError("evidence path must be a regular file, not a symlink")
    if not path.parent.is_dir():
        raise DrillError("evidence directory must already exist")
    descriptor, temporary = tempfile.mkstemp(prefix=".resilience-", suffix=".tmp", dir=path.parent)
    try:
        os.fchmod(descriptor, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            json.dump(value, handle, sort_keys=True, indent=2)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--evidence", type=Path)
    parser.add_argument("--confirm")
    parser.add_argument("--validate-only", action="store_true")
    return parser.parse_args()


def main() -> int:
    arguments = parse_args()
    try:
        if arguments.manifest.is_symlink() or not arguments.manifest.is_file():
            raise DrillError("manifest must be a regular file, not a symlink")
        document = validate_manifest(json.loads(arguments.manifest.read_text(encoding="utf-8")))
        if arguments.validate_only:
            print(f"delivery-resilience-drill: valid manifest {canonical_digest(document)}")
            return 0
        if arguments.evidence is None:
            raise DrillError("--evidence is required unless --validate-only is used")
        confirmation = f"delete-only-owned:{document['context']}:{document['namespace']}:{document['run_id']}"
        if arguments.confirm != confirmation:
            raise DrillError(f"--confirm must exactly equal {confirmation}")
        report = Runner(document, arguments.evidence).run()
        print(f"delivery-resilience-drill: {report['status']} evidence={arguments.evidence}")
        return 0 if report["status"] == "passed" else 1
    except (DrillError, json.JSONDecodeError, OSError) as error:
        print(f"delivery-resilience-drill: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
