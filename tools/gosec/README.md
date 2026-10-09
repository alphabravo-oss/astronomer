# Source scanner dependency closure

This module builds unchanged upstream gosec 2.29.0 with released upstream x/tools 0.51.0, which supports Go 1.27 export-version5. The official 2.29.0 binary embeds x/tools 0.49.0 and cannot completely analyze the selected compiler's export data. No scanner Go source is copied, patched or vendored. This is Astronomer's reproducible build-tool dependency closure, not an official vendor binary or runtime dependency/image fork.

Run `scripts/build-gosec.sh /absolute/output/gosec` from the repository. It verifies the closed version/lock record, review deadline and module cache, then builds with `-mod=readonly -trimpath`. Scanner version authority lives in this module; W01 connects it to the repository authority. The tool directive keeps the command dependency in the lock graph without importing a main package.

[Evidence](../../docs/assurance/technology-guide/w02/README.md) records before/after dependency graphs, actual build metadata, complete selected-toolchain coverage and scanner self-analysis. Compatibility does not accept any source finding. The dated closure review must be renewed or removed when an official stable scanner release supports the selected toolchain. Never edit the closure digest merely to bypass a failed review.
