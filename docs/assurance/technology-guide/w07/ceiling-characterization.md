# W07 legacy pagination ceiling characterization

At source `2ef792f8`, the actual catalog chart handler receives an authorized global fixture of 10,041 rows. A valid legacy request at offset 10,000 returns twenty rows and metadata advertising `next_offset:10020`. The next request returns HTTP200 and the same first row, `chart-10000`, because the shared parser clamps the continuation back to 10,000.

`GOMAXPROCS=4 go test -p 2 ./internal/handler -run '^TestCatalogPaginationCeilingMustProgressOrReject$' -count=1 -v` fails on the repeated identity ([receipt](ceiling-before.log)). The regression requires the next request to progress or reject explicitly; both avoid an infinite accepted continuation. It does not claim indexed database cost or real PostgreSQL qualification from this handler fixture.
