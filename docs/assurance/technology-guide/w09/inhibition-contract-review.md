# Released inhibition contract characterization

Owner: Plan032 W09/W06 integration reviewer. Reviewed 2026-10-09; exact OpenAPI
exceptions expire 2026-11-08 and require re-review before that date. The review
adds only the 22 exact POST/PUT inhibition warning triples observed by oasdiff
1.33.0 against released v1.2.1. Other changes remain outside this review. The
released specification and historical review entries remain unchanged.

The released source is commit `a1fc7b5b` (`v1.2.1`). A separate detached worktree
ran the actual `CreateInhibition` and `UpdateInhibition` handlers with synthetic
requests and a transaction boundary that records entry and then deliberately
returns an error. A 500 with transaction entry proves decoding/validation
accepted the request; it does not claim successful persistence. PUT uses a
successful synthetic existing-row read. Every rejected case verifies that no
mutation transaction was entered. The retained test fixture and verbose log make
the characterization reproducible without a database or credentials:

```sh
git worktree add --detach /tmp/astronomer-inhibition-v1 v1.2.1
cp docs/assurance/technology-guide/w09/released-inhibition-characterization_test.go.txt /tmp/astronomer-inhibition-v1/internal/handler/w09_released_characterization_test.go
cd /tmp/astronomer-inhibition-v1
GOMAXPROCS=4 go test -p=2 ./internal/handler -run '^TestW09ReleasedInhibitionCompatibility$' -count=1 -v
```

All 26 released cases pass, covering the same 13 requests on each method:

- Missing or empty source/target arrays already return 400; empty names return
  422; empty or whitespace-only matcher labels already return 400. Their schema
  required/minimum/pattern additions reflect existing validation. Current safe
  typed field validation rejects these before mutation with 422.
- Missing matcher values, JSON null matcher values, explicit null `is_regex`,
  and blank equality labels previously enter the mutation transaction. These
  malformed-input cases now return a safe field-specific 422 before mutation.
  This is intentional hardening, not unchanged runtime behavior. No stored rule
  is automatically repaired, disabled or deleted.
- Explicit empty string values, omitted regex=false and nullable optional
  equality/enabled fields still enter the transaction. Their supported v1
  semantics remain intact.

The current actual-handler regression repeats all 26 method/request cases:
`go test -p=2 ./internal/handler -run '^TestInhibitionReleasedCompatibilityBoundary$' -count=1 -v`.
The current and released verbose logs are retained in this directory. W06's
separate generated SDK compatibility fixture continues to check the old public
Go pointer layout and serialization; the schema review does not waive that gate.
