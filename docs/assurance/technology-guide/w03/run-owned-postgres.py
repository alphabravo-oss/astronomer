#!/usr/bin/env python3
"""Bounded transaction fixture; owns only a uniquely labeled local container."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import time

ROOT = Path(__file__).resolve().parents[4]
EVIDENCE = Path(__file__).resolve().parent
name = "astronomer-plan032-w03-pg-" + secrets.token_hex(5)
password = secrets.token_hex(24)


def run(arguments):
    return subprocess.run(arguments, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)


try:
    run(["docker", "run", "-d", "--rm", "--name", name,
         "--label", "astronomer.qualification.owner=plan032-w03",
         "-e", "POSTGRES_USER=integration", "-e", "POSTGRES_PASSWORD=" + password,
         "-e", "POSTGRES_DB=integration", "-p", "127.0.0.1::5432", "pgvector/pgvector:pg16"])
    for _ in range(60):
        if subprocess.run(["docker", "exec", name, "pg_isready", "-U", "integration", "-d", "integration"],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            break
        time.sleep(1)
    else:
        raise RuntimeError("owned PostgreSQL fixture did not become ready")
    port = run(["docker", "port", name, "5432/tcp"]).stdout.decode().strip().rsplit(":", 1)[1]
    env = dict(os.environ, GOTOOLCHAIN="go1.27.2", GOMAXPROCS="4", GOFLAGS="-p=2",
               BUILTIN_PROVISIONER_TEST_DATABASE_URL=f"postgres://integration:{password}@127.0.0.1:{port}/integration?sslmode=disable")
    command = ["go", "test", "-json", "./internal/delivery/builtin", "-run",
               "TestProvisionerPostgresMultiSourceAssets|TestAutomaticScanningPostgres", "-count=1", "-timeout=3m"]
    result = subprocess.run(command, cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    output = result.stdout.decode()
    if password in output or password in result.stderr.decode():
        raise RuntimeError("fixture output cannot be retained")
    (EVIDENCE / "postgres-transition-test.jsonl").write_text(output)
    events = [json.loads(line) for line in output.splitlines() if line.startswith("{")]
    terminal = {item["Test"]: item["Action"] for item in events if item.get("Test") and item["Action"] in {"pass", "fail", "skip"}}
    expected = ["TestProvisionerPostgresMultiSourceAssets", "TestAutomaticScanningPostgres"]
    complete = all(terminal.get(test) == "pass" for test in expected)
    summary = {"schema_version": 1, "fixture": "owned isolated Docker pgvector/pgvector:pg16; unique random schemas",
               "release_qualification": False, "postgres18_cnpg_qualification": False,
               "command": "GOTOOLCHAIN=go1.27.2 GOMAXPROCS=4 GOFLAGS=-p=2 BUILTIN_PROVISIONER_TEST_DATABASE_URL=<private owned fixture DSN> " + " ".join(command),
               "exit_code": result.returncode, "all_expected_tests_executed_passed": complete, "tests": terminal}
    (EVIDENCE / "postgres-transition-summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print("Owned PostgreSQL transition fixture:", "PASS" if complete and result.returncode == 0 else "FAIL",
          "with", len(terminal), "actual terminal test records")
    if result.returncode:
        for item in events:
            line = item.get("Output", "")
            if "FAIL" in line or "_test.go:" in line:
                print(line.strip())
    raise SystemExit(result.returncode if result.returncode else (0 if complete else 1))
finally:
    subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
