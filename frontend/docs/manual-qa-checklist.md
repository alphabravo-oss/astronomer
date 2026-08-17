# P7.3 — Manual QA checklist (Vite + TanStack migration)

Execute once on the P6.3 k3d deployment (`make docker-build-frontend k3d-import-all helm-install`)
by whoever merges the migration PR, and record the filled-in checklist in the PR.
Every failure here is a release blocker, not a follow-up. Anything found that is
automatable gets a regression test in the appropriate tier (unit / e2e-mock /
route-smoke / live) **before** its fix merges.

The companion screenshot gallery (route-smoke crawl with `SMOKE_GALLERY=1`,
uploaded as the `smoke-gallery` CI artifact) is reviewed alongside this list:
eyeball all route thumbnails once for layout/theme breakage.

## Auth

- [ ] Local login succeeds and lands on the dashboard
- [ ] One SSO redirect round-trips (provider login → back into the app, session established)
- [ ] MFA lockout: repeated bad TOTP codes surface the 423 locked state in the UI
- [ ] Forgot password → reset email flow → new password logs in
- [ ] `must_change_password` user is kicked into the change-password screen and cannot navigate away
- [ ] Unauthenticated deep link → login → returns to the original deep link (`returnTo`)
- [ ] Logout clears the session; guarded pages bounce back to login

## Terminals (@wterm WASM through the gateway)

- [ ] Cluster shell opens and accepts input
- [ ] Pod exec opens and accepts input
- [ ] Logs follow streams live output
- [ ] Window-manager keeps terminal sessions alive across navigation
- [ ] Minimize / restore works
- [ ] LRU eviction kicks in at the session cap (oldest window evicted)

## Editors

- [ ] Monaco lazy-loads on the template editor (network tab: chunk fetched on first open, page interactive before)
- [ ] Monaco lazy-loads on the YAML editor

## Theming

- [ ] Theme cycle (light → dark → system) works and persists
- [ ] Persistence uses the `astronomer-theme` localStorage key (byte-compatible with pre-migration)
- [ ] Continuous Delivery pages follow the Astronomer theme and no obsolete co-hosted UI writes a bare `theme` key

## Routing & URL state

- [ ] 3 of the 13 `?tab=` pages: hard refresh restores the selected tab, back/forward walks tab history correctly
- [ ] Scroll behavior (D25): from the bottom of a long clusters or audit list, navigate to a detail page → lands at top
- [ ] Back from that detail page restores the previous scroll position
- [ ] Switching a `?tab=` on a page does NOT jump scroll to top

## Live data (real agent)

- [ ] Cluster rows / metrics update live via SSE with a real connected agent (no manual refresh)
- [ ] DevTools offline → wait → online: stream reconnects and data heals (invalidate-on-reconnect)

## Tables

- [ ] One virtualized table scrolls smoothly through a large dataset (e.g. pods)
- [ ] One server-paginated table pages correctly (e.g. audit log)
- [ ] Delivery source/target/rollout/deployment paging and filters survive refresh and back/forward navigation

## Continuous Delivery

- [ ] A target preview with more than 100 decisions pages forward and backward without duplicating or skipping clusters
- [ ] Editing a target invalidates the old preview; rollout start remains blocked until a new preview digest is confirmed
- [ ] A stale target edit surfaces the resource-version conflict instead of overwriting the newer target
- [ ] Rollout approval, pause/resume, abort, retry, and reconcile actions update the visible rollout/deployment state
- [ ] A non-platform administrator cannot create a platform-scoped bundle version
- [ ] Delivery overview reports incompatible agents and links to the affected cluster-agent inventory

## Catalog project isolation

- [ ] Changing the selected project changes the visible chart set without briefly reusing another project's cached results
- [ ] Direct chart versions/default-values requests include the selected project and reject an inaccessible project
- [ ] Cluster Apps only offers projects attached to that cluster and only shows that project's charts/recommendations
- [ ] Installing a chart sends the selected project and cannot target a cluster outside that project
- [ ] A user without project-scoped `catalog:create` can browse but cannot open or submit the install flow

## Misc

- [ ] Pixel-7-ish viewport (~412×915) spot-check: shell, nav, one list page, one detail page usable
- [ ] Version badge shows the baked `VERSION`
- [ ] Favicon renders in the tab
