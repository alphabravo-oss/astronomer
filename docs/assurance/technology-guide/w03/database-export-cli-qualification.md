# Streamed database exporter and operator CLI qualification

Immutable source `c6381144f13f451fe8508a1bb7e4270ec2da3879` passed the seven full
distribution/producer/consumer/network/bundle/release/astro package race suites
with selected Cosign 3.1.3 and Go 1.27.2: 168 parent tests and 656 cases passed,
with zero failures or skips. The [closed consistency receipt](database-export-cli-evidence.json)
binds 544 exact source inputs, the Git tree, unchanged before/after snapshots,
selected producer bytes, exact invocations/environments and all retained logs.
It is unsigned local test evidence, not an authoritative release qualification.

All four selected producer/consumer fixtures executed. The descriptor publisher
case includes the complete streamed writer/readback, offline verify/import,
metadata-only registry capture with no DB layer refetch, preserved default-v1
verification, explicit format/action refusal and exact known unsigned chart child
negative. Export failure cases leave no accepted output or owned staging files;
same-user replacement of an owned temporary path is preserved and rejected.
This does not claim a full network CLI export invocation: its canonical client
factory is tested independently, while the actual signed capture-to-writer and
offline CLI verify/import paths are composed in the retained fixture.

Vet, Node 26.11.1 docs, CLI documentation and the entire release contract target
passed. Docs use read-only modules from the W07 worktree after confirming byte
equality of package.json and package-lock.json; no install, symlink or cache writer
was introduced. Release checks executed 64 Python fixtures, artifact shell tests,
chart image comparison and their Go selectors. The release target's
internal/releasecontract selector had no matching tests; that entire package
separately executed in the full race suite (49.138 seconds). This distinction is
retained rather than counting an empty selector as test coverage.

The maintained capacity guard passed before each gate and at closure, with
65 GiB available and 20% free at the end. All compiler/runtime subprocesses
closed before the window was handed back. The evidence checker executes one
positive and eleven negative schema/source/tree/log/counter/tool/authority
assertions. Its ambient Python runtime is receipt tooling, not W15 latest-Python
qualification.

Current supplier whole-layer hashes, DB metadata/freshness, production OIDC
publication/startup, live registry import/readback and fresh-cache/no-egress
clusters remain unqualified. No future tag or release was approved. W03 remains
in progress; the larger contract/input/kit versions do not inherit historical
artifact or small QA payload qualification.
