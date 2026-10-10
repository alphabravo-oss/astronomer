import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]

class UpgradeToolkitPublicationContract(unittest.TestCase):
    def test_both_emitted_archives_are_preserved_and_explicitly_published(self):
        workflow = (ROOT / '.github/workflows/release.yaml').read_text()
        build = (ROOT / 'scripts/build-upgrade-toolkit.sh').read_text()
        self.assertIn('mv -- "$directory/kit/astronomer-upgrade-${target}-linux-${architecture}.tar.gz" "$output/assets/"', build)
        for architecture in ('amd64', 'arm64'):
            self.assertIn('-name "astronomer-upgrade-${TAG}-linux-' + architecture + '.tar.gz"', workflow)
        self.assertIn('archive="astronomer-upgrade-${TAG}-linux-${architecture}.tar.gz"', workflow)
        self.assertIn('test -s "$archive"', workflow)
        self.assertIn('upgrade_toolkit_assets+=("$archive" "$stem"', workflow)
        self.assertIn('"${upgrade_toolkit_assets[@]}"', workflow)
        self.assertIn('"${release_tarballs[@]}"', workflow)
        self.assertNotIn('astronomer-*.tgz *.tar.gz sbom-', workflow)

    def test_historical_release_gates_and_signed_toolkit_members_remain(self):
        workflow = (ROOT / '.github/workflows/release.yaml').read_text()
        self.assertIn("if: github.ref_name != 'v1.2.0' && github.ref_name != 'v1.2.1'", workflow)
        self.assertIn("needs.rc-rehearsal.result == 'success'", workflow)
        self.assertIn("needs.publish-upgrade-toolkit.result == 'success'", workflow)
        self.assertIn('"${stem}.sigstore.json"', workflow)
        self.assertIn('"${stem}.installer.sigstore.json"', workflow)
        self.assertIn('name: upgrade-toolkit-${{ needs.preflight.outputs.image-tag }}', workflow)
