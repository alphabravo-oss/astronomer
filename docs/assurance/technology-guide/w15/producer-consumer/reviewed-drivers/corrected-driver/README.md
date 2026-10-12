# Empty canonical image list correction

The original run stopped after producer-0 completed because the graph checker required all files to be nonempty. Canonical scripts/build_catalog.py line 155 intentionally emits empty images.txt when there are no auxiliary images. The actual producer report covers 34 files, 21 applications and 17 repositories; all bytes, hashes, modes and release-index/entry digests match.

This successor permits a reported zero-byte file only at the exact relative path images.txt. All other graph, bounds, mode, schema and digest checks remain unchanged. Added controls accept the canonical empty image list and reject reported empty catalog, manifest, entry and unrelated files. Original run and driver remain immutable; no original receipt is relabeled.

No retry is authorized by this packet. Following root review and exclusive lane allocation, the unchanged finite five-step driver can run once with a fresh output:

    python3 -B /tmp/w15-catalog-producer-consumer-empty-images-afd3af6/run_interop.py /tmp/w15-catalog-producer-consumer-afd3af6-run2

It retains the synthetic key/in-memory transport and zero database-write scope. Original receipt: /tmp/w15-catalog-producer-consumer-afd3af6-run1/receipt.json, SHA256 9a5205ecb7a713db5c8dff1c0a5aab7176915d4d425d103015c9a7e7a8e006e1.
