# Agent inventory count boundary

Heartbeat and metrics count projection now rejects negative values and values above MaxInt32 before narrowing to database fields. Invalid wire counts reach no persistence, event publication or command fanout. Zero, ordinary and maximum representable values retain their existing behavior; public protocol types and wire layout stay unchanged.

The [packet](packet.json) retains original before and after receipts/logs losslessly as deterministic gzip. Every retained artifact binds its private original path, original byte count and SHA256, and compressed byte count and SHA256. Two complete frozen Git maps bind the source used by the actual runs. The original external artifacts remain unchanged.

The before run at `dd8bd21c` produced ten expected failed terminals (two invalid-count parents and eight leaves), four legitimate passed terminals (one parent and three leaves), and one failed package, with zero skips. The after run at `fed0cfff` passed fourteen parents and 23 leaves under the race detector, with one package pass and zero skips. This includes the count triggers, legitimate heartbeat/metrics behavior, existing upgrade command dispatch and optional payload reader cases.

Tunnel vet, cached JSON governance, dependency boundaries and complexity passed. Six process groups were held and drained; all 27 numeric capacity checks passed, including the final guard. Source and selected Go identities remained stable. The count validation did not alter deadline, lease, shutdown or error behavior owned by W10.

Three existing upgrade payload readers moved verbatim into one cohesive file to reduce the handler. Their three exact JSON ownership sites moved with them. The canonical complexity ratchet decreased from 934 to 895; the checker counts the trailing newline as an additional split entry. Existing absence, invalid-input and optional value behavior remains characterized.

This is scoped evidence for the count boundary and unchanged reader behavior. It does not establish a current repository-wide native scanner pass, close all W02 findings, or complete the whole plan.
