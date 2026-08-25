#!/usr/bin/env python3
"""Derive conservative component sizing inputs from measured certification reports."""

import argparse
import hashlib
import json
import math
import statistics
from pathlib import Path

REPORT_SCHEMA = "astronomer-scale-report-v2"
OUTPUT_SCHEMA = "astronomer-component-sizing-recommendations-v1"
COMPONENTS = ("audit", "server", "tunnel", "worker")
MINIMUM_INDEPENDENT_REPORTS = 3
MINIMUM_SCALING_EFFICIENCY = 0.50
MAXIMUM_SCALING_EFFICIENCY = 1.25
MONOTONIC_NOISE_FLOOR = 0.95
RELEASE_FIELDS = (
    "commit", "images", "chart_values", "environment", "kubernetes_version",
    "postgres_version", "redis_version", "hardware",
)


def finite_positive(value, field):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value <= 0:
        raise ValueError(f"{field} must be a finite positive number")
    return float(value)


def load_report(path: Path) -> dict:
    report = json.loads(path.read_text(encoding="utf-8"))
    if report.get("schema_version") != REPORT_SCHEMA or report.get("verdict") != "pass" or report.get("certification") is not True:
        raise ValueError(f"{path}: only passing certification reports are accepted")
    metadata = report.get("metadata")
    if not isinstance(metadata, dict):
        raise ValueError(f"{path}: metadata is missing")
    try:
        replicas = json.loads(metadata.get("component_replicas", ""))
    except json.JSONDecodeError as error:
        raise ValueError(f"{path}: component_replicas is invalid JSON") from error
    if not isinstance(replicas, dict) or set(replicas) != set(COMPONENTS):
        raise ValueError(f"{path}: component_replicas must contain exactly {', '.join(COMPONENTS)}")
    metrics = report.get("component_metrics")
    if not isinstance(metrics, dict) or set(metrics) != set(COMPONENTS):
        raise ValueError(f"{path}: component_metrics must contain exactly {', '.join(COMPONENTS)}")
    components = {}
    for component in COMPONENTS:
        replica_count = replicas[component]
        if isinstance(replica_count, bool) or not isinstance(replica_count, int) or replica_count <= 0:
            raise ValueError(f"{path}: {component} replicas must be a positive integer")
        metric = metrics[component]
        if not isinstance(metric, dict):
            raise ValueError(f"{path}: {component} metric is missing")
        rate = finite_positive(metric.get("observed_rate"), f"{path}: {component} observed_rate")
        declared_load = finite_positive(metric.get("declared_load"), f"{path}: {component} declared_load")
        sample_count = metric.get("sample_count")
        if isinstance(sample_count, bool) or not isinstance(sample_count, int) or sample_count < 2:
            raise ValueError(f"{path}: {component} requires at least two raw observations")
        unit = metric.get("rate_unit")
        if not isinstance(unit, str) or not unit.strip():
            raise ValueError(f"{path}: {component} rate_unit is missing")
        components[component] = {
            "replicas": replica_count, "rate": rate, "declared_load": declared_load,
            "unit": unit, "sample_count": sample_count,
        }
    profile = report.get("profile")
    if not isinstance(profile, str) or not profile:
        raise ValueError(f"{path}: profile is missing")
    run_id = metadata.get("run_id")
    if not isinstance(run_id, str) or not run_id.strip():
        raise ValueError(f"{path}: metadata.run_id is missing")
    return {
        "path": path, "profile": profile, "clusters": report.get("clusters"),
        "target_rps": report.get("target_rps"), "run_id": run_id,
        "metadata": metadata, "components": components,
    }


def one_sided_t_critical_95(degrees_of_freedom: int) -> float:
    """Conservative 95% one-sided Student-t critical values.

    Values above 30 degrees of freedom deliberately use the df=30 value
    instead of converging to the smaller normal critical value.
    """
    table = {
        2: 2.920, 3: 2.353, 4: 2.132, 5: 2.015, 6: 1.943,
        7: 1.895, 8: 1.860, 9: 1.833, 10: 1.812, 11: 1.796,
        12: 1.782, 13: 1.771, 14: 1.761, 15: 1.753, 16: 1.746,
        17: 1.740, 18: 1.734, 19: 1.729, 20: 1.725, 21: 1.721,
        22: 1.717, 23: 1.714, 24: 1.711, 25: 1.708, 26: 1.706,
        27: 1.703, 28: 1.701, 29: 1.699, 30: 1.697,
    }
    return table[min(degrees_of_freedom, 30)]


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", action="append", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--docs-table-out", type=Path, required=True)
    parser.add_argument("--headroom", type=float, default=0.25)
    args = parser.parse_args()
    if not math.isfinite(args.headroom) or args.headroom < 0 or args.headroom > 1:
        raise SystemExit("headroom must be between 0 and 1")
    try:
        samples = [load_report(path.resolve()) for path in args.report]
    except (OSError, json.JSONDecodeError, ValueError) as error:
        raise SystemExit(str(error)) from error
    if len(samples) < MINIMUM_INDEPENDENT_REPORTS:
        raise SystemExit(f"at least {MINIMUM_INDEPENDENT_REPORTS} independent certification reports are required")
    profiles = [sample["profile"] for sample in samples]
    if len(set(profiles)) != len(profiles):
        raise SystemExit("profile samples must be unique")
    run_ids = [sample["run_id"] for sample in samples]
    if len(set(run_ids)) != len(run_ids):
        raise SystemExit("certification reports must come from distinct source runs")
    release = {field: samples[0]["metadata"].get(field) for field in RELEASE_FIELDS}
    if any(not isinstance(value, str) or not value.strip() for value in release.values()):
        raise SystemExit("release metadata is incomplete")
    for sample in samples[1:]:
        if {field: sample["metadata"].get(field) for field in RELEASE_FIELDS} != release:
            raise SystemExit("reports do not describe the same release and environment")

    calibration = {}
    for component in COMPONENTS:
        units = {sample["components"][component]["unit"] for sample in samples}
        replica_topologies = {sample["components"][component]["replicas"] for sample in samples}
        declared_loads = {round(sample["components"][component]["declared_load"], 12) for sample in samples}
        if len(units) != 1 or len(declared_loads) < 2:
            raise SystemExit(
                f"{component} requires consistent units and at least two distinct declared-load samples"
            )
        if len(replica_topologies) != 1:
            raise SystemExit(
                f"{component} mixes heterogeneous replica topologies; reduce each topology independently"
            )
        ordered = sorted(samples, key=lambda sample: (
            sample["components"][component]["declared_load"], sample["profile"],
        ))
        for previous, current in zip(ordered, ordered[1:]):
            previous_metric = previous["components"][component]
            current_metric = current["components"][component]
            if current_metric["declared_load"] == previous_metric["declared_load"]:
                continue
            if current_metric["rate"] < previous_metric["rate"] * MONOTONIC_NOISE_FLOOR:
                raise SystemExit(
                    f"{component} observed capacity decreases as declared load increases: "
                    f"{previous['profile']} -> {current['profile']}"
                )
            declared_growth = current_metric["declared_load"] / previous_metric["declared_load"]
            observed_growth = current_metric["rate"] / previous_metric["rate"]
            efficiency = observed_growth / declared_growth
            if efficiency < MINIMUM_SCALING_EFFICIENCY or efficiency > MAXIMUM_SCALING_EFFICIENCY:
                raise SystemExit(
                    f"{component} scaling efficiency {efficiency:.3f} is outside "
                    f"[{MINIMUM_SCALING_EFFICIENCY:.2f}, {MAXIMUM_SCALING_EFFICIENCY:.2f}] "
                    f"between {previous['profile']} and {current['profile']}"
                )
        per_replica = [
            sample["components"][component]["rate"] / sample["components"][component]["replicas"]
            for sample in samples
        ]
        mean = statistics.fmean(per_replica)
        standard_error = statistics.stdev(per_replica) / math.sqrt(len(per_replica))
        lower_confidence_bound = mean - one_sided_t_critical_95(len(per_replica) - 1) * standard_error
        conservative_rate = min(min(per_replica), lower_confidence_bound)
        if not math.isfinite(conservative_rate) or conservative_rate <= 0:
            raise SystemExit(
                f"{component} has no positive one-sided 95% lower confidence bound; collect more stable samples"
            )
        calibration[component] = {
            "rate_unit": units.pop(),
            "conservative_safe_rate_per_replica": round(conservative_rate, 6),
            "method": "one-sided-95pct-student-t-lower-bound-capped-at-minimum",
            "per_report_rates": [round(value, 6) for value in per_replica],
            "minimum_observed_replicas": min(sample["components"][component]["replicas"] for sample in samples),
            "sample_count": len(samples),
        }

    recommendations = []
    for sample in sorted(samples, key=lambda item: item["profile"]):
        components = {}
        for component in COMPONENTS:
            basis = calibration[component]
            desired = sample["components"][component]["rate"] * (1 + args.headroom)
            replicas = max(
                basis["minimum_observed_replicas"],
                math.ceil(desired / basis["conservative_safe_rate_per_replica"]),
            )
            components[component] = {
                "recommended_replicas": replicas,
                "target_rate": round(sample["components"][component]["rate"], 6),
                "rate_unit": basis["rate_unit"],
            }
        recommendations.append({
            "profile": sample["profile"], "clusters": sample["clusters"], "target_rps": sample["target_rps"],
            "components": components,
            "report_sha256": hashlib.sha256(sample["path"].read_bytes()).hexdigest(),
        })

    output = {
        "schema_version": OUTPUT_SCHEMA,
        "status": "evidence-derived-unpublished",
        "claim": "Not a production recommendation until the referenced reports are real retained certification evidence.",
        "release": release,
        "headroom_fraction": args.headroom,
        "statistical_method": "one-sided 95% Student-t lower confidence bound, capped at the minimum observed per-replica rate",
        "comparability_contract": {
            "distinct_source_runs": True,
            "single_replica_topology_per_component": True,
            "monotonic_noise_floor": MONOTONIC_NOISE_FLOOR,
            "scaling_efficiency_bounds": [MINIMUM_SCALING_EFFICIENCY, MAXIMUM_SCALING_EFFICIENCY],
        },
        "calibration": calibration,
        "recommendations": recommendations,
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(output, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    lines = [
        "| Profile | Clusters | Target HTTP RPS | Evidence-derived component replicas | Status |",
        "|---|---:|---:|---|---|",
    ]
    for row in recommendations:
        sizing = ", ".join(f"{name}={row['components'][name]['recommended_replicas']}" for name in COMPONENTS)
        lines.append(f"| {row['profile']} | {row['clusters']} | {row['target_rps']} | {sizing} | unpublished evidence input |")
    args.docs_table_out.parent.mkdir(parents=True, exist_ok=True)
    args.docs_table_out.write_text("\n".join(lines) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
