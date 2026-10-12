#!/usr/bin/env python3
"""Recover the original v2.0.1 blob signature from its public transparency entry."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import subprocess
import urllib.request

TAG = 'v2.0.1'
COMMIT = '9fee3c5ddc59353413c9f5574d6f119e283cc808'
SOURCE_RUN = '38199835448'
MANIFEST_SHA = '88af461a142186fc7e6009c13db1218eb712eaea6ad0f041016b07b50c322ec4'
MANIFEST_REF = 'ghcr.io/alphabravo-oss/astronomer-release-manifest@sha256:3b4e1bf38f43062e813be4df5178883592288eaa0c9626b02b0be90b100f3da9'
LOG_INDEX = 3220778135
IDENTITY = 'https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/v2.0.1'
ISSUER = 'https://token.actions.githubusercontent.com'


def require_release(tag, commit, source_run):
    if (tag, commit, source_run) != (TAG, COMMIT, SOURCE_RUN):
        raise ValueError('recovery is limited to the exact interrupted v2.0.1 release')


def original_bundle(entries, manifest):
    if hashlib.sha256(manifest).hexdigest() != MANIFEST_SHA:
        raise ValueError('original manifest bytes differ')
    document = json.loads(manifest)
    if document['release']['version'] != TAG or document['release']['source_commit'] != COMMIT:
        raise ValueError('original manifest identity differs')
    if not isinstance(entries, dict) or len(entries) != 1:
        raise ValueError('expected one original transparency entry')
    entry = next(iter(entries.values()))
    body = json.loads(base64.b64decode(entry['body'], validate=True))
    if (entry['logIndex'] != LOG_INDEX or body['kind'] != 'hashedrekord'
            or body['spec']['data']['hash'] != {'algorithm': 'sha256', 'value': MANIFEST_SHA}):
        raise ValueError('transparency entry does not bind the original manifest')
    return {'base64Signature': body['spec']['signature']['content'],
            'cert': body['spec']['signature']['publicKey']['content'],
            'rekorBundle': {'SignedEntryTimestamp': entry['verification']['signedEntryTimestamp'],
                            'Payload': {k: entry[k] for k in ('body', 'integratedTime', 'logIndex', 'logID')}}}


def restore(directory):
    manifest = directory / 'release-manifest.json'
    with urllib.request.urlopen(f'https://rekor.sigstore.dev/api/v1/log/entries?logIndex={LOG_INDEX}', timeout=30) as response:
        raw = response.read(1024 * 1024 + 1)
    if len(raw) > 1024 * 1024:
        raise ValueError('oversized transparency entry')
    bundle = directory / 'release-manifest.sigstore.json'
    bundle.write_text(json.dumps(original_bundle(json.loads(raw), manifest.read_bytes()), indent=2) + '\n')
    # This verifies the original signature, certificate identity, issuer and
    # transparency promise. No new manifest signing authority is introduced.
    subprocess.run(['cosign', 'verify-blob', '--bundle', str(bundle),
                    '--certificate-identity', IDENTITY, '--certificate-oidc-issuer', ISSUER,
                    str(manifest)], check=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('tag', 'source-commit', 'source-run-id'):
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--directory', type=Path)
    args = parser.parse_args()
    require_release(args.tag, args.source_commit, args.source_run_id)
    if args.directory is not None:
        restore(args.directory)
