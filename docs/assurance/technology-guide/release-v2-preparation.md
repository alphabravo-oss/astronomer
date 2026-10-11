# Version 2.0.0 release preparation

The release owner requested merge, v2.0.0 publication and local deployment on
2026-10-11, and explicitly carried forward the earlier external certification
and license deferrals. This record is preparation evidence, not proof of a
published release or completed hosted qualification.

- Release, image and chart version surfaces now identify 2.0.0.
- Catalog, transfer, rehearsal and upgrade validators accept exact stable major
  versions; prerelease, mutable and malformed versions remain rejected.
- Release publication policy preserves historical tags and adds exactly v2.0.0.
  Unapproved future tags remain strict. Waiver image/finding sets and expiry are
  unchanged; their approved tag is now v2.0.0.
- Publication still requires signed source-bound toolchain resolution, signed
  upgrade toolkits, image scans/SBOMs/signatures, Kubernetes clean installs and
  a signed upgrade/backup/restore rehearsal.
- The API comparison against v1.2.1 retains the 82 prior exact reviews and adds
  53 deliberate browser UI proxy contract changes for v2.0.0. See the
  [browser isolation review](tool-ui-isolation-review.md). Each review is bound
  to exact source/baseline digests, warning identity, owner and expiry.
- Local release contract, toolchain (54 tests), upgrade/catalog/chart package
  tests and major-version rehearsal harness passed. The harness uses CLI doubles
  and does not establish live restore qualification.
- Local full PR runs exposed a runner.temp expansion failure and stale fixed
  dates in toolchain tests. The workflow now resolves its runner temporary path
  in the shell; test dates cover the current authority and retain future-date
  rejection cases. Those initial runs are not passing PR qualification.

See [migration notes](../../runbooks/upgrade-2.0.md) and
[local CNPG acceptance](../../../advisor-plans/032-evidence/2026-10-11-compact-cnpg.md).
Plan 032 remains in progress. No cloud, production scale, Rancher benchmark,
human accessibility, clean-license or full multi-host HA certification is claimed.
