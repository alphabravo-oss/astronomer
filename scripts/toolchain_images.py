"""Bounded read-only OCI registry resolution; ephemeral anonymous auth only."""
import hashlib
import json
import re
import urllib.error
import urllib.parse
import urllib.request

MEDIA = ", ".join(["application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json"])


def request(url, headers=None):
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers or {}), timeout=30) as response:
        raw = response.read(8 * 1024 * 1024 + 1)
        if len(raw) > 8 * 1024 * 1024:
            raise ValueError("registry metadata exceeds bound")
        return raw, response.headers


def resolve_image(reference, required=("linux/amd64", "linux/arm64")):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9./:_@-]*", reference) or ".." in reference or reference.count("@") > 1:
        raise ValueError("invalid OCI reference")
    if "@" in reference:
        name, tag = reference.split("@", 1)
    else:
        name, tag = reference.rsplit(":", 1)
    if "/" not in name:
        name = "docker.io/library/" + name
    registry, repository = name.split("/", 1)
    if "." not in registry and ":" not in registry:
        repository = name
        registry = "docker.io"
    if not re.fullmatch(r"[A-Za-z0-9.-]+(?::[0-9]+)?", registry) or not re.fullmatch(r"[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*", repository) or not re.fullmatch(r"(?:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}|sha256:[a-f0-9]{64})", tag):
        raise ValueError("invalid OCI registry, repository or reference")
    host = "registry-1.docker.io" if registry == "docker.io" else registry
    endpoint = f"https://{host}/v2/{repository}"
    headers = {"Accept": MEDIA}
    url = endpoint + "/manifests/" + tag
    try:
        raw, response_headers = request(url, headers)
    except urllib.error.HTTPError as error:
        if error.code != 401:
            raise
        challenge = error.headers.get("WWW-Authenticate", "")
        if not challenge.startswith("Bearer "):
            raise ValueError("registry lacks supported read-only bearer challenge") from error
        fields = dict(re.findall(r'(\w+)="([^"]+)"', challenge))
        realm = urllib.parse.urlparse(fields["realm"])
        if realm.scheme != "https" or not realm.hostname or realm.username or realm.password or realm.fragment or realm.query:
            raise ValueError("invalid registry auth realm") from error
        query = urllib.parse.urlencode({"service": fields.get("service", host), "scope": f"repository:{repository}:pull"})
        token_raw, _ = request(fields["realm"] + "?" + query)
        token_data = json.loads(token_raw)
        headers["Authorization"] = "Bearer " + token_data.get("token", token_data.get("access_token", ""))
        raw, response_headers = request(url, headers)
    digest = "sha256:" + hashlib.sha256(raw).hexdigest()
    if response_headers.get("Docker-Content-Digest", digest) != digest:
        raise ValueError("registry digest contradicts manifest bytes")
    if tag.startswith("sha256:") and digest != tag:
        raise ValueError("registry returned different content for immutable digest")
    index = json.loads(raw)
    platforms = {}
    for manifest in index.get("manifests", []):
        platform = manifest.get("platform", {})
        key = platform.get("os", "") + "/" + platform.get("architecture", "")
        if key in required:
            if key in platforms:
                raise ValueError("ambiguous image platform manifest")
            if not re.fullmatch(r"sha256:[a-f0-9]{64}", manifest["digest"]):
                raise ValueError("invalid platform manifest digest")
            platforms[key] = manifest["digest"]
    if not set(required).issubset(platforms):
        raise ValueError("image lacks required platforms")
    manifests = {}
    for platform in sorted(required):
        manifest_raw, _ = request(endpoint + "/manifests/" + platforms[platform], headers)
        if "sha256:" + hashlib.sha256(manifest_raw).hexdigest() != platforms[platform]:
            raise ValueError("platform manifest digest mismatch")
        manifests[platform] = json.loads(manifest_raw)
    manifest = manifests[sorted(required)[0]]
    config_digest = manifest["config"]["digest"]
    if not re.fullmatch(r"sha256:[a-f0-9]{64}", config_digest):
        raise ValueError("invalid image config digest")
    config_raw, _ = request(endpoint + "/blobs/" + config_digest, headers)
    if "sha256:" + hashlib.sha256(config_raw).hexdigest() != config_digest:
        raise ValueError("image config digest mismatch")
    config = json.loads(config_raw)
    identity = name + "@" + digest if tag.startswith("sha256:") else name + ":" + tag + "@" + digest
    return {"reference": identity, "digest": digest, "platforms": sorted(platforms), "platformDigests": platforms, "createdAt": config.get("created"), "environment": config.get("config", {}).get("Env", []), "labels": config.get("config", {}).get("Labels", {}), "source": url}
