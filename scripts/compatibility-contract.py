#!/usr/bin/env python3
"""Validate and document Astronomer's release compatibility contract.

The canonical ``compatibility.yaml`` is deliberately JSON-formatted YAML. JSON
is a strict subset of YAML, which keeps this release gate dependency-free and
prevents a CI runner's YAML-library version from changing parsed values.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
DEFAULT_CONTRACT = ROOT / "deploy/release/compatibility.yaml"
DEFAULT_SCHEMA = ROOT / "deploy/release/compatibility.schema.json"
DEFAULT_DOC = ROOT / "docs/architecture/compatibility.md"
DEFAULT_GO = ROOT / "pkg/protocol/compatibility_generated.go"
DEFAULT_HELM = ROOT / "deploy/chart/files/release-compatibility.json"


class ContractError(ValueError):
    """A deterministic, user-actionable contract validation error."""


def load_json(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ContractError(f"missing file: {path}") from exc
    except json.JSONDecodeError as exc:
        raise ContractError(f"{path}:{exc.lineno}:{exc.colno}: {exc.msg}") from exc


def json_type_matches(value: Any, expected: str) -> bool:
    if expected == "object":
        return isinstance(value, dict)
    if expected == "array":
        return isinstance(value, list)
    if expected == "string":
        return isinstance(value, str)
    if expected == "integer":
        return isinstance(value, int) and not isinstance(value, bool)
    if expected == "number":
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    if expected == "boolean":
        return isinstance(value, bool)
    if expected == "null":
        return value is None
    raise ContractError(f"schema uses unsupported type {expected!r}")


def resolve_ref(root_schema: dict[str, Any], ref: str) -> dict[str, Any]:
    if not ref.startswith("#/"):
        raise ContractError(f"schema uses unsupported external $ref {ref!r}")
    node: Any = root_schema
    for raw_part in ref[2:].split("/"):
        part = raw_part.replace("~1", "/").replace("~0", "~")
        if not isinstance(node, dict) or part not in node:
            raise ContractError(f"schema contains unresolved $ref {ref!r}")
        node = node[part]
    if not isinstance(node, dict):
        raise ContractError(f"schema $ref {ref!r} does not resolve to an object")
    return node


def validate_schema(value: Any, schema: dict[str, Any], root_schema: dict[str, Any], path: str = "$") -> None:
    """Validate the deliberately small JSON-Schema vocabulary used here."""
    if "$ref" in schema:
        validate_schema(value, resolve_ref(root_schema, schema["$ref"]), root_schema, path)
        return

    if "const" in schema and value != schema["const"]:
        raise ContractError(f"{path}: expected constant {schema['const']!r}, got {value!r}")
    if "enum" in schema and value not in schema["enum"]:
        raise ContractError(f"{path}: {value!r} is not one of {schema['enum']!r}")

    expected_type = schema.get("type")
    if expected_type and not json_type_matches(value, expected_type):
        raise ContractError(f"{path}: expected {expected_type}, got {type(value).__name__}")

    if isinstance(value, dict):
        required = schema.get("required", [])
        missing = sorted(set(required) - set(value))
        if missing:
            raise ContractError(f"{path}: missing required keys: {', '.join(missing)}")
        properties = schema.get("properties", {})
        if schema.get("additionalProperties") is False:
            extra = sorted(set(value) - set(properties))
            if extra:
                raise ContractError(f"{path}: unexpected keys: {', '.join(extra)}")
        for key, child in value.items():
            if key in properties:
                validate_schema(child, properties[key], root_schema, f"{path}.{key}")

    if isinstance(value, list):
        if len(value) < schema.get("minItems", 0):
            raise ContractError(f"{path}: expected at least {schema['minItems']} items")
        if schema.get("uniqueItems"):
            encoded = [json.dumps(item, sort_keys=True, separators=(",", ":")) for item in value]
            if len(encoded) != len(set(encoded)):
                raise ContractError(f"{path}: duplicate items are not allowed")
        item_schema = schema.get("items")
        if item_schema:
            for index, child in enumerate(value):
                validate_schema(child, item_schema, root_schema, f"{path}[{index}]")

    if isinstance(value, str):
        if len(value) < schema.get("minLength", 0):
            raise ContractError(f"{path}: string is shorter than {schema['minLength']}")
        pattern = schema.get("pattern")
        if pattern and re.search(pattern, value) is None:
            raise ContractError(f"{path}: {value!r} does not match {pattern!r}")

    if isinstance(value, (int, float)) and not isinstance(value, bool):
        if "minimum" in schema and value < schema["minimum"]:
            raise ContractError(f"{path}: {value} is less than {schema['minimum']}")
        if "maximum" in schema and value > schema["maximum"]:
            raise ContractError(f"{path}: {value} is greater than {schema['maximum']}")
        if "exclusiveMinimum" in schema and value <= schema["exclusiveMinimum"]:
            raise ContractError(f"{path}: {value} must be greater than {schema['exclusiveMinimum']}")


def minor_number(value: str) -> int:
    major, minor = value.split(".", 1)
    return int(major) * 1000 + int(minor)


def expand_minor_ranges(ranges: list[dict[str, str]], label: str) -> list[str]:
    occupied: dict[int, int] = {}
    expanded: list[str] = []
    for index, item in enumerate(ranges):
        lower = minor_number(item["minimum_minor"])
        upper = minor_number(item["maximum_minor"])
        if lower > upper:
            raise ContractError(f"{label}[{index}]: empty range (minimum exceeds maximum)")
        if lower // 1000 != upper // 1000:
            raise ContractError(f"{label}[{index}]: a range cannot cross major versions")
        for numeric in range(lower, upper + 1):
            if numeric in occupied:
                raise ContractError(
                    f"{label}[{index}]: overlaps range {occupied[numeric]} at "
                    f"{numeric // 1000}.{numeric % 1000}"
                )
            occupied[numeric] = index
            expanded.append(f"{numeric // 1000}.{numeric % 1000}")
    return sorted(expanded, key=minor_number)


def validate_integer_ranges(ranges: list[dict[str, int]], label: str) -> None:
    occupied: dict[int, int] = {}
    for index, item in enumerate(ranges):
        lower, upper = item["minimum"], item["maximum"]
        if lower > upper:
            raise ContractError(f"{label}[{index}]: empty range (minimum exceeds maximum)")
        for value in range(lower, upper + 1):
            if value in occupied:
                raise ContractError(f"{label}[{index}]: overlaps range {occupied[value]} at {value}")
            occupied[value] = index


def validate_semantics(contract: dict[str, Any], root: Path) -> None:
    kubernetes = contract["kubernetes"]
    expanded = expand_minor_ranges(kubernetes["supported_ranges"], "kubernetes.supported_ranges")
    if expanded != kubernetes["advertised_minors"]:
        raise ContractError(
            "kubernetes.advertised_minors must exactly equal the expanded supported_ranges; "
            f"expected {expanded!r}"
        )

    qualifications: dict[str, dict[str, Any]] = {}
    for index, item in enumerate(kubernetes["qualification"]):
        minor = item["minor"]
        if minor in qualifications:
            raise ContractError(f"kubernetes.qualification[{index}]: duplicate minor {minor}")
        runner = root / item["runner"]
        if not runner.is_file():
            raise ContractError(f"kubernetes.qualification[{index}]: runner does not exist: {item['runner']}")
        command_words = item["command"].split()
        invoked_by_shell = (
            len(command_words) >= 2
            and command_words[0] in {"bash", "sh"}
            and command_words[1].removeprefix("./") == item["runner"]
        )
        if not runner.stat().st_mode & 0o111 and not invoked_by_shell:
            raise ContractError(
                f"kubernetes.qualification[{index}]: non-executable runner must be invoked explicitly with bash/sh: "
                f"{item['runner']}"
            )
        if not re.search(rf"(?:^|[^0-9])v?{re.escape(minor)}(?:\.|[^0-9]|$)", item["command"]):
            raise ContractError(
                f"kubernetes.qualification[{index}]: command does not select Kubernetes {minor}"
            )
        qualifications[minor] = item
    missing = sorted(set(expanded) - set(qualifications), key=minor_number)
    extra = sorted(set(qualifications) - set(expanded), key=minor_number)
    if missing or extra:
        raise ContractError(f"Kubernetes qualification mismatch: missing={missing}, extra={extra}")

    validate_integer_ranges(contract["agent_protocol"]["supported_ranges"], "agent_protocol.supported_ranges")

    agent = contract["agent"]
    versions = [
        tuple(int(part) for part in agent[key].removeprefix("v").split("."))
        for key in (
            "minimum_compatible_version",
            "minimum_supported_version",
            "maximum_supported_version_exclusive",
        )
    ]
    if not versions[0] <= versions[1] < versions[2]:
        raise ContractError(
            "agent versions must satisfy minimum_compatible <= minimum_supported < maximum_exclusive"
        )
    validate_integer_ranges([agent["tunnel_protocol"]], "agent.tunnel_protocol")
    validate_integer_ranges([agent["heartbeat_schema"]], "agent.heartbeat_schema")

    postgres = contract["postgresql"]
    minimum_schema = postgres["minimum_upgrade_schema"]
    target_schema = postgres["target_schema"]
    reversible_schema = postgres["reversible_through_schema"]
    if minimum_schema > target_schema:
        raise ContractError("postgresql.minimum_upgrade_schema cannot exceed target_schema")
    if reversible_schema > target_schema:
        raise ContractError("postgresql.reversible_through_schema cannot exceed target_schema")
    if contract["release_line"] not in postgres["supported_upgrade_release_lines"]:
        raise ContractError("postgresql.supported_upgrade_release_lines must include the current release_line")

    # The chart renders the database preflight from the generated compatibility
    # view. Keep the public values contract pinned to the same numbers so a
    # chart consumer cannot accidentally advertise a different upgrade range.
    values_text = (root / "deploy/chart/values.yaml").read_text(encoding="utf-8")
    minimum_matches = re.findall(r"^\s+minimumUpgradeVersion:\s*(\d+)\s*$", values_text, re.MULTILINE)
    target_matches = re.findall(r"^\s+targetVersion:\s*(\d+)\s*$", values_text, re.MULTILINE)
    if minimum_matches != [str(minimum_schema)] or target_matches != [str(target_schema)]:
        raise ContractError(
            "deploy/chart/values.yaml databaseSchema range does not match postgresql compatibility: "
            f"minimum={minimum_matches}, target={target_matches}"
        )

    values_schema = load_json(root / "deploy/chart/values.schema.json")
    schema_ranges: list[tuple[int, int]] = []

    def collect_schema_ranges(node: Any) -> None:
        if isinstance(node, dict):
            properties = node.get("properties")
            if isinstance(properties, dict) and {"minimumUpgradeVersion", "targetVersion"} <= set(properties):
                minimum_const = properties["minimumUpgradeVersion"].get("const")
                target_const = properties["targetVersion"].get("const")
                if isinstance(minimum_const, int) and isinstance(target_const, int):
                    schema_ranges.append((minimum_const, target_const))
            for value in node.values():
                collect_schema_ranges(value)
        elif isinstance(node, list):
            for value in node:
                collect_schema_ranges(value)

    collect_schema_ranges(values_schema)
    if not schema_ranges or any(item != (minimum_schema, target_schema) for item in schema_ranges):
        raise ContractError(
            "deploy/chart/values.schema.json databaseSchema const values do not match postgresql compatibility: "
            f"{schema_ranges}"
        )

    components = contract["flux"]["components"]
    names = [item["name"] for item in components]
    required = {"source-controller", "kustomize-controller", "helm-controller"}
    if set(names) != required or len(names) != len(required):
        raise ContractError(f"flux.components must be exactly {sorted(required)!r}, got {names!r}")
    excluded = set(contract["flux"]["excluded_components"])
    if excluded & set(names):
        raise ContractError(f"Flux components cannot be included and excluded: {sorted(excluded & set(names))}")

    source_types = [item["type"] for item in contract["sources"]]
    if len(source_types) != len(set(source_types)):
        raise ContractError("sources contains duplicate source types")

    limits = contract["limits"]
    if limits["status_snapshot_bytes"] > limits["assignment_snapshot_bytes"]:
        raise ContractError("limits.status_snapshot_bytes cannot exceed assignment_snapshot_bytes")
    if limits["helm_chart_bytes"] > limits["source_artifact_bytes"]:
        raise ContractError("limits.helm_chart_bytes cannot exceed source_artifact_bytes")


def format_bytes(value: int) -> str:
    for suffix, divisor in (("GiB", 1 << 30), ("MiB", 1 << 20), ("KiB", 1 << 10)):
        if value >= divisor and value % divisor == 0:
            return f"{value // divisor} {suffix}"
    return f"{value} bytes"


def generate_doc(contract: dict[str, Any]) -> str:
    kube = contract["kubernetes"]
    flux = contract["flux"]
    protocol = contract["agent_protocol"]
    agent = contract["agent"]
    postgres = contract["postgresql"]
    browsers = contract["browsers"]
    limits = contract["limits"]
    slos = contract["slos"]

    lines = [
        "# Release compatibility",
        "",
        "<!-- Generated by scripts/compatibility-contract.py; do not edit by hand. -->",
        "",
        f"This table describes the `{contract['release_line']}` release line. Install mode is "
        f"**{contract['install_mode'].replace('_', ' ')}**. Supported upgrades begin at schema "
        f"{postgres['minimum_upgrade_schema']} and migrate to schema {postgres['target_schema']}.",
        "",
        "## Runtime matrix",
        "",
        "| Surface | Supported contract |",
        "| --- | --- |",
        f"| Kubernetes | {', '.join('v' + value for value in kube['advertised_minors'])}; latest patch of each minor |",
        f"| Flux distribution | `{flux['distribution_version']}` |",
        f"| Agent application | supported `{agent['minimum_supported_version']}` to "
        f"`<{agent['maximum_supported_version_exclusive']}`; deprecated-but-compatible from "
        f"`{agent['minimum_compatible_version']}` |",
        f"| Tunnel protocol | versions {agent['tunnel_protocol']['minimum']}-{agent['tunnel_protocol']['maximum']} |",
        f"| Heartbeat schema | versions {agent['heartbeat_schema']['minimum']}-{agent['heartbeat_schema']['maximum']} |",
        f"| Agent protocol | `{protocol['name']}` versions "
        + ", ".join(f"{item['minimum']}-{item['maximum']}" for item in protocol["supported_ranges"])
        + " |",
        f"| PostgreSQL | {', '.join(str(value) for value in postgres['supported_majors'])}; TLS required in production; "
        f"schemas {postgres['minimum_upgrade_schema']}-{postgres['target_schema']} supported for upgrade |",
        "| Browsers | Chrome " + str(browsers["minimum_versions"]["chrome"]) +
        "+, Edge " + str(browsers["minimum_versions"]["edge"]) +
        "+, Firefox " + str(browsers["minimum_versions"]["firefox"]) +
        "+, Safari " + browsers["minimum_versions"]["safari"] +
        "+; current and previous stable at release |",
        "",
        "Unknown future agent majors are rejected before authentication and work dispatch. "
        f"The release supports {agent['support_window_release_lines']} adjacent release lines; "
        "deprecated agents remain connected only inside the declared compatibility window.",
        "",
        "CONNECT admission also requires: "
        + ", ".join(f"`{item}`" for item in protocol["required_capabilities"])
        + ".",
        "",
        "## Flux components",
        "",
        "Images are resolved to immutable digests by the release distribution; tags below identify the qualified upstream component release.",
        "",
        "| Component | Upstream image | APIs consumed by Astronomer |",
        "| --- | --- | --- |",
    ]
    for component in flux["components"]:
        apis = "<br>".join(f"`{api}`" for api in component["apis"])
        lines.append(
            f"| `{component['name']}` | `{component['repository']}@{component['digest']}` "
            f"(upstream `{component['tag']}`) | {apis} |"
        )
    lines.extend(
        [
            "",
            "Excluded components: " + ", ".join(f"`{item}`" for item in flux["excluded_components"]) + ".",
            "",
            "## Source types",
            "",
            "| Type | Immutable identity | Authentication modes |",
            "| --- | --- | --- |",
        ]
    )
    for source in contract["sources"]:
        lines.append(
            f"| `{source['type']}` | `{source['immutable_identity']}` | "
            + ", ".join(f"`{item}`" for item in source["authentication"])
            + " |"
        )
    lines.extend(
        [
            "",
            "## Hard limits",
            "",
            "| Limit | Value |",
            "| --- | ---: |",
            f"| Assignment snapshot | {format_bytes(limits['assignment_snapshot_bytes'])} |",
            f"| Status snapshot | {format_bytes(limits['status_snapshot_bytes'])} |",
            f"| Events in one status snapshot | {limits['status_events_per_snapshot']} |",
            f"| Source artifact | {format_bytes(limits['source_artifact_bytes'])} |",
            f"| Helm chart | {format_bytes(limits['helm_chart_bytes'])} |",
            f"| Rendered objects per deployment | {limits['rendered_objects_per_deployment']} |",
            f"| Concurrent assignments per cluster | {limits['concurrent_assignments_per_cluster']} |",
            "",
            "## Service objectives",
            "",
            "| Objective | Target |",
            "| --- | ---: |",
            f"| Management API monthly availability | {slos['management_api_monthly_availability_percent']}% |",
            f"| Connected assignment acknowledgement p99 | {slos['connected_assignment_ack_seconds_p99']} seconds |",
            f"| Flux transition visible centrally p99 | {slos['flux_transition_visibility_seconds_p99']} seconds |",
            f"| Rollout scheduler decision p99 | {slos['rollout_scheduler_seconds_p99']} seconds |",
            f"| Management database RPO | {slos['management_database_rpo_minutes']} minutes |",
            f"| Management plane RTO | {slos['management_plane_rto_minutes']} minutes |",
            "| Control-plane outage behavior | Last accepted generation continues reconciling locally |",
            "",
            "## Kubernetes qualification",
            "",
            "Every advertised minor has an explicit executable live qualification command:",
            "",
        ]
    )
    for item in kube["qualification"]:
        lines.append(f"- Kubernetes {item['minor']}: `{item['command']}`")
    lines.extend(
        [
            "",
            "Validate this contract and generated page with:",
            "",
            "```bash",
            "./scripts/compatibility-contract.py check",
            "```",
            "",
        ]
    )
    return "\n".join(lines)


def generate_go(contract: dict[str, Any]) -> str:
    agent = contract["agent"]
    protocol = contract["agent_protocol"]
    protocol_range = protocol["supported_ranges"][0]
    capabilities = "\n".join(f'\t"{item}",' for item in protocol["required_capabilities"])
    source = f'''// Code generated by scripts/compatibility-contract.py; DO NOT EDIT.

package protocol

import "fmt"

const (
\tTunnelProtocolVersion = {agent["tunnel_protocol"]["maximum"]}
\tMinimumTunnelProtocolVersion = {agent["tunnel_protocol"]["minimum"]}
\tMaximumTunnelProtocolVersion = {agent["tunnel_protocol"]["maximum"]}
\tHeartbeatSchemaVersion = {agent["heartbeat_schema"]["maximum"]}
\tMinimumHeartbeatSchemaVersion = {agent["heartbeat_schema"]["minimum"]}
\tMaximumHeartbeatSchemaVersion = {agent["heartbeat_schema"]["maximum"]}
\tMinimumDeliveryProtocolVersion = {protocol_range["minimum"]}
\tMaximumDeliveryProtocolVersion = {protocol_range["maximum"]}
\tDeliveryProtocolVersion = "{protocol_range["maximum"]}.0"
\tMinimumCompatibleAgentVersion = "{agent["minimum_compatible_version"]}"
\tMinimumSupportedAgentVersion = "{agent["minimum_supported_version"]}"
\tMaximumSupportedAgentVersionExclusive = "{agent["maximum_supported_version_exclusive"]}"
)

var requiredConnectCapabilities = [...]string{{
{capabilities}
}}

func RequiredConnectCapabilities() []string {{
\treturn append([]string(nil), requiredConnectCapabilities[:]...)
}}

func SupportedConnectContract() string {{
\treturn fmt.Sprintf("agent %s..<%s; tunnel %d-%d; heartbeat %d-%d; delivery %d-%d",
\t\tMinimumCompatibleAgentVersion, MaximumSupportedAgentVersionExclusive,
\t\tMinimumTunnelProtocolVersion, MaximumTunnelProtocolVersion,
\t\tMinimumHeartbeatSchemaVersion, MaximumHeartbeatSchemaVersion,
\t\tMinimumDeliveryProtocolVersion, MaximumDeliveryProtocolVersion)
}}
'''
    result = subprocess.run(["gofmt"], input=source, text=True, capture_output=True, check=False)
    if result.returncode != 0:
        raise ContractError(f"gofmt generated compatibility view: {result.stderr.strip()}")
    return result.stdout


def generate_helm(contract: dict[str, Any]) -> str:
    view = {
        "schema_version": contract["schema_version"],
        "release_line": contract["release_line"],
        "agent": contract["agent"],
        "agent_protocol": contract["agent_protocol"],
        "postgresql": contract["postgresql"],
    }
    return json.dumps(view, indent=2, sort_keys=True) + "\n"


def validate(contract_path: Path, schema_path: Path, root: Path) -> dict[str, Any]:
    contract = load_json(contract_path)
    schema = load_json(schema_path)
    if not isinstance(contract, dict) or not isinstance(schema, dict):
        raise ContractError("contract and schema roots must be JSON objects")
    validate_schema(contract, schema, schema)
    validate_semantics(contract, root)
    return contract


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("validate", "generate", "check"))
    parser.add_argument("--contract", type=Path, default=DEFAULT_CONTRACT)
    parser.add_argument("--schema", type=Path, default=DEFAULT_SCHEMA)
    parser.add_argument("--doc", type=Path, default=DEFAULT_DOC)
    parser.add_argument("--go", type=Path, default=DEFAULT_GO)
    parser.add_argument("--helm", type=Path, default=DEFAULT_HELM)
    parser.add_argument("--root", type=Path, default=ROOT)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        root = args.root.resolve()
        contract = validate(args.contract.resolve(), args.schema.resolve(), root)
        generated = {
            args.doc: generate_doc(contract),
            args.go: generate_go(contract),
            args.helm: generate_helm(contract),
        }
        if args.command == "validate":
            print("compatibility contract: valid")
            return 0
        if args.command == "generate":
            for path, payload in generated.items():
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(payload, encoding="utf-8")
                print(f"compatibility contract: generated {path}")
            return 0
        for path, payload in generated.items():
            try:
                existing = path.read_text(encoding="utf-8")
            except FileNotFoundError as exc:
                raise ContractError(f"generated compatibility view is missing: {path}") from exc
            if existing != payload:
                raise ContractError(f"generated compatibility view is stale: run {Path(__file__).name} generate")
        print("compatibility contract: valid; generated views are current")
        return 0
    except ContractError as exc:
        print(f"compatibility contract: ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
