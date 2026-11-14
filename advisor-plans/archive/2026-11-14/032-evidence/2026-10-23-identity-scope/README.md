> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Identity scope recovery — 2026-10-23

The owner reconfirmed: remove Dex and use the shared login system.
The recovered October 20 owner decision already superseded Dex as the shared IdP.

## Evidence and limits

- [Owner decision, including October 21 sign-in modes](owner-identity-plan.md), recovered from `/root/handoff/PLAN-identity-zitadel.md`.
- [Astronomer integration brief](astronomer-integration-brief.md), recovered from `/root/handoff/codex-brief-identity-astronomer.md`: requests `feat/identity-zitadel`, shared login integration and deletion of Dex. This is an instruction, not completion evidence.
- [Shared component release report](identity-release-report.md), recovered from `/root/handoff/exec-identity-repo.md`: records AlphaBravo Identity v0.1.0, commit `3156bfc100abe6b323f0f124a1693e76ebfab666`, signed artifacts and isolated live proof. Local `/root/work/identity` is clean on `main` at that commit and tag. Proof concerns the shared component/demo products, not Astronomer adoption. Release/signature claims are historical report evidence, not reverified in this planning pass.
- [Branch inventory](branch-inventory.json): local main, cached origin/main, realign/2026-10-20, fix/2.1-runtime-verification and implement/033-offering-assurance all retain Dex routes/templates and lack the shared Identity Go dependency.
- `git ls-remote --heads origin '*identity*' '*zitadel*' '*dex*'` returned no matching current remote heads. No local `feat/identity-zitadel`, `/root/work/astronomer-identity`, or `/root/handoff/exec-identity-astronomer.md` was found. This does not prove an unobserved/deleted branch never existed; integration completion remains unverified.
- Commit `2e0513b8c74bbc2db681df5974763eb72fe277d4` retires the **remote upstream Dex installation path** and explicitly retains chart-bundled Dex. It is contained in the inspected Astronomer branches. It is not Dex removal.
- Commit `c87c5f6ff868edf25291f96d9aed630a408e965e` removes a duplicate Settings/General SSO tab, not authentication or Dex.

## Resolution

[Plan 032 §20.13](../../032-technology-guide-alignment-and-latest-stable-master-plan.md#2013-shared-identity-adoption-and-dex-retirement--scope-correction-2026-10-23)
is the active continuation. Dex hardening batches §20.10–20.12 are historical,
superseded work, not release requirements. Their evidence and uncommitted changes
remain preserved for selective disposition; do not merge the accumulated patch
wholesale. No runtime, database, deployment or identity repository was changed in
this planning reconciliation.

The October 21 local/SSO/both modes override older break-glass-only language and
older blanket deletion of local password/lockout features. Keep first-class local
accounts, MFA, policy, product authorization, API tokens and a controlled emergency
admin path; delete replaced broker/federation machinery after replacement proof.

## Later owner clarification (2026-10-23)

Local login is **always available**; SSO is optional. This supersedes SSO-only
mode in the recovered October 21 source snapshots above. Those files remain
historical evidence, not current permission to disable local accounts.
