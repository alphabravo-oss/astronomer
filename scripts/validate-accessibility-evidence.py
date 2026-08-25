#!/usr/bin/env python3
"""Validate the complete human assistive-technology release matrix."""

import argparse
import importlib.util
import json
from pathlib import Path


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("evidence", type=Path)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-run-id", required=True)
    args = parser.parse_args()
    validator_path = Path(__file__).with_name("validate-release-approval.py")
    spec = importlib.util.spec_from_file_location("release_approval", validator_path)
    validator = importlib.util.module_from_spec(spec); spec.loader.exec_module(validator)
    validator.regular(args.evidence, "accessibility evidence")
    value = json.loads(args.evidence.read_text(encoding="utf-8"))
    validator.validate_accessibility(value, args.tag, args.source_commit, args.source_run_id)


if __name__ == "__main__":
    main()
