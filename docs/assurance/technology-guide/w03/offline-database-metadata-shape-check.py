"""W03 operator metadata shape check; cryptographic trust remains separate."""

import copy
import json
from pathlib import Path

from jsonschema import Draft202012Validator

schema = json.loads(Path("deploy/bundles/schemas/offline-database-metadata-v2.schema.json").read_text())
Draft202012Validator.check_schema(schema)
validator = Draft202012Validator(schema)
positive = {
    "schema_version": 2,
    "release": "v1.2.2",
    "repository": "registry.example.test/distribution",
    "digest": "sha256:" + "1" * 64,
    "catalog_digest": "sha256:" + "2" * 64,
    "profile": "builtins",
    "subjects": [{
        "repository": "registry.example.test/distribution",
        "digest": "sha256:" + "1" * 64,
        "signature_digest": "sha256:" + "3" * 64,
    }],
}
validator.validate(positive)
for name in ("missing", "unknown", "wrong-version", "unknown-subject", "empty-subjects"):
    negative = copy.deepcopy(positive)
    if name == "missing":
        del negative["catalog_digest"]
    elif name == "unknown":
        negative["untrusted"] = True
    elif name == "wrong-version":
        negative["schema_version"] = 1
    elif name == "unknown-subject":
        negative["subjects"][0]["extra"] = True
    else:
        negative["subjects"] = []
    if not list(validator.iter_errors(negative)):
        raise AssertionError("negative unexpectedly accepted: " + name)
print("PASS: metadata-v2 schema shape, one positive and five negatives; crypto identity remains independently required")
