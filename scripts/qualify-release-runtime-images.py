#!/usr/bin/env python3
"""Qualify every transitive release image by exact digest."""
from __future__ import annotations
import argparse, datetime as dt, hashlib, json, re, subprocess
from pathlib import Path

REF = re.compile(r"^[^\s@]+@sha256:[a-f0-9]{64}$")
SPDX_TOKEN = re.compile(r"[A-Za-z0-9][A-Za-z0-9.+-]*")
OPERATORS = {"AND", "OR", "WITH"}

def canonical(v): return (json.dumps(v, sort_keys=True, separators=(",", ":")) + "\n").encode()
def sha(path): return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()
def load_closed(path, keys, label):
    value=json.loads(path.read_text())
    if not isinstance(value,dict) or set(value)!=set(keys): raise ValueError(f"{label} violates closed schema")
    return value
def run(command, output):
    with output.open("wb") as handle: subprocess.run(command, check=True, stdout=handle)

def references(manifest):
    refs=[]
    refs += [x["reference"] for x in manifest["astronomer"]["images"]]
    refs += [x["reference"] for x in manifest["astronomer"]["runtime_images"]]
    refs += [x["reference"] for x in manifest["flux"]["controllers"]]
    for component in manifest["built_in_bundles"]["components"]: refs += component["images"]
    charlie=manifest["charlie"]["artifact"]
    if charlie["kind"] == "container_image": refs.append(charlie["reference"])
    result=sorted(set(refs))
    if not result or any(not REF.fullmatch(ref) for ref in result): raise ValueError("release contains a mutable or malformed image reference")
    return result

def waiver_map(document, now, release_version=None):
    if document["schema_version"] != 1 or not isinstance(document["waivers"],list): raise ValueError("invalid waiver document")
    result={}
    exact={"reference","category","ids","reason","approved_by","expires_at"}
    for waiver in document["waivers"]:
        if not isinstance(waiver,dict) or set(waiver) not in (exact, exact | {"release_version"}) or not REF.fullmatch(waiver["reference"]): raise ValueError("waiver violates closed exact-reference schema")
        if "release_version" in waiver and (not isinstance(waiver["release_version"],str) or not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+",waiver["release_version"]) or waiver["release_version"] != release_version): raise ValueError("waiver release version does not match the manifest")
        if waiver["category"] not in {"vulnerability","license"} or not waiver["ids"] or not all(isinstance(x,str) and x for x in waiver["ids"]): raise ValueError("waiver category/ids invalid")
        if not waiver["reason"].strip() or not waiver["approved_by"].strip(): raise ValueError("waiver requires reason and approver")
        expiry=dt.datetime.fromisoformat(waiver["expires_at"].replace("Z","+00:00"))
        if expiry.tzinfo is None or expiry <= now: raise ValueError("expired waiver is forbidden")
        for item in waiver["ids"]:
            key=(waiver["reference"],waiver["category"],item)
            if key in result: raise ValueError("duplicate waiver scope is forbidden")
            result[key] = digest_waiver(waiver,item)
    return result
def digest_waiver(w,item): return "sha256:"+hashlib.sha256(canonical({**w,"id":item})).hexdigest()

def qualify_findings(version, reference, vulnerabilities, licenses):
    # The v1.2.0/v1.2.1 publication decision defers license qualification, not scanning
    # or retention. Never apply this exception to vulnerabilities or later tags.
    if vulnerabilities or (licenses and version not in {"v1.2.0", "v1.2.1"}):
        raise ValueError(f"unwaived policy findings for {reference}: vulnerabilities={vulnerabilities}, licenses={licenses}")
    return {
        "high_critical_unwaived": 0,
        "license_unwaived": len(set(licenses)),
        "license_findings": sorted(set(licenses)),
        "license_qualification": "pending_review" if licenses else "passed",
    }


def prepare_oci_source(directory, reference):
    """Bind an extracted OCI archive to the signed image, before either scanner reads it."""
    if not REF.fullmatch(reference): raise ValueError("OCI source requires an exact image reference")
    root = directory.resolve()
    if (root / "index.json").is_symlink(): raise ValueError("unsafe OCI index path")
    digest = reference.split("@", 1)[1]
    visited = set()
    def blob(descriptor, document=False):
        value = descriptor["digest"]
        if not re.fullmatch(r"sha256:[a-f0-9]{64}", value): raise ValueError("invalid OCI blob digest")
        path = root / "blobs" / "sha256" / value.split(":", 1)[1]
        if not path.resolve().is_relative_to(root) or path.is_symlink(): raise ValueError("unsafe OCI blob path")
        content = path.read_bytes()
        if "sha256:" + hashlib.sha256(content).hexdigest() != value: raise ValueError("OCI blob digest mismatch")
        if "size" in descriptor and descriptor["size"] != len(content): raise ValueError("OCI blob size mismatch")
        if document and value not in visited:
            visited.add(value)
            parsed = json.loads(content)
            for child in parsed.get("manifests", []): blob(child, True)
            if "config" in parsed: blob(parsed["config"])
            for child in parsed.get("layers", []): blob(child)
        return content
    raw = blob({"digest": digest}, True)
    index = json.loads(raw)
    platforms = {f"{x.get('platform',{}).get('os','')}/{x.get('platform',{}).get('architecture','')}" for x in index.get("manifests", [])}
    if not {"linux/amd64", "linux/arm64"}.issubset(platforms): raise ValueError("OCI source lacks required platforms")
    # Scan the same platform as the Linux release runner. A flat layout also
    # supports the pinned Syft version, which cannot resolve nested indexes.
    # The selected manifest and every blob are bound by the authenticated root.
    selected = [x for x in index["manifests"] if x.get("platform", {}).get("os") == "linux" and x["platform"].get("architecture") == "amd64"]
    if len(selected) != 1: raise ValueError("ambiguous OCI scan platform")
    (root / "index.json").write_bytes(canonical({"schemaVersion": 2, "manifests": selected}))
    return raw


def main():
    p=argparse.ArgumentParser(); p.add_argument("--manifest",type=Path,required=True); p.add_argument("--waivers",type=Path,required=True); p.add_argument("--license-policy",type=Path,required=True); p.add_argument("--work-dir",type=Path,required=True); p.add_argument("--output",type=Path,required=True); p.add_argument("--charlie-oci-layout",type=Path); a=p.parse_args()
    manifest=json.loads(a.manifest.read_text()); waivers=load_closed(a.waivers,{"schema_version","waivers"},"waivers"); policy=load_closed(a.license_policy,{"schema_version","allowed_spdx_ids"},"license policy")
    if a.charlie_oci_layout and manifest["charlie"]["artifact"]["kind"] != "container_image": raise ValueError("Charlie OCI source requires a container image")
    if policy["schema_version"]!=1 or not isinstance(policy["allowed_spdx_ids"],list): raise ValueError("invalid license policy")
    allowed=set(policy["allowed_spdx_ids"]); now=dt.datetime.now(dt.timezone.utc); indexed=waiver_map(waivers,now,manifest["release"]["version"])
    a.work_dir.mkdir(parents=True,exist_ok=True); entries=[]
    for index,ref in enumerate(references(manifest)):
        prefix=a.work_dir/f"image-{index:03d}"; raw=prefix.with_suffix(".manifest.json")
        archive = a.charlie_oci_layout if ref == manifest["charlie"]["artifact"]["reference"] else None
        if archive:
            raw.write_bytes(prepare_oci_source(archive, ref))
        else:
            run(["docker","buildx","imagetools","inspect",ref,"--raw"],raw)
        image_manifest=json.loads(raw.read_text()); platforms=sorted({f"{x.get('platform',{}).get('os','')}/{x.get('platform',{}).get('architecture','')}" for x in image_manifest.get("manifests",[])})
        if not {"linux/amd64","linux/arm64"}.issubset(platforms): raise ValueError(f"image lacks required platforms: {ref}")
        vuln=prefix.with_suffix(".trivy.json"); source=["--input",str(archive),"--platform","linux/amd64"] if archive else [ref]; run(["trivy","image","--quiet","--format","json","--scanners","vuln","--severity","HIGH,CRITICAL","--ignore-unfixed",*source],vuln)
        vuln_ids=sorted({v["VulnerabilityID"] for result in json.loads(vuln.read_text()).get("Results",[]) for v in (result.get("Vulnerabilities") or [])})
        applied=[]; unwaived=[]
        for item in vuln_ids:
            waiver=indexed.get((ref,"vulnerability",item)); applied.append(waiver) if waiver else unwaived.append(item)
        sbom=prefix.with_suffix(".spdx.json"); source=[f"oci-dir:{archive}","--platform","linux/amd64"] if archive else [ref]; run(["syft",*source,"-o","spdx-json"],sbom); packages=json.loads(sbom.read_text()).get("packages",[]); license_issues=[]
        for package in packages:
            expression=package.get("licenseConcluded")
            if not expression or expression in {"NOASSERTION","NONE"}: expression=package.get("licenseDeclared") or "NOASSERTION"
            tokens={x for x in SPDX_TOKEN.findall(expression) if x not in OPERATORS}
            for token in tokens:
                issue=token if token not in {"NOASSERTION","NONE"} else f"{token}:{package.get('name','unknown')}"
                if token in allowed: continue
                waiver=indexed.get((ref,"license",issue)); applied.append(waiver) if waiver else license_issues.append(issue)
        findings = qualify_findings(manifest["release"]["version"], ref, unwaived, license_issues)
        entries.append({"reference":ref,"platforms":["linux/amd64","linux/arm64"],"sbom_sha256":sha(sbom),"vulnerability_report_sha256":sha(vuln),**findings,"applied_waivers":sorted(set(applied))})
    used={item for entry in entries for item in entry["applied_waivers"]}
    unused=set(indexed.values())-used
    if unused: raise ValueError("unused or stale exact-digest waiver is forbidden")
    report={"schema_version":1,"release_version":manifest["release"]["version"],"release_manifest_sha256":sha(a.manifest),"result":"passed","vulnerability_qualification":"passed","license_qualification":"deferred" if manifest["release"]["version"] in {"v1.2.0", "v1.2.1"} else "passed","generated_at":now.replace(microsecond=0).isoformat().replace("+00:00","Z"),"entries":entries,"waivers_sha256":sha(a.waivers),"license_policy_sha256":sha(a.license_policy)}
    a.output.parent.mkdir(parents=True,exist_ok=True); a.output.write_bytes(canonical(report))
if __name__=="__main__": main()
