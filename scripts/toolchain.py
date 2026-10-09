#!/usr/bin/env python3
"""Offline alignment, explicit generation, and read-only stable discovery."""
import argparse
import concurrent.futures
import datetime as dt
import hashlib
import json
import pathlib
import re
import subprocess
import sys
import shutil
import urllib.error
import urllib.request
import urllib.parse
import xml.etree.ElementTree as ET
from toolchain_images import resolve_image
import toolchain_inventory

ROOT = pathlib.Path(__file__).resolve().parent.parent
AUTHORITY = ROOT / "deploy/toolchain/versions.json"
SCHEMA = ROOT / "deploy/toolchain/versions.schema.json"
STABLE = re.compile(r"^(?:v|go)?\d+\.\d+(?:\.\d+)?(?:\+[^\s]+)?$")


def serialized(document):
    return json.dumps(document, indent=2, sort_keys=True) + "\n"


def timestamp(value, date_only=False):
    if date_only and re.fullmatch(r"\d{4}-\d{2}-\d{2}", value):
        value += "T00:00:00Z"
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError("timestamp must have an explicit UTC offset")
    return parsed.astimezone(dt.timezone.utc)


def validate(document, now=None, release=False):
    now = now or dt.datetime.now(dt.timezone.utc)
    ids = set()
    for component in document["components"]:
        name = component["id"]
        if name in ids:
            raise ValueError(f"duplicate component ID: {name}")
        ids.add(name)
        parsed = urllib.parse.urlparse(component["upstream"]["url"])
        if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.fragment:
            raise ValueError(f"{name}: invalid upstream locator")
        discovery = component["discovery"]
        for key in ("releasedAt", "observedAt"):
            if timestamp(discovery[key], date_only=key == "releasedAt") > now:
                raise ValueError(f"{name}: future {key}")
        if timestamp(discovery["releasedAt"], date_only=True) > timestamp(discovery["observedAt"]):
            raise ValueError(f"{name}: release published after discovery")
        if not STABLE.fullmatch(discovery["version"]):
            # Go's @latest legitimately selects a timestamped commit when a
            # module has no final semantic release. It is never an RC/canary.
            if component["ecosystem"] != "gomod" or not re.fullmatch(r"v\d+\.\d+\.\d+-\d{14}-[a-f0-9]{12}", discovery["version"]):
                raise ValueError(f"{name}: non-final discovery version")
        exception = component.get("exception")
        selected = component["selected"]
        if selected is not None and not STABLE.fullmatch(selected):
            if component["ecosystem"] != "gomod" or not re.fullmatch(r"v\d+\.\d+\.\d+-\d{14}-[a-f0-9]{12}", selected):
                raise ValueError(f"{name}: non-final selected version")
        if exception and any(not isinstance(exception.get(key), str) or not exception[key].strip() for key in ("owner", "reason", "evidence", "reviewBy", "exitCondition")):
            raise ValueError(f"{name}: incomplete owned exception")
        if component["selected"] != discovery["version"] and not exception:
            raise ValueError(f"{name}: lagging selection without owned exception")
        if exception and timestamp(exception["reviewBy"]) <= now:
            raise ValueError(f"{name}: expired compatibility exception")
        if release and (now - timestamp(discovery["observedAt"])).days >= document["policy"]["maxDiscoveryAgeDays"]:
            raise ValueError(f"{name}: stale discovery")
        for artifact in component["artifacts"]:
            if not set(component["platforms"]).issubset(artifact["platforms"]):
                raise ValueError(f"{name}: unsupported artifact platform")
            platform_digests = artifact.get("platformDigests")
            if platform_digests is not None and set(platform_digests) != set(artifact["platforms"]):
                raise ValueError(f"{name}: artifact platform-map contradiction")
            if selected and ":" + selected not in artifact["reference"] and ":v" + selected not in artifact["reference"]:
                raise ValueError(f"{name}: artifact does not identify selected version")
            if timestamp(artifact["verifiedAt"]) > now:
                raise ValueError(f"{name}: future artifact verification")
            if not artifact["reference"].endswith("@" + artifact["digest"]):
                raise ValueError(f"{name}: reference/digest contradiction")
        for consumer in component["consumers"]:
            path = pathlib.PurePosixPath(consumer["path"])
            if path.is_absolute() or ".." in path.parts:
                raise ValueError(f"{name}: consumer escapes repository")
    for row in document.get("inventory", []):
        path = pathlib.PurePosixPath(row["path"])
        if path.is_absolute() or ".." in path.parts:
            raise ValueError("inventory source escapes repository")
        if row["state"] == "leased":
            if any(not row.get(key, "").strip() for key in ("reviewBy", "evidence", "owner", "reason")):
                raise ValueError("inventory lease lacks owned evidence/review")
            if timestamp(row["reviewBy"]) <= now:
                raise ValueError("expired source consumer lease")
    return document


def align(document, root=ROOT, write=False):
    failures = []
    for component in document["components"]:
        for consumer in component["consumers"]:
            path = root / consumer["path"]
            source = path.read_text()
            expected = consumer["value"].replace("{version}", component["selected"] or "")
            if component["artifacts"]:
                expected = expected.replace("{reference}", component["artifacts"][0]["reference"])
            if consumer["kind"] == "json":
                metadata = json.loads(source)
                cursor = metadata
                keys = [key.replace("~1", "/").replace("~0", "~") for key in consumer["selector"].split("/")]
                for key in keys[:-1]:
                    cursor = cursor[int(key) if isinstance(cursor, list) else key]
                final_key = int(keys[-1]) if isinstance(cursor, list) else keys[-1]
                if cursor[final_key] != expected:
                    failures.append(f"{component['id']}: {consumer['path']}:{consumer['selector']}")
                    if write:
                        cursor[final_key] = expected
                        path.write_text(json.dumps(metadata, indent=2) + "\n")
            else:
                pattern = re.compile(consumer["selector"], re.MULTILINE)
                matches = list(pattern.finditer(source))
                if len(matches) != consumer.get("count", 1):
                    raise ValueError(f"{component['id']}: missing/extra consumer matches in {path}")
                if any(match.group(1) != expected for match in matches):
                    failures.append(f"{component['id']}: {consumer['path']}")
                    if write:
                        source = pattern.sub(lambda match: match.group(0)[:match.start(1)-match.start()] + expected + match.group(0)[match.end(1)-match.start():], source)
                        path.write_text(source)
    if failures and not write:
        raise ValueError("version consumer drift:\n" + "\n".join(failures))


def fetch_bytes(url):
    accept = "text/html, application/atom+xml" if url.startswith("https://github.com/") else "application/json"
    request = urllib.request.Request(url, headers={"User-Agent": "Astronomer-version-discovery", "Accept": accept})
    with urllib.request.urlopen(request, timeout=30) as response:
        if response.status != 200:
            raise ValueError(f"upstream HTTP status {response.status}")
        raw = response.read(64 * 1024 * 1024 + 1)
        if len(raw) > 64 * 1024 * 1024:
            raise ValueError("upstream metadata exceeds bound")
    return raw, "sha256:" + hashlib.sha256(raw).hexdigest()


def fetch_json(url):
    try:
        raw, digest = fetch_bytes(url)
    except urllib.error.HTTPError as error:
        # The authenticated official GitHub CLI is an optional read-only
        # fallback for shared-IP limits. Ordinary offline checks never use it.
        parsed = urllib.parse.urlparse(url)
        if error.code != 403 or parsed.hostname != "api.github.com" or not re.fullmatch(r"/repos/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/releases", parsed.path) or not shutil.which("gh"):
            raise
        result = subprocess.run(["gh", "api", parsed.path.removeprefix("/")], capture_output=True, timeout=30)
        if result.returncode != 0:
            raise ValueError("official GitHub metadata unavailable after authenticated read-only fallback") from error
        raw = result.stdout
        digest = "sha256:" + hashlib.sha256(raw).hexdigest()
    return json.loads(raw), digest


def final_candidates(rows, now):
    accepted = []
    for version, date, stable in rows:
        if not stable or not STABLE.fullmatch(version):
            continue
        if timestamp(date, date_only=True) > now:
            raise ValueError(f"future final release: {version}")
        accepted.append((tuple(map(int, re.findall(r"\d+", version.split("+")[0]))), version, date))
    if not accepted:
        raise ValueError("no unambiguous final release")
    return max(accepted)[1:]


def github_feed(url, now):
    raw, digest = fetch_bytes(url)
    feed = ET.fromstring(raw)
    namespace = {"a": "http://www.w3.org/2005/Atom"}
    candidates = []
    for entry in feed.findall("a:entry", namespace):
        link = entry.find("a:link", namespace)
        if link is None:
            continue
        page_url = link.attrib["href"]
        version = page_url.rsplit("/", 1)[-1].removeprefix("v")
        if STABLE.fullmatch(version):
            candidates.append((tuple(map(int, version.split("."))), version, page_url))
    for _, version, page_url in sorted(candidates, reverse=True):
        if not page_url.lower().startswith(url.removesuffix(".atom").lower() + "/tag/"):
            raise ValueError("GitHub feed links outside declared release repository")
        page, page_digest = fetch_bytes(page_url)
        html = page.decode()
        if re.search(r">\s*Pre-release\s*</span>", html):
            continue
        # Bind the publication timestamp to the release event. Other times
        # on this page describe included commits and must not be accepted.
        dates = re.findall(r'released this\s*<relative-time\b[^>]*datetime="([^"]+)"', html)
        if len(dates) != 1:
            raise ValueError("GitHub release lacks unambiguous publication date")
        date = timestamp(dates[0]).isoformat()
        final_candidates([(version, date, True)], now)
        return version, date, digest + "; " + page_digest
    raise ValueError("GitHub feed lacks independently dated final release")


def resolve(component, now=None):
    now = now or dt.datetime.now(dt.timezone.utc)
    upstream = component["upstream"]
    kind = upstream["kind"]
    if kind == "github-feed":
        version, date, digest = github_feed(upstream["url"], now)
        rows = [(version, date, True)]
        metadata = None
    elif kind == "image":
        image = resolve_image(upstream["locator"], component["platforms"])
        environment = dict(entry.split("=", 1) for entry in image["environment"] if "=" in entry)
        version = environment.get("NGINX_VERSION") or image["labels"].get("org.opencontainers.image.version")
        if not version:
            raise ValueError("image lacks official version metadata")
        rows = [(version, image["createdAt"], True)]
        metadata, digest = None, image["digest"]
    elif kind == "helm-index":
        raw, digest = fetch_bytes(upstream["url"])
        metadata = json.loads(subprocess.check_output(["go", "run", "./deploy/toolchain/check", "--yaml-json"], input=raw, cwd=ROOT))
    else:
        metadata, digest = fetch_json(upstream["url"])
    if kind in ("image", "github-feed"):
        pass
    elif kind == "alpine":
        branch = next(branch for branch in metadata["release_branches"] if branch["rel_branch"] == metadata["latest_stable"])
        rows = [(release["version"], release["date"], True) for release in branch["releases"]]
        if not {"aarch64", "x86_64"}.issubset(branch["arches"]):
            raise ValueError("Alpine branch lacks required architectures")
    elif kind == "npm":
        version = metadata["dist-tags"]["latest"]
        if version not in metadata["versions"]:
            raise ValueError("npm latest missing from versions")
        rows = [(version, metadata["time"][version], True)]
    elif kind == "node":
        rows = [(row["version"].removeprefix("v"), row["date"], True) for row in metadata]
        version, _ = final_candidates(rows, now)
        selected_row = next(row for row in metadata if row["version"].removeprefix("v") == version)
        needed = {"linux-x64", "linux-arm64"}
        if not needed.issubset(selected_row["files"]):
            raise ValueError("Node release lacks required platforms")
    elif kind == "github":
        rows = [(row["tag_name"].removeprefix("v"), row["published_at"], not row["draft"] and not row["prerelease"]) for row in metadata]
    elif kind == "gomod":
        rows = [(metadata["Version"], metadata["Time"], True)]
    elif kind == "helm-index":
        rows = [(row["version"], row["created"], True) for row in metadata["entries"][upstream["locator"]]]
    elif kind == "go":
        final = [row for row in metadata if row["stable"] and STABLE.fullmatch(row["version"])]
        if not final:
            raise ValueError("Go metadata lacks a final release")
        row = max(final, key=lambda item: tuple(map(int, item["version"].removeprefix("go").split("."))))
        needed = {("linux", "amd64"), ("linux", "arm64")}
        if not needed.issubset({(f["os"], f["arch"]) for f in row["files"] if f["kind"] == "archive" and re.fullmatch("[a-f0-9]{64}", f["sha256"])}):
            raise ValueError("Go release lacks checked platform archives")
        # Go download JSON has no release dates. Independently cross-check
        # the official dated release history before accepting a new compiler.
        history, history_digest = fetch_bytes("https://go.dev/doc/devel/release")
        version = row["version"].removeprefix("go")
        match = re.search(re.escape(row["version"]) + r"\s*\(released\s+(\d{4}-\d{2}-\d{2})\)", history.decode())
        if not match:
            raise ValueError("Go final release absent from independently dated release history")
        date = match.group(1)
        rows = [(version, date, True)]
        digest += "; " + history_digest
    else:
        raise ValueError(f"{kind}: requires component-specific qualification resolver")
    if kind == "gomod" and re.fullmatch(r"v\d+\.\d+\.\d+-\d{14}-[a-f0-9]{12}", rows[0][0]):
        version, date, _ = rows[0]
        if timestamp(date) > now:
            raise ValueError("future Go module commit")
    else:
        version, date = final_candidates(rows, now)
    return {"id": component["id"], "selected": component["selected"], "upstreamStable": version, "releasedAt": date, "observedAt": now.isoformat().replace("+00:00", "Z"), "metadataDigest": digest, "source": upstream["url"], "status": "current" if version == component["selected"] else "review-required"}


def discover(document):
    def one(component):
        try:
            return resolve(component)
        except (ValueError, KeyError, TypeError, urllib.error.URLError, TimeoutError) as error:
            return {"id": component["id"], "status": "unavailable", "error": str(error)}
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        return sorted(pool.map(one, document["components"]), key=lambda row: row["id"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--check", action="store_true")
    mode.add_argument("--write", action="store_true")
    mode.add_argument("--discover", action="store_true")
    parser.add_argument("--freshness", action="store_true", help="also reject stale source discovery")
    parser.add_argument("--release", action="store_true", help="verify an offline signed resolution")
    parser.add_argument("--resolution", type=pathlib.Path)
    parser.add_argument("--bundle", type=pathlib.Path)
    parser.add_argument("--report", type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()
    if args.release:
        if not args.resolution or not args.bundle:
            raise ValueError("release acceptance requires a signed resolution and Sigstore bundle")
        subprocess.run(["python3", str(ROOT / "scripts/toolchain-resolution.py"), "--verify", "--record", str(args.resolution), "--bundle", str(args.bundle)], check=True)
        return 0
    subprocess.run(["go", "run", "./deploy/toolchain/check", str(SCHEMA), str(AUTHORITY)], cwd=ROOT, check=True)
    source = AUTHORITY.read_text()
    document = validate(json.loads(source), release=args.freshness)
    if source != serialized(document):
        raise ValueError("version authority is not deterministically serialized")
    align(document, write=args.write)
    toolchain_inventory.check(document, ROOT)
    if args.report:
        now = dt.datetime.now(dt.timezone.utc)
        rows = [{"id": c["id"], "selected": c["selected"], "upstreamStable": c["discovery"]["version"], "owner": c["owner"], "discoveryAgeDays": (now - timestamp(c["discovery"]["observedAt"])).days, "reviewDeadline": c.get("exception", {}).get("reviewBy"), "exception": c.get("exception"), "qualification": c["qualification"]} for c in document["components"]]
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(serialized({"schemaVersion": 1, "authorityDigest": "sha256:" + hashlib.sha256(source.encode()).hexdigest(), "components": rows}))
    if args.discover:
        report = {"schemaVersion": 1, "authorityDigest": "sha256:" + hashlib.sha256(source.encode()).hexdigest(), "components": discover(document)}
        if args.output:
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_text(serialized(report))
        for row in report["components"]:
            print(f"{row['id']}: {row['status']} {row.get('upstreamStable', row.get('error', ''))}")
        subprocess.run(["go", "list", "-u", "-m", "all"], cwd=ROOT, check=True)
        if any(row["status"] == "unavailable" for row in report["components"]):
            return 1
    else:
        print(f"Toolchain authority aligned: {len(document['components'])} components")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
