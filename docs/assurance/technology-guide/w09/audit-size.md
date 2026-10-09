# Audit writer size boundary receipt

The generic audit `Record` and `RecordMandatory` writers could marshal and write
detail larger than the durable reader's 16 MiB limit. The failing regression
observed a database write and two bus publications, including acceptance by the
mandatory writer. The existing transactional `RecordOutbox` path already had
its stricter 64 KiB bound and rejected this input before the change.

The generic writer now rejects serialized detail beyond the canonical reader
bound before persistence or publication. It retains its existing failure metric
and content-free diagnostic; the mandatory path returns its existing failure
type. It does not truncate a document or fabricate an empty detail object.

`go test -p=2 ./internal/audit ./internal/jsoncontract -count=1` and scoped Go vet
pass. `audit-size-baseline.log` records the actual failing regression;
`audit-size-fix.log` records the full package pass. The selected golangci-lint
2.14.0 scoped check is retained in `audit-size-lint.log`.
