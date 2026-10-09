#!/usr/bin/env python3
"""Actual QA signing → offline canonical verification → closed extraction."""
import argparse
import importlib.util
import io
import json
import os
import re
from pathlib import Path
import subprocess
import tarfile
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import build_opener, ProxyHandler
from urllib.error import HTTPError
import unittest
from unittest.mock import patch

from distribution_fixture import prepare_layout, sha

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("application_airgap", ROOT / "scripts/application-catalog-airgap.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
SCHEMA_SPEC = importlib.util.spec_from_file_location("application_contract_schema", ROOT / "scripts/compatibility-contract.py")
SCHEMA_VALIDATOR = importlib.util.module_from_spec(SCHEMA_SPEC)
SCHEMA_SPEC.loader.exec_module(SCHEMA_VALIDATOR)


def validate_contract(name, document):
    schema = json.loads((ROOT / "deploy/bundles/schemas" / f"{name}-v1.schema.json").read_bytes())
    SCHEMA_VALIDATOR.validate_schema(document, schema, schema)


class ApplicationCatalogArchiveTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cosign = os.environ.get("COSIGN_BIN")
        if not cls.cosign:
            raise unittest.SkipTest("COSIGN_BIN must select the qualified signing producer")
        cls.workspace = tempfile.TemporaryDirectory(prefix="plan032-application-qa-")
        cls.root = Path(cls.workspace.name)
        cls.verifier = cls.root / "distribution-verifier"
        subprocess.run(["go", "build", "-o", str(cls.verifier), "./deploy/bundles/cmd/distribution"], cwd=ROOT,
                       env=os.environ | {"GOTOOLCHAIN": "go1.27.2", "GOMAXPROCS": "4", "GOFLAGS": "-p=2"}, check=True,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    @classmethod
    def tearDownClass(cls):
        cls.workspace.cleanup()

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(dir=self.root)
        self.work = Path(self.temporary.name)
        prepare_layout(self.work)
        self.public_requests = 0
        owner = self
        class PublicTrap(BaseHTTPRequestHandler):
            def do_GET(self):
                owner.public_requests += 1
                self.send_response(503)
                self.end_headers()
            do_CONNECT = do_GET
            def log_message(self, *args):
                pass
        self.trap = ThreadingHTTPServer(("127.0.0.1", 0), PublicTrap)
        threading.Thread(target=self.trap.serve_forever, daemon=True).start()
        self.addCleanup(self.trap.server_close)
        self.addCleanup(self.trap.shutdown)
        proxy = f"http://127.0.0.1:{self.trap.server_port}"
        self.environment = patch.dict(os.environ, {"HOME": str(self.work), "DOCKER_CONFIG": str(self.work),
                                                   "COSIGN_PASSWORD": os.urandom(24).hex(),
                                                   "HTTP_PROXY": proxy, "HTTPS_PROXY": proxy,
                                                   "ALL_PROXY": proxy, "NO_PROXY": "localhost,127.0.0.1",
                                                   "http_proxy": proxy, "https_proxy": proxy, "all_proxy": proxy, "no_proxy": "localhost,127.0.0.1"})
        self.environment.start()
        self.addCleanup(self.environment.stop)
        try:
            build_opener(ProxyHandler({"http": proxy})).open("http://public.qa.example.test/negative-control", timeout=2)
            self.fail("public negative control unexpectedly succeeded")
        except HTTPError as error:
            error.close()
        self.assertEqual(self.public_requests, 1)
        self.public_requests = 0
        self.addCleanup(self.temporary.cleanup)
        subprocess.run([self.cosign, "generate-key-pair", "--output-key-prefix", str(self.work / "vendor")], check=True,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        reviewed = json.loads((self.work / "reviewed-catalog.json").read_text())
        component = reviewed["components"][0]
        catalog = {"apiVersion": "catalog.astronomer.dev/v1alpha1", "kind": "ApplicationCatalog",
                   "metadata": {"name": "local-qa", "schemaVersion": 1, "minimumReaderVersion": 1},
                   "repositories": [{"name": "qa", "type": "helm", "url": "https://charts.qa.example.test/reviewed"}],
                   "applications": [{"slug": component["slug"], "name": "QA application", "category": "security",
                                     "supportTier": "qa", "artifact": {"type": "helm", "repository": "qa",
                                     "chart": component["source"]["chart"], "version": component["source"]["version"]},
                                     "lifecycle": {"install": True, "uninstall": True}}]}
        self.catalog = self.work / "application-catalog.json"
        self.catalog.write_bytes(MODULE.encoded(catalog))
        self.profile = self.work / "profile-index.json"
        self.profile.write_bytes(MODULE.encoded({"schema_version": 1, "catalog_digest": sha(self.catalog.read_bytes()),
                    "profiles": [{"application": component["slug"], "profile": "default", "archive": str(self.work / "charts" / (component["slug"] + ".tgz")),
                                  "archive_digest": component["source"]["chart_digest"], "values": component["values"],
                                  "images": component["images"], "namespace": "application-qa", "kubernetes_version": "1.35.0"}]}))
        self.output, self.bom, self.signature = self.work / "catalog.tgz", self.work / "transfer.json", self.work / "transfer.sigstore.json"
        self.export_args = argparse.Namespace(catalog=self.catalog, profile_index=self.profile, platform_evidence=self.work / "platforms",
                           output=self.output, bom=self.bom, signature=self.signature, verifier_bin=self.verifier, cosign_bin=Path(self.cosign),
                           release="v1.2.1", publisher_mode="qa", signing_key=self.work / "vendor.key")
        self.import_args = argparse.Namespace(archive=self.output, bom=self.bom, signature=self.signature, destination=self.work / "imported",
                           verifier_bin=self.verifier, trust_directory=self.work, signature_provider="cosign_key", key_ref="vendor",
                           certificate_identity=None, certificate_oidc_issuer=None)

    def tearDown(self):
        self.assertEqual(self.public_requests, 0, "signing/verification attempted a public service")

    def sign_transfer(self):
        subprocess.run([self.cosign, "sign-blob", "--yes", "--use-signing-config=false", "--tlog-upload=false", "--key", str(self.work / "vendor.key"),
                        "--bundle", str(self.signature), str(self.bom)], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def test_actual_sign_verify_import_preserves_publisher_bytes(self):
        MODULE.export(self.export_args)
        validate_contract("application-profile-input", json.loads(self.profile.read_bytes()))
        validate_contract("application-transfer", json.loads(self.bom.read_bytes()))
        report = json.loads(subprocess.run([str(self.verifier), "--application-profiles", str(self.profile),
                            "--catalog", str(self.catalog), "--platform-evidence", str(self.work / "platforms")],
                           check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout)
        validate_contract("application-profile-report", report)
        validate_contract("application-import-status", {"verification": "verified", "inventory": "complete_for_reviewed_profiles",
                           "runtime_qualification": "unavailable_until_signed_local_OCI_catalog_and_child_subjects_qualified"})
        MODULE.import_archive(self.import_args)
        extracted = self.import_args.destination
        self.assertEqual((extracted / "catalog.json").read_bytes(), self.catalog.read_bytes())
        self.assertEqual((extracted / "transfer.json").read_bytes(), self.bom.read_bytes())
        self.assertEqual((extracted / "transfer.sigstore.json").read_bytes(), self.signature.read_bytes())
        self.assertTrue(list(extracted.glob("profiles/*.values.json")))
        self.assertTrue(list(extracted.glob("profiles/*.images.json")))

    def test_contract_schemas_reject_unknown_missing_and_wrong_type_fields(self):
        MODULE.export(self.export_args)
        documents = {"application-profile-input": json.loads(self.profile.read_bytes()),
                     "application-transfer": json.loads(self.bom.read_bytes()),
                     "application-profile-report": json.loads(subprocess.run([str(self.verifier), "--application-profiles", str(self.profile),
                            "--catalog", str(self.catalog), "--platform-evidence", str(self.work / "platforms")],
                           check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout),
                     "application-import-status": {"verification": "verified", "inventory": "complete_for_reviewed_profiles",
                           "runtime_qualification": "unavailable_until_signed_local_OCI_catalog_and_child_subjects_qualified"}}
        for name, original in documents.items():
            for mutation in (lambda doc: doc.update(unknown=True), lambda doc: doc.pop(next(iter(doc))),
                             lambda doc: doc.update({next(iter(doc)): []})):
                with self.subTest(schema=name, mutation=mutation):
                    document = json.loads(MODULE.encoded(original))
                    mutation(document)
                    with self.assertRaises(SCHEMA_VALIDATOR.ContractError):
                        validate_contract(name, document)

    def test_authentic_transfer_rejects_unknown_and_missing_contract_fields(self):
        MODULE.export(self.export_args)
        original = json.loads(self.bom.read_bytes())
        for mutation in (lambda doc: doc.update(unknown=True), lambda doc: doc.pop("bom_digest"),
                         lambda doc: doc.update(schema_version=2)):
            transfer = json.loads(MODULE.encoded(original))
            mutation(transfer)
            self.bom.write_bytes(MODULE.encoded(transfer))
            self.sign_transfer()
            with self.assertRaises(MODULE.CatalogArchiveError):
                MODULE.import_archive(self.import_args)
            self.assertFalse(self.import_args.destination.exists())

    def test_invalid_signature_rejected_before_archive_open_or_destination_creation(self):
        MODULE.export(self.export_args)
        self.signature.write_bytes(b"invalid signing evidence")
        self.output.write_bytes(b"invalid gzip bytes")
        with self.assertRaises(MODULE.CatalogArchiveError):
            MODULE.import_archive(self.import_args)
        self.assertFalse(self.import_args.destination.exists())

    def test_wrong_mounted_vendor_key_and_missing_keyless_evidence_fail(self):
        MODULE.export(self.export_args)
        self.import_args.key_ref = "absent"
        with self.assertRaises(MODULE.CatalogArchiveError):
            MODULE.import_archive(self.import_args)
        self.import_args.signature_provider, self.import_args.certificate_identity, self.import_args.certificate_oidc_issuer = "cosign_keyless", "wrong publisher", "https://issuer.example"
        with self.assertRaises(MODULE.CatalogArchiveError):
            MODULE.import_archive(self.import_args)
        self.assertFalse(self.import_args.destination.exists())

    def test_authentic_parent_rejects_extra_duplicate_and_link_archive_members(self):
        MODULE.export(self.export_args)
        original = self.output.read_bytes()
        for kind, name in ((tarfile.REGTYPE, "extra"), (tarfile.REGTYPE, "catalog.json"), (tarfile.SYMTYPE, "link"), (tarfile.REGTYPE, "../outside")):
            with self.subTest(kind=kind, name=name):
                with tarfile.open(fileobj=io.BytesIO(original), mode="r:gz") as source:
                    entries = [(member, source.extractfile(member).read()) for member in source.getmembers()]
                buffer = io.BytesIO()
                with tarfile.open(fileobj=buffer, mode="w:gz", format=tarfile.USTAR_FORMAT) as target:
                    for member, data in entries:
                        target.addfile(member, io.BytesIO(data))
                    member = tarfile.TarInfo(name)
                    member.type, member.linkname = kind, "../../outside"
                    data = b"extra" if kind == tarfile.REGTYPE else b""
                    member.size = len(data)
                    target.addfile(member, io.BytesIO(data))
                self.output.write_bytes(buffer.getvalue())
                transfer = json.loads(self.bom.read_bytes())
                transfer["archive_digest"] = sha(self.output.read_bytes())
                self.bom.write_bytes(MODULE.encoded(transfer))
                self.sign_transfer()  # negative has authentic signing evidence and correct archive SHA
                with self.assertRaises(MODULE.CatalogArchiveError):
                    MODULE.import_archive(self.import_args)
                self.assertFalse(self.import_args.destination.exists())

    def test_export_fails_mutable_render_missing_profile_and_changed_archive(self):
        original = json.loads(self.profile.read_bytes())
        for mutate in (lambda doc: doc["profiles"][0]["values"].update(image="mutable:latest"),
                       lambda doc: doc.update(profiles=[]),
                       lambda doc: doc["profiles"][0].update(archive_digest="sha256:" + "a" * 64)):
            document = json.loads(MODULE.encoded(original))
            mutate(document)
            self.profile.write_bytes(MODULE.encoded(document))
            with self.assertRaises(MODULE.CatalogArchiveError):
                MODULE.export(self.export_args)
            self.assertFalse(self.output.exists())

    def test_actual_runbook_snippet_uses_canonical_current_release_contract(self):
        from release_manifest_test import ReleaseManifestTest, GENERATOR
        fixture = ReleaseManifestTest()
        fixture.setUp()
        self.addCleanup(fixture.tearDown)
        document = GENERATOR.build(fixture.args())
        self.bom = self.work / "release-manifest.json"
        snippet = re.search(r"<!-- verified-release-contract:start -->\n```bash\n(.*?)\n```", (ROOT / "docs/airgapped-install.md").read_text(), re.S).group(1)
        environment = os.environ | {"DISTRIBUTION_VERIFIER": str(self.verifier), "RELEASE_MANIFEST": str(self.bom),
                     "RELEASE_SIGNATURE": str(self.signature), "SIGSTORE_TRUST_DIRECTORY": str(self.work),
                     "RELEASE_SIGNATURE_PROVIDER": "cosign_key", "RELEASE_KEY_REF": "vendor",
                     "RELEASE_CERTIFICATE_IDENTITY": "", "RELEASE_CERTIFICATE_OIDC_ISSUER": "",
                     "ASTRONOMER_RELEASE": document["release"]["version"]}
        mutations = [None, lambda doc: doc["release"].update(install_mode="fresh_only"),
                     lambda doc: doc["astronomer"].update(images=[image for image in doc["astronomer"]["images"] if image["name"] != "dr"]),
                     lambda doc: doc["astronomer"]["images"].append(doc["astronomer"]["images"][0]),
                     lambda doc: doc.update(unknown=True)]
        for mutation in mutations:
            value = json.loads(MODULE.encoded(document))
            if mutation:
                mutation(value)
            self.bom.write_bytes(GENERATOR.encode(value))
            self.sign_transfer()  # every malformed contract has authentic QA signing evidence
            result = subprocess.run(["bash", "-c", snippet], env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.assertEqual(result.returncode == 0, mutation is None)
        environment["ASTRONOMER_RELEASE"] = "v1.2.999"
        self.bom.write_bytes(GENERATOR.encode(document)); self.sign_transfer()
        self.assertNotEqual(subprocess.run(["bash", "-c", snippet], env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE).returncode, 0)


if __name__ == "__main__":
    unittest.main()
