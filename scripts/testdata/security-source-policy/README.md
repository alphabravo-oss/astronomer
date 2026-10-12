# Historical source-policy fixtures

`index.json` binds each original commit/path to the SHA-256 of its exact
uncompressed bytes. The compressed blobs were read from the original Git objects
and retain the existing source-review fingerprints. They include source snapshots
from temporary implementation branches that are not ancestors of the release
branch. Tests must work from a fresh checkout without those local branch refs.

The loader bounds decompression and verifies every digest. These are historical
positive/negative test inputs, not current-source security acceptance evidence.
