import datetime as dt
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location("runtime_qualifier",ROOT/"scripts/qualify-release-runtime-images.py")
module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
REF="registry.example.test/team/image@sha256:"+"a"*64

class RuntimeQualifierTest(unittest.TestCase):
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

if __name__=="__main__": unittest.main()
