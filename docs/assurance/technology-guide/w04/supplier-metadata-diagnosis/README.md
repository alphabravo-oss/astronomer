# Supplier metadata capture diagnosis

On 2026-10-10 at 03:59:52 UTC, the approved metadata-only observer built against unchanged b1befe42 and reproduced both capture failures. The build passed in 5.778 seconds; the network operation failed in 3.022 seconds. All three maintained capacity checks passed. The before/after source snapshots match. This is failed capture evidence, not database freshness, full payload availability, scan-used provenance or release qualification.

The original whole-transfer attempt is retained as `original-receipt.json` and `original-transfer.log`: its build passed, but capture failed before any config or payload fetch. The diagnostic observer then retained only the two immutable manifest responses, each bounded by 1 MiB. Their original bytes hash to the response and resolved subject digests:

| Role | Original manifest SHA256 | Bytes | Declared compressed layer bytes |
| --- | --- | ---: | ---: |
| Main | 361d77189ec82c0b39f7c0fad4d2dc7f1aa4dfd48c24511e27ebe1520f677875 | 616 | 126468206 |
| Java | 7657995935bf8d94bf1b06252d4c62538a751e96633c54613bfdf292eea68a54 | 624 | 980963728 |

Both manifests use an inline OCI empty configuration: media type `application/vnd.oci.empty.v1+json`, size 2, SHA256 `44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a`, and base64 `e30=` representing the two-byte `{}` object. The unchanged `fetchArtifactMetadata` rejects every nonempty `config.Data` before fetching a configuration. That exact source check explains the reproduced failure. The manifest remains the immutable original; removing its inline field would change the subject and is not an acceptable repair.

Each role made HEAD 401, token GET 200, HEAD 200 and immutable manifest GET 200. No config or database layer was requested. No token response, credential, signed redirect location/query, raw remote error or database payload is retained here. Public manifest bytes are retained unchanged as untrusted supplier data. The scanner digest/version in the observer describes constructor compatibility intent, not a measured scanner or scan-used witness. Upstream creation annotations are not full database metadata freshness measurements.

The root-only executable remains in the private qualification directory identified by the receipt, with its recorded SHA256. The tracked observer and driver reproduce the metadata-only operation, but execution requires a separately coordinated compiler/network lease and maintained capacity checks. Do not run the tracked driver automatically: its executable source paths intentionally point to the original reviewed private draft and frozen worktree.

`bindings.json` binds the retained raw files; `check.py` validates those hashes, the Git tree and all 50 frozen runtime source inputs, failed operation status, original response/descriptor claims and conservative scope flags. It checks evidence consistency, not supplier acceptance. Run `python3 docs/assurance/technology-guide/w04/supplier-metadata-diagnosis/check.py --self-test` for one positive and targeted negative assertions. The subsequent inline-configuration correction is a separate source review and has no qualification in this packet.
