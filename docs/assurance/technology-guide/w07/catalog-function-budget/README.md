# Catalog function budget followup

The combined source exceeded the existing CatalogPage ceiling: 312 lines against 310. Commit `741ed742` extracts the unchanged chart empty state into a colocated component and reduces the function to 292 lines. The ceiling decreases to 292; no ceiling increases. Commit `ea24c5a5` refreshes both generated source inventories from that combined source. Root retains the original failing complexity receipt separately.

The clean `ea24c5a547daf7209805751fdde4842395b5cae0` freeze passes all 25 tests in five existing catalog/cursor fixture files, frontend TypeScript, and scoped ESLint. [The receipt](receipt.json) records exact commands, source cleanliness, resource checks and raw log hashes. [The test report](catalog-test-report.json) records zero failed or pending tests. [The packet](packet.json) binds both production files and every retained artifact.

Tests use the same lockfile and read-only package-entry symlinks to the qualified W07 Node 26/npm 12 install, with caller-local Vite caches. No install, browser run, full build or Go compiler ran in this window. The complexity gate and both generated inventory checks also pass; SQL/JSON ownership metadata remains unchanged because its discovered sites and exact owner hashes are unchanged.

This receipt covers the function budget correction only. Whole W07 pagination coverage and representative selective query costs, and the current W02 native source rescan, remain open.
