# W04 mirrored physical database authority

The reviewed runtime implementation and tests are applied, but **not compiled or
qualified**. The isolated durable source advances from `4d2ecffa`; the original
clean CA qualifier remains at `9ee9961e3f8a98ad3ee187b24fe667c90cbb9566`. The full
reviewed source proposal remains externally frozen at
`/tmp/plan032-w04-mirror-authority-draft/` with exact original/draft bindings.
The future 079 SQL changes remain untracked source drafts, not generated SQLC
or an executable migration qualification packet. No database, compiler, Cosign
CLI, supplier transfer, cluster operation or publication ran for this change.
Only source formatting and byte-binding verification have executed.

## Actual retained failure

The original sparse CA run is retained at
`/root/.cache/astronomer-032/qualification/w04-pinned-ca-9ee/`. Its missing Flux
input and rejected Go build-event shape are environment/runner failures; no
positive runtime qualification follows. The corrected full checkout run remains
at `/root/.cache/astronomer-032/qualification/w04-pinned-ca-9ee-full/`:

- Full tracked selection: 7,145 present inputs, zero missing; exact clean 9ee.
- Actual Go 1.27.2 and Node 26.11.1. Unchanged-provider compatibility baseline:
  three parents / three cases PASS, zero failures or skips.
- Focused physical-CA race: 16 parents / 37 cases, one failed parent, zero skips.
  `TestPinnedDatabaseAuthorityBindsActualTLSRootsCatalogAndMountedRotation`
  reports that actual pinned TLS and keyed catalog composition failed.
- Full four-package race, publisher package, vet and static/docs/CLI gates were
  not reached. Source unchanged, baseline restored, held owned groups drained;
  all 19 fatal guards passed and final capacity remained 65 GiB / 20%.

The existing positive fixture supplies Restricted network authority but no
verified MirrorPlan. Canonical `projectMirrors` therefore correctly refuses its
unsigned disconnected projection. Inspection also finds that a genuinely
mirrored base would fail the original bare-digest equality check. These are
source diagnoses against the retained failure, not a passing corrected replay.

## Changes and exact ordering

1. `VerifyMirrorPlan` retains the exact original bounded payload/signature,
   signing policy, operator trust-directory input, and immutable parsed fields
   and ordered rewrites in a private proof. `Loader.New` checks all exported
   fields against that proof and captures a fresh document copy. A caller
   cannot edit a verified target, source, manifest binding or release while
   preserving its authenticated identity. Ordinary mirror verification still
   uses the existing canonical ExecVerifier and does not acquire a new global
   platform restriction. Identity() returns provenance data only.
2. Optional `NewDatabaseAuthority` captures the vendor trust as before, captures
   the operator mirror key from that separately verified origin using existing
   `CaptureCosignTrust`, and re-verifies the ORIGINAL endorsement signature
   with canonical ExecVerifier against the frozen operator bytes before private
   Loader construction. Rotation before construction fails the original
   signature; rotation after construction fails current physical revalidation.
   Signature provider, keyRef and origin are never selected by a feed document.
3. Bare parent OCI digest remains unchanged. Expected effective catalog identity
   is constructed exactly from that parent plus the captured verified mirror
   identity. There is no prefix grant or caller-selected composite identity.
   Base provenance retains the effective identity; signed update binding gains
   optional exact `mirror_endorsement_digest`. Unmirrored feed output omits the
   field and retains its previous bytes; mirrored consumers require equality.
4. Physical mirrored authority uses a versioned framed fingerprint containing
   vendor material, exact endorsement identity, separately captured operator
   policy/material/origin, and existing registry TLS roots/policy/origin when
   present. Immutable historical snapshots retain operator fingerprints after
   Close. Current use still requires Revalidate. Both owned signing snapshots
   close on errors and normal Close; idle transport closure is not active-body
   drain. Signing-only and pinned unmirrored fingerprints remain unchanged.
5. Future 079 base rows explicitly bind bare parent, effective identity, mirror
   endorsement and paired operator material/origin fingerprints. Schema checks
   require all mirror fields absent or all valid and exact derived identity.
   Promotion, update high-water acceptance, receipt refresh and current reads
   compare those fields explicitly in addition to the physical binding hash.
   The private repository participant, atomic publication/projection/rescan,
   generated queries, migration, and actual PG 17/18 transactions remain open.

## Genuine fixture controls (unexecuted)

The private-IP TLS registry fixture keeps original signed public chart locations
and immutable subjects. A DIFFERENT local operator QA key signs the exact
rewrites to its approved private origin. All original child signatures remain
vendor-key verified. Its signed values profile now explicitly includes image
repositories alongside existing immutable digests so projection does not infer
unsigned Helm defaults. The scanner remains intentionally disabled.

New tests cover:

- Genuine private TLS + vendor key + different operator endorsement key; exact
  composite catalog/base/feed identity and immutable historical provenance.
- Missing/wrong valid operator key before construction, rotation/removal after
  construction, unsigned/mismatched release/parent endorsement, missing
  endorsement under Restricted policy, wrong feed mirror, and no retrieval on
  constructor/current-trust denial. Original vendor material cannot replace the
  operator key. Both owned trust directories are removed after Close.
- Changes to verified source, target, kind, ordered rewrites, release, parent,
  catalog, manifest or schema reject before network. Caller mutation after
  successful construction cannot change frozen rewrites.
- Actual signed mirrored feed and exact child descriptors through the same
  pinned private-TLS authority; differently mirrored/missing-mirror feeds remain
  denied even with a valid vendor signature. Removing operator trust prevents
  any update retrieval.
- Parser exact mirror equality, omitted unmirrored field, unchanged unmirrored
  physical fingerprint and ordinary catalog identity.

These fixtures prove local keyed consumer composition only if their future
selected-tool race gates pass. They do not prove production OIDC publisher
identity, supplier whole payloads/metadata, enabled scanner operation, measured
DB freshness, report scan-used witness, durable acceptance, or a current combined
candidate. Existing ordinary signature-first/archive/network/child requirements
and Restricted endorsement refusal stay in force.

## Scope and reader ownership

Changed runtime modules: distribution mirror/loader/database base/authority/feed
contract and the existing resolver trust snapshot helper. No new production JSON
serialization/decoding call site is introduced; the existing MirrorPlan closed
reader now captures proof after authentication, and the existing closed update
reader compares an additive optional binding field. The new Identity and framed
fingerprint helpers do not serialize JSON. Current JSON inventory module/site
hashes need exact later owner-coordinated repair after frozen source review and
qualification; there is no blanket inventory refresh in this source slice.
