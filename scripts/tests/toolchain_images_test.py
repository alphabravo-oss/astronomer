import hashlib
import json
import sys
import unittest
import urllib.error
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import toolchain_images as images


def encoded(value):
    raw = json.dumps(value).encode()
    return raw, 'sha256:' + hashlib.sha256(raw).hexdigest()


class ImageResolutionTests(unittest.TestCase):
    def setUp(self):
        self.config, config_digest = encoded({'created': '2026-10-08T00:00:00Z', 'config': {'Env': ['NGINX_VERSION=1.31.6']}})
        self.manifest, manifest_digest = encoded({'config': {'digest': config_digest}})
        self.index, self.digest = encoded({'manifests': [{'digest': manifest_digest, 'platform': {'os': 'linux', 'architecture': arch}} for arch in ['amd64', 'arm64']]})
        self.requests = []

    def request(self, url, headers):
        self.requests.append(url)
        if '/blobs/' in url:
            return self.config, {}
        if url.endswith('/latest') or url.endswith(self.digest):
            return self.index, {'Docker-Content-Digest': self.digest}
        return self.manifest, {}

    def test_exact_digest_identity_and_all_platform_bytes(self):
        with patch.object(images, 'request', side_effect=self.request):
            result = images.resolve_image('docker.io/library/nginx@' + self.digest)
        self.assertEqual(result['reference'], 'docker.io/library/nginx@' + self.digest)
        self.assertEqual(len([url for url in self.requests if '/manifests/' in url]), 3)

    def test_missing_ambiguous_and_corrupt_platforms(self):
        value = json.loads(self.index)
        for manifests in [value['manifests'][:1], value['manifests'] + [value['manifests'][0]]]:
            self.index, self.digest = encoded({'manifests': manifests})
            with patch.object(images, 'request', side_effect=self.request):
                with self.assertRaisesRegex(ValueError, 'platform'):
                    images.resolve_image('docker.io/library/nginx:latest')
        self.setUp()
        self.manifest = b'corrupt'
        with patch.object(images, 'request', side_effect=self.request):
            with self.assertRaisesRegex(ValueError, 'manifest digest'):
                images.resolve_image('docker.io/library/nginx:latest')

    def test_corrupt_index_and_config(self):
        with patch.object(images, 'request', return_value=(self.index, {'Docker-Content-Digest': 'sha256:' + '0' * 64})):
            with self.assertRaisesRegex(ValueError, 'manifest bytes'):
                images.resolve_image('docker.io/library/nginx:latest')
        self.config = b'corrupt'
        with patch.object(images, 'request', side_effect=self.request):
            with self.assertRaisesRegex(ValueError, 'config digest'):
                images.resolve_image('docker.io/library/nginx:latest')

    def test_unsafe_reference_and_auth_realm(self):
        for ref in ['https://evil/image:1.0', 'docker.io/../image:1.0', 'docker.io/a:tag@sha256:bad', 'docker.io/a:1.0#fragment', 'docker.io/a@sha256:bad']:
            with self.assertRaisesRegex(ValueError, 'invalid OCI'):
                images.resolve_image(ref)
        for realm in ['https:///token', 'https://example.com/token#fragment', 'http://example.com/token', 'https://user:secret@example.com/token']:
            error = urllib.error.HTTPError('https://registry.example', 401, 'auth required', {'WWW-Authenticate': 'Bearer realm="' + realm + '"'}, None)
            with patch.object(images, 'request', side_effect=error):
                with self.assertRaisesRegex(ValueError, 'auth realm'):
                    images.resolve_image('registry.example/image:latest')


if __name__ == '__main__':
    unittest.main()
