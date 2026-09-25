# Plan027 shell evidence

Captured from the production frontend build serving the Plan027 worktree in the desktop and Pixel 7 Playwright projects. Both projects exercised viewport widths 390, 412, 768, 1024 and 1280.

The shell test waits for Import to load, asserts required navigation, namespace, notification and account controls are visible, measures header button bounds, opens account and notification popovers and checks their viewport bounds, and verifies Escape restores trigger focus. Below 1024 it checks the closed sidebar is inert and excluded from keyboard navigation, then verifies navigation Escape restores focus.

Both responsive test instances passed in the 2026-09-24 browser batch recorded at `/tmp/plan027-browser-navigation.log`. That batch had 14 passing and 6 failing cases overall; its unrelated navigation/scope fixture failures were investigated separately. These screenshots support responsive shell behavior only, not completion of the whole plan. The 390px desktop-project capture was visually inspected and showed all controls loaded and within the viewport.
