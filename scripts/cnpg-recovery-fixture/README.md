# Owned credential recovery fixture

This executable seeds two synthetic tenants in a fully migrated, empty
`plan032_w12_recovery*` database. It uses production catalog credential sealing,
Fernet envelopes, Charlie trust generation, agent token hashing and audit outbox
serialization. One tenant uses a historical key, including a legacy Fernet
delivery-source value; the other uses the current versioned key. It preserves
project/cluster identities and desired/observed delivery generations.

The database reference and the independently recoverable key/token reference
must be private regular files (0600). The key reference is created exclusively;
an existing file is never overwritten. It contains disposable private material
and must remain outside repository, log and uploaded artifact paths. No customer
key or real upstream credential is accepted or needed. The manifest contains
only public identities, public certificates, hashes and expected generations.

Build from the reviewed source using its selected Go toolchain:

```sh
GOTOOLCHAIN=go1.27.2 go build -trimpath -o /private/bin/cnpg-recovery-fixture ./scripts/cnpg-recovery-fixture
/private/bin/cnpg-recovery-fixture --mode seed \
  --database-ref /private/recovery-database.json \
  --key-ref /private/independent-recovery-keys.json \
  --manifest /private/recovery-expected.json --source-commit "$REVIEWED_SOURCE_COMMIT"
```

The database reference is a closed JSON object with `database_url`. Database
name overrides, fragments, symlinks, public references and databases outside the
owned prefix are rejected before a connection is made. Seeding requires schema
72 and no existing clusters, projects or Charlie connections. It writes only
that database, including an owned marker table; it never invokes Kubernetes,
contacts Charlie or mutates an existing production fixture.

After the operator takes a physical backup and restores into a fresh owned
database with the same required name prefix, recover the key/token reference
independently, preserving its exact contents and permissions. Use a new private
database reference pointing to the restore and the original manifest:

```sh
/private/bin/cnpg-recovery-fixture --mode verify \
  --database-ref /private/restored-database.json \
  --key-ref /private/recovered-keys.json \
  --manifest /private/recovery-expected.json --source-commit "$REVIEWED_SOURCE_COMMIT"
```

Verification uses production decryption and readers, checks both Charlie TLS
private-key/certificate pairs, usages and identities, rejects agent access across
tenant clusters, and compares tenant identities, generations and audit intent
order/details. The same verification command with `--negative-key-mode
missing-old` and `--negative-key-mode wrong` must exit nonzero; missing-key
success is never a qualified restoration. Those negative modes do not alter the
recovered key file or database.

Unit tests run without a database. The real reader test is explicitly selected
with `-tags=integration` and
`ASTRONOMER_RECOVERY_TEST_DATABASE_REF=/private/reference.json`; a missing
prerequisite fails rather than skips. It needs a fresh dedicated database and
intentionally changes its synthetic deployment generation after the positive
and key-negative checks to prove that restoration drift is detected.

This fixture does not itself perform backup, restore or key recovery. Keep the
operator's source, cluster/PVC identities, backup object identity, restored
endpoint, command results and timing with the resulting receipt. Successful
reader checks establish synthetic data/key restoration at that source version;
they do not establish whole-application RPO/RTO, a cryptographic audit hash chain,
external provider behavior or a human release approval.
