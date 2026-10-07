# Plan 031 Phase 11 QA matrix (2026-10-06)

An automated matrix, not hand testing. A temporary Playwright spec (removed after the run) visited 16 representative pages at 1440x900, 1920x1080 and 390x844, in light and dark (seeded through the stubbed user preferences, as the visual specs do), in comfortable and compact density. 192 combinations. Each combination asserted:

- no page-level horizontal scroll;
- no serious or critical axe violations (wcag2a, wcag2aa, wcag21aa, wcag22aa);
- no unexpected console errors.

Pages: overview, clusters list, cluster overview, workloads, apps, node detail, delivery estate, audit, settings general, webhooks new, alerting, security, search, charlie, settings backup, rbac.

Result: 192 of 192 pass after the fixes below. Screenshots in this folder are the comfortable-density captures for all widths and themes, plus compact at 1440.

I reviewed a sample of the screenshots by eye (cluster overview light and dark at 390 and 1440, clusters list at 390, estate at 1920, alerting at 390). Defects found and fixed:

1. At 390px the clusters list filter dropdowns clipped their labels ("All Provic", "All Environn"). The filter rows on the clusters page, the alerting events tab and the settings audit tab now wrap, and the dropdowns no longer shrink.
2. At 390px the shared tab strip clipped its last tab and wrapped labels onto two lines. Tab lists now scroll horizontally and tabs no longer shrink or wrap.

Not covered by this matrix: a keyboard-only walkthrough beyond the existing keyboard e2e specs, and judgement of pages that only render empty states under the route stubs.
