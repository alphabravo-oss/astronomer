# Integrated Go build, source 54763ab3

Both integration-tagged packages compile against the actual clean source
`54763ab397fa97cd11f7c7c11b974f9adf377ef0`: distribution and server, including
the twelve local qualification helper files. Five actual gates and 27 default
capacity observations pass. The original Rekor-body type error on `2551dfa6`
and unused-import error on `803e364f` are retained. The minimal corrections use
the already checked string body and remove the unused import. No integration
test ran; registry, signing graph, database, native scanner/Flux/agent runtime
and application startup remain unqualified by compilation.

The same clean source passes production builds for all 28 command packages
discovered by `go list ./...`, including server, worker, agent, CLI, migrator,
key rotation, database-cache and shell validation helpers, upgrade/distribution
tools, qualification commands and governance tools. All 58 steps and 223 unchanged
20 GiB/20 percent capacity observations pass. Actual retained ELF bodies total
845,742,464 bytes. Root independently matches all binary digests and build logs;
every build-info record identifies the exact Git revision and `vcs.modified=false`.
Builds use Go 1.27.2, Linux/amd64, CGO disabled, trimpath and stripped debug symbols.

`index.json` binds compressed original receipts/logs and binary identities. Gzip
decompression preserves original bytes. Binaries stay in their private local
output directories; this packet does not contain or publish them. Compiling the
FIPS probe provides no FIPS execution or certification evidence. Version remains
1.2.1 with the actual source stamp; no release or tag was created. Image builds,
SBOMs, vulnerability checks, signatures, other platforms and deployment remain
separate work. W01.5's build portion passes; its fresh reachable-vulnerability
check remains open.
