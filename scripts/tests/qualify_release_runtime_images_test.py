import datetime as dt
import importlib.util
import hashlib
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location("runtime_qualifier",ROOT/"scripts/qualify-release-runtime-images.py")
module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
REF="registry.example.test/team/image@sha256:"+"a"*64

class RuntimeQualifierTest(unittest.TestCase):
    def oci_fixture(self, root, arches=("amd64", "arm64")):
        blobs = root / "blobs" / "sha256"
        blobs.mkdir(parents=True)
        def write(value):
            content = value if isinstance(value, bytes) else module.canonical(value)
            digest = hashlib.sha256(content).hexdigest()
            (blobs / digest).write_bytes(content)
            return {"digest": "sha256:" + digest, "size": len(content)}
        layer = write(b"verified image layer")
        manifests = []
        for arch in arches:
            config = write({"architecture": arch, "os": "linux"})
            descriptor = write({"schemaVersion": 2, "config": config, "layers": [layer]})
            descriptor.update(platform={"os": "linux", "architecture": arch}, mediaType="application/vnd.oci.image.manifest.v1+json")
            manifests.append(descriptor)
        index = write({"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": manifests})
        (root / "oci-layout").write_text('{"imageLayoutVersion":"1.0.0"}')
        (root / "index.json").write_text(json.dumps({"schemaVersion": 2, "manifests": [{"digest": "sha256:" + "f" * 64}, index]}))
        return "registry.example.test/charlie@" + index["digest"], blobs / layer["digest"].split(":")[1]

    def test_oci_source_binds_root_and_removes_alternate_scanner_candidates(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); ref, _ = self.oci_fixture(root)
            raw = module.prepare_oci_source(root, ref)
            self.assertEqual("sha256:" + hashlib.sha256(raw).hexdigest(), ref.split("@")[1])
            candidates = json.loads((root / "index.json").read_text())["manifests"]
            self.assertEqual(len(candidates), 1)
            authenticated = json.loads(raw)["manifests"]
            self.assertIn(candidates[0], authenticated)
            self.assertEqual(candidates[0]["platform"], {"os": "linux", "architecture": "amd64"})

    def test_oci_source_rejects_changed_or_missing_layers(self):
        for content in (b"tampered", None):
            with self.subTest(content=content), tempfile.TemporaryDirectory() as directory:
                root = Path(directory); ref, layer = self.oci_fixture(root)
                layer.write_bytes(content) if content is not None else layer.unlink()
                with self.assertRaises((ValueError, FileNotFoundError)):
                    module.prepare_oci_source(root, ref)

    def test_oci_source_rejects_unbound_root_and_missing_platform(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); ref, _ = self.oci_fixture(root, ("amd64",))
            with self.assertRaisesRegex(ValueError, "required platforms"):
                module.prepare_oci_source(root, ref)
            with self.assertRaises(FileNotFoundError):
                module.prepare_oci_source(root, ref.split("@")[0] + "@sha256:" + "0" * 64)

    def test_oci_source_rejects_symlinked_index_and_layer(self):
        for target in ("index", "layer"):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as directory:
                root = Path(directory); ref, layer = self.oci_fixture(root)
                original = root / "index.json" if target == "index" else layer
                moved = root / "original"; original.rename(moved); original.symlink_to(moved)
                with self.assertRaisesRegex(ValueError, "unsafe OCI"):
                    module.prepare_oci_source(root, ref)

    def test_archive_scans_keep_the_signed_reference_in_the_report(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); layout = root / "oci"; ref, _ = self.oci_fixture(layout)
            manifest = json.loads(json.dumps(self.manifest()).replace(REF, ref))
            manifest["release"] = {"version": "v1.2.0"}
            documents = {"manifest": manifest, "waivers": {"schema_version": 1, "waivers": []}, "license-policy": {"schema_version": 1, "allowed_spdx_ids": ["MIT"]}}
            args = ["qualify-release-runtime-images.py", "--charlie-oci-layout", str(layout)]
            for name, value in documents.items():
                path = root / f"{name}.json"; path.write_text(json.dumps(value)); args.extend([f"--{name}", str(path)])
            output = root / "report.json"
            args.extend(["--work-dir", str(root / "evidence"), "--output", str(output)])
            commands = []
            def fake_run(command, destination):
                commands.append(command)
                self.assertIn(command[0], ("trivy", "syft"))
                destination.write_text(json.dumps({"Results": [], "packages": []}))
            with patch("sys.argv", args), patch.object(module, "run", fake_run):
                module.main()
            self.assertIn("--input", commands[0]); self.assertIn(str(layout), commands[0])
            self.assertIn(f"oci-dir:{layout}", commands[1])
            self.assertEqual(json.loads(output.read_text())["entries"][0]["reference"], ref)

    def manifest(self):
        artifact={"kind":"container_image","reference":REF}
        return {"astronomer":{"images":[{"reference":REF}],"runtime_images":[{"reference":REF}]},"flux":{"controllers":[{"reference":REF}]},"built_in_bundles":{"components":[{"images":[REF]}]},"charlie":{"artifact":artifact}}

    def test_transitive_references_are_exact_and_deduplicated(self):
        self.assertEqual(module.references(self.manifest()),[REF])

    def test_mutable_transitive_reference_fails(self):
        value=self.manifest(); value["built_in_bundles"]["components"][0]["images"]=["registry.example.test/image:latest"]
        with self.assertRaises(ValueError): module.references(value)

    def test_waiver_is_exact_digest_scoped_and_expiring(self):
        waiver={"reference":REF,"category":"vulnerability","ids":["CVE-2099-1"],"reason":"reviewed","approved_by":"security","expires_at":"2099-01-01T00:00:00Z"}
        got=module.waiver_map({"schema_version":1,"waivers":[waiver]},dt.datetime(2026,1,1,tzinfo=dt.timezone.utc))
        self.assertIn((REF,"vulnerability","CVE-2099-1"),got)
        waiver["expires_at"]="2020-01-01T00:00:00Z"
        with self.assertRaises(ValueError): module.waiver_map({"schema_version":1,"waivers":[waiver]},dt.datetime(2026,1,1,tzinfo=dt.timezone.utc))

    def test_v120_retains_license_findings_without_claiming_qualification(self):
        result = module.qualify_findings("v1.2.0", REF, [], ["GPL-2.0-only", "NOASSERTION:busybox"])
        self.assertEqual(result["license_unwaived"], 2)
        self.assertEqual(result["license_findings"], ["GPL-2.0-only", "NOASSERTION:busybox"])
        self.assertEqual(result["license_qualification"], "pending_review")

    def test_v121_defers_licenses_but_requires_vulnerability_waivers(self):
        result = module.qualify_findings("v1.2.1", REF, [], ["GPL-2.0-only"])
        self.assertEqual(result["license_qualification"], "pending_review")
        with self.assertRaises(ValueError):
            module.qualify_findings("v1.2.1", REF, ["CVE-2099-1"], [])

    def test_release_scoped_waiver_cannot_carry_to_another_release(self):
        waiver={"reference":REF,"category":"vulnerability","ids":["CVE-2099-1"],"reason":"owner accepted upstream risk","approved_by":"release owner","expires_at":"2099-01-01T00:00:00Z","release_version":"v1.2.0"}
        document={"schema_version":1,"waivers":[waiver]}
        now=dt.datetime(2026,1,1,tzinfo=dt.timezone.utc)
        accepted=module.waiver_map(document,now,"v1.2.0")
        self.assertIn((REF,"vulnerability","CVE-2099-1"),accepted)
        self.assertNotIn((REF.replace("a"*64,"b"*64),"vulnerability","CVE-2099-1"),accepted)
        self.assertNotIn((REF,"vulnerability","CVE-2099-2"),accepted)
        for version in (None,"v1.2.1","v2.0.0"):
            with self.subTest(version=version), self.assertRaisesRegex(ValueError,"release version"):
                module.waiver_map(document,now,version)

    def test_v120_report_retains_findings_and_discloses_deferred_license_status(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest = self.manifest()
            manifest["release"] = {"version": "v1.2.0"}
            documents = {"manifest": manifest, "waivers": {"schema_version": 1, "waivers": []}, "license-policy": {"schema_version": 1, "allowed_spdx_ids": ["MIT"]}}
            args = ["qualify-release-runtime-images.py"]
            for key, value in documents.items():
                path = root / f"{key}.json"
                path.write_text(json.dumps(value))
                args.extend([f"--{key}", str(path)])
            output = root / "report.json"
            args.extend(["--work-dir", str(root / "evidence"), "--output", str(output)])
            def fake_run(command, destination):
                bodies = {
                    "docker": {"manifests": [{"platform": {"os": "linux", "architecture": arch}} for arch in ("amd64", "arm64")]},
                    "trivy": {"Results": []},
                    "syft": {"packages": [{"name": "busybox", "licenseConcluded": "NOASSERTION", "licenseDeclared": "NOASSERTION"}]},
                }
                destination.write_text(json.dumps(bodies[command[0]]))
            with patch("sys.argv", args), patch.object(module, "run", fake_run):
                module.main()
            report = json.loads(output.read_text())
            self.assertEqual(report["license_qualification"], "deferred")
            self.assertEqual(report["vulnerability_qualification"], "passed")
            self.assertEqual(report["entries"][0]["license_findings"], ["NOASSERTION:busybox"])
            self.assertEqual(report["entries"][0]["license_unwaived"], 1)

    def test_v120_never_defers_vulnerabilities(self):
        with self.assertRaises(ValueError):
            module.qualify_findings("v1.2.0", REF, ["CVE-2099-1"], ["NOASSERTION:busybox"])

    def test_other_versions_keep_license_qualification_mandatory(self):
        for version in ("v1.1.0", "v1.2.2", "v1.3.0", "v2.0.0"):
            with self.subTest(version=version), self.assertRaises(ValueError):
                module.qualify_findings(version, REF, [], ["GPL-2.0-only"])

if __name__=="__main__": unittest.main()
