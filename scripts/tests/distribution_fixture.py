"""Local QA preparation inputs. These bytes never claim vendor qualification."""
import gzip
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[2]


def sha(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def encode(value):
    return json.dumps(value, separators=(",", ":")).encode()


def prepare_layout(root: Path, repository="qa.example.test/distribution") -> Path:
    charts = root / "charts"
    platforms = root / "platforms"
    charts.mkdir()
    platforms.mkdir()
    descriptors = []
    for architecture in ("amd64", "arm64"):
        config = encode({"architecture": architecture, "os": "linux", "config": {}, "rootfs": {"type": "layers", "diff_ids": []}})
        manifest = encode({"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
                           "config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": sha(config), "size": len(config)},
                           "layers": [{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": sha(b"qa fixture layer"), "size": 16}]})
        for content in (config, manifest):
            (platforms / (sha(content).removeprefix("sha256:") + ".json")).write_bytes(content)
        descriptors.append({"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": sha(manifest), "size": len(manifest),
                            "platform": {"os": "linux", "architecture": architecture}})
    index = encode({"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": descriptors})
    (platforms / (sha(index).removeprefix("sha256:") + ".json")).write_bytes(index)
    image = "qa.example.test/workload@" + sha(index)
    catalog = json.loads((ROOT / "deploy/bundles/catalog.json").read_text())
    for component in catalog["components"]:
        component["images"] = [image]
        component["values"] = {"image": image, "reviewedQAProfile": True}
        component["source"]["url"] = "https://charts.qa.example.test/reviewed"
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode="w", format=tarfile.USTAR_FORMAT) as archive:
            files = {"Chart.yaml": f"apiVersion: v2\nname: {component['source']['chart']}\nversion: {component['source']['version']}\n",
                     "values.yaml": "qaDefault: classified\n",
                     "templates/pod.yaml": "apiVersion: v1\nkind: Pod\nspec:\n  initContainers:\n  - name: init\n    image: {{ .Values.image }}\n  containers:\n  - name: main\n    image: {{ .Values.image }}\n"}
            for name, body in sorted(files.items()):
                data = body.encode()
                member = tarfile.TarInfo(component["source"]["chart"] + "/" + name)
                member.mode = 0o600
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
        content = gzip.compress(buffer.getvalue(), mtime=0)
        component["source"]["chart_digest"] = sha(content)
        (charts / (component["slug"] + ".tgz")).write_bytes(content)
    catalog_path = root / "reviewed-catalog.json"
    catalog_path.write_bytes(encode(catalog))
    assets = root / "assets.json"
    assets.write_bytes(encode({"schema_version": 1, "required_categories": ["catalog", "chart", "values", "image_inventory"], "assets": []}))
    layout = root / "layout"
    result = subprocess.run(["bash", "scripts/build-builtin-bundles.sh", "--v2", "--charts-directory", str(charts),
                             "--asset-index", str(assets), "--platform-evidence", str(platforms), "--repository", repository,
                             "--layout-output", str(layout)], cwd=ROOT, env=__import__('os').environ | {"BUNDLE_CATALOG": str(catalog_path)},
                            check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    metadata = json.loads(result.stdout)
    assert metadata["catalog_digest"] == sha((layout / "catalog.json").read_bytes())
    return layout
