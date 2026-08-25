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

def waiver_map(document, now):
    if document["schema_version"] != 1 or not isinstance(document["waivers"],list): raise ValueError("invalid waiver document")
    result={}
    exact={"reference","category","ids","reason","approved_by","expires_at"}
    for waiver in document["waivers"]:
        if not isinstance(waiver,dict) or set(waiver)!=exact or not REF.fullmatch(waiver["reference"]): raise ValueError("waiver violates closed exact-reference schema")
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

def main():
    p=argparse.ArgumentParser(); p.add_argument("--manifest",type=Path,required=True); p.add_argument("--waivers",type=Path,required=True); p.add_argument("--license-policy",type=Path,required=True); p.add_argument("--work-dir",type=Path,required=True); p.add_argument("--output",type=Path,required=True); a=p.parse_args()
    manifest=json.loads(a.manifest.read_text()); waivers=load_closed(a.waivers,{"schema_version","waivers"},"waivers"); policy=load_closed(a.license_policy,{"schema_version","allowed_spdx_ids"},"license policy")
    if policy["schema_version"]!=1 or not isinstance(policy["allowed_spdx_ids"],list): raise ValueError("invalid license policy")
    allowed=set(policy["allowed_spdx_ids"]); now=dt.datetime.now(dt.timezone.utc); indexed=waiver_map(waivers,now)
    a.work_dir.mkdir(parents=True,exist_ok=True); entries=[]
    for index,ref in enumerate(references(manifest)):
        prefix=a.work_dir/f"image-{index:03d}"; raw=prefix.with_suffix(".manifest.json"); run(["docker","buildx","imagetools","inspect",ref,"--raw"],raw)
        image_manifest=json.loads(raw.read_text()); platforms=sorted({f"{x.get('platform',{}).get('os','')}/{x.get('platform',{}).get('architecture','')}" for x in image_manifest.get("manifests",[])})
        if not {"linux/amd64","linux/arm64"}.issubset(platforms): raise ValueError(f"image lacks required platforms: {ref}")
        vuln=prefix.with_suffix(".trivy.json"); run(["trivy","image","--quiet","--format","json","--scanners","vuln","--severity","HIGH,CRITICAL","--ignore-unfixed",ref],vuln)
        vuln_ids=sorted({v["VulnerabilityID"] for result in json.loads(vuln.read_text()).get("Results",[]) for v in (result.get("Vulnerabilities") or [])})
        applied=[]; unwaived=[]
        for item in vuln_ids:
            waiver=indexed.get((ref,"vulnerability",item)); applied.append(waiver) if waiver else unwaived.append(item)
        sbom=prefix.with_suffix(".spdx.json"); run(["syft",ref,"-o","spdx-json"],sbom); packages=json.loads(sbom.read_text()).get("packages",[]); license_issues=[]
        for package in packages:
            expression=package.get("licenseConcluded")
            if not expression or expression in {"NOASSERTION","NONE"}: expression=package.get("licenseDeclared") or "NOASSERTION"
            tokens={x for x in SPDX_TOKEN.findall(expression) if x not in OPERATORS}
            for token in tokens:
                issue=token if token not in {"NOASSERTION","NONE"} else f"{token}:{package.get('name','unknown')}"
                if token in allowed: continue
                waiver=indexed.get((ref,"license",issue)); applied.append(waiver) if waiver else license_issues.append(issue)
        if unwaived or license_issues: raise ValueError(f"unwaived policy findings for {ref}: vulnerabilities={unwaived}, licenses={license_issues}")
        entries.append({"reference":ref,"platforms":["linux/amd64","linux/arm64"],"sbom_sha256":sha(sbom),"vulnerability_report_sha256":sha(vuln),"high_critical_unwaived":0,"license_unwaived":0,"applied_waivers":sorted(set(applied))})
    used={item for entry in entries for item in entry["applied_waivers"]}
    unused=set(indexed.values())-used
    if unused: raise ValueError("unused or stale exact-digest waiver is forbidden")
    report={"schema_version":1,"release_version":manifest["release"]["version"],"release_manifest_sha256":sha(a.manifest),"result":"passed","generated_at":now.replace(microsecond=0).isoformat().replace("+00:00","Z"),"entries":entries,"waivers_sha256":sha(a.waivers),"license_policy_sha256":sha(a.license_policy)}
    a.output.parent.mkdir(parents=True,exist_ok=True); a.output.write_bytes(canonical(report))
if __name__=="__main__": main()
