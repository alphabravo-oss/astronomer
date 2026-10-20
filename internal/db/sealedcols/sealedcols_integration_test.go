//go:build integration

package sealedcols_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sealedcols"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

// Every *_ciphertext column in the live (fully migrated) schema must be a
// registered family, and every registered family must exist with all four
// columns. A migration that adds a sealed column without registering it would
// otherwise be skipped by rotation, and the old key could never be retired.
func TestRegistryMatchesLiveSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testdb.Pool(t)

	rows, err := pool.Query(ctx, `
		SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name LIKE '%\_ciphertext'
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	var live []sealedcols.Column
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		live = append(live, sealedcols.Column{Table: table, Name: column[:len(column)-len("_ciphertext")]})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, c := range live {
		if !slices.Contains(sealedcols.Columns, c) {
			t.Errorf("schema column %s_ciphertext is not registered in sealedcols.Columns", c)
		}
	}
	for _, c := range sealedcols.Columns {
		for _, suffix := range []string{"_key_id", "_algorithm", "_ciphertext", "_nonce"} {
			var n int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`, c.Table, c.Name+suffix).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Errorf("registered family %s is missing column %s%s", c, c.Name, suffix)
			}
		}
	}
	// No Fernet-era column survives.
	var legacy int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND (column_name LIKE '%\_encrypted' OR column_name = 'encrypted_credentials')`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != 0 {
		t.Errorf("%d *_encrypted columns remain", legacy)
	}
	t.Logf("registry: %d sealed families, live schema: %d *_ciphertext columns", len(sealedcols.Columns), len(live))
}

// The DB enforces the sealed shape: key_id, algorithm and ciphertext are all
// set or all empty.
func TestSealedShapeConstraint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := sqlc.New(testdb.Pool(t))
	_, err := q.CreatePrometheusDatasource(ctx, sqlc.CreatePrometheusDatasourceParams{
		Name: "half-sealed", Url: "http://prom:9090", AuthKeyID: "a", Enabled: true,
	})
	if err == nil {
		t.Fatal("insert with key_id but no algorithm/ciphertext succeeded")
	}
}

type seeded struct {
	table string
	read  func(ctx context.Context) ([]crypto.Sealed, error)
	want  map[string]bool // plaintexts
}

// seed writes rows through the generated store functions (the same calls the
// handlers make) in six different tables, every secret sealed with enc.
func seed(t *testing.T, pool *pgxpool.Pool, enc *crypto.Encryptor) []seeded {
	t.Helper()
	ctx := context.Background()
	q := sqlc.New(pool)
	seal := func(s string) crypto.Sealed {
		t.Helper()
		out, err := enc.EncryptString(s)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	var out []seeded

	// webhook_subscriptions: 250 rows, so rotation crosses batch boundaries.
	webhooks := seeded{table: "webhook_subscriptions", want: map[string]bool{}}
	for i := range 250 {
		secret := fmt.Sprintf("whsec-%03d", i)
		p := sqlc.CreateWebhookSubscriptionParams{
			Name: fmt.Sprintf("hook-%03d", i), Url: "https://hooks.example.com/" + secret,
			EventFilters: json.RawMessage(`["*"]`), ExtraHeaders: json.RawMessage(`{}`),
			Enabled: true, MaxRetries: 3, TimeoutSeconds: 5,
		}
		p.SecretKeyID, p.SecretAlgorithm, p.SecretCiphertext, p.SecretNonce = seal(secret).Parts()
		if _, err := q.CreateWebhookSubscription(ctx, p); err != nil {
			t.Fatalf("CreateWebhookSubscription: %v", err)
		}
		webhooks.want[secret] = true
	}
	webhooks.read = func(ctx context.Context) ([]crypto.Sealed, error) {
		rows, err := q.ListWebhookSubscriptions(ctx)
		var s []crypto.Sealed
		for _, r := range rows {
			s = append(s, crypto.FromParts(r.SecretKeyID, r.SecretAlgorithm, r.SecretCiphertext, r.SecretNonce))
		}
		return s, err
	}
	out = append(out, webhooks)

	// smtp_settings singleton.
	smtp := sqlc.UpsertSMTPSettingsParams{
		ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Enabled: true, Host: "smtp.example.com", Port: 587,
		Username: "mailer", FromAddress: "noreply@example.com", FromName: "Astronomer", AuthMechanism: "plain",
		Encryption: "starttls", RequireTls: true, TimeoutSeconds: 10,
	}
	smtp.PasswordKeyID, smtp.PasswordAlgorithm, smtp.PasswordCiphertext, smtp.PasswordNonce = seal("smtp-password").Parts()
	smtpRow, err := q.UpsertSMTPSettings(ctx, smtp)
	if err != nil {
		t.Fatalf("UpsertSMTPSettings: %v", err)
	}
	out = append(out, seeded{table: "smtp_settings", want: map[string]bool{"smtp-password": true},
		read: func(ctx context.Context) ([]crypto.Sealed, error) {
			r, err := q.GetSMTPSettings(ctx, smtpRow.ID)
			return []crypto.Sealed{crypto.FromParts(r.PasswordKeyID, r.PasswordAlgorithm, r.PasswordCiphertext, r.PasswordNonce)}, err
		}})

	// helm_repositories: credential document.
	helm := seeded{table: "helm_repositories", want: map[string]bool{}}
	for i := range 3 {
		doc := fmt.Sprintf(`{"username":"bot","password":"helm-pat-%d"}`, i)
		p := sqlc.CreateHelmRepositoryParams{
			Name: fmt.Sprintf("private-%d", i), Url: fmt.Sprintf("https://charts-%d.example.com", i), RepoType: "helm",
			AuthType: "basic", AuthConfig: json.RawMessage(`{"username":"bot"}`), Enabled: true,
		}
		p.AuthConfigKeyID, p.AuthConfigAlgorithm, p.AuthConfigCiphertext, p.AuthConfigNonce = seal(doc).Parts()
		if _, err := q.CreateHelmRepository(ctx, p); err != nil {
			t.Fatalf("CreateHelmRepository: %v", err)
		}
		helm.want[doc] = true
	}
	helm.read = func(ctx context.Context) ([]crypto.Sealed, error) {
		rows, err := q.ListHelmRepositories(ctx, sqlc.ListHelmRepositoriesParams{Limit: 1000})
		var s []crypto.Sealed
		for _, r := range rows {
			if r.AuthConfigKeyID != "" {
				s = append(s, crypto.FromParts(r.AuthConfigKeyID, r.AuthConfigAlgorithm, r.AuthConfigCiphertext, r.AuthConfigNonce))
			}
		}
		return s, err
	}
	out = append(out, helm)

	// vault_connections.
	vp := sqlc.CreateVaultConnectionParams{Name: "vault", Addr: "https://vault.example.com", AuthMethod: "token", Enabled: true}
	vp.AuthKeyID, vp.AuthAlgorithm, vp.AuthCiphertext, vp.AuthNonce = seal(`{"token":"hvs.secret"}`).Parts()
	vault, err := q.CreateVaultConnection(ctx, vp)
	if err != nil {
		t.Fatalf("CreateVaultConnection: %v", err)
	}
	out = append(out, seeded{table: "vault_connections", want: map[string]bool{`{"token":"hvs.secret"}`: true},
		read: func(ctx context.Context) ([]crypto.Sealed, error) {
			r, err := q.GetVaultConnectionByID(ctx, vault.ID)
			return []crypto.Sealed{crypto.FromParts(r.AuthKeyID, r.AuthAlgorithm, r.AuthCiphertext, r.AuthNonce)}, err
		}})

	// siem_forwarders.
	sp := sqlc.CreateSIEMForwarderParams{Name: "splunk", Transport: "splunk_hec", Endpoint: "https://splunk.example.com:8088",
		EventFilters: json.RawMessage(`["*"]`), Format: "json", BatchSize: 100, FlushIntervalMs: 1000, TimeoutSeconds: 5, Enabled: true}
	sp.AuthKeyID, sp.AuthAlgorithm, sp.AuthCiphertext, sp.AuthNonce = seal(`{"hec_token":"splunk-hec"}`).Parts()
	siem, err := q.CreateSIEMForwarder(ctx, sp)
	if err != nil {
		t.Fatalf("CreateSIEMForwarder: %v", err)
	}
	out = append(out, seeded{table: "siem_forwarders", want: map[string]bool{`{"hec_token":"splunk-hec"}`: true},
		read: func(ctx context.Context) ([]crypto.Sealed, error) {
			r, err := q.GetSIEMForwarder(ctx, siem.ID)
			return []crypto.Sealed{crypto.FromParts(r.AuthKeyID, r.AuthAlgorithm, r.AuthCiphertext, r.AuthNonce)}, err
		}})

	// prometheus_datasources.
	pp := sqlc.CreatePrometheusDatasourceParams{Name: "thanos", Url: "https://thanos.example.com", Enabled: true}
	pp.AuthKeyID, pp.AuthAlgorithm, pp.AuthCiphertext, pp.AuthNonce = seal(`{"bearer":"prom-token"}`).Parts()
	prom, err := q.CreatePrometheusDatasource(ctx, pp)
	if err != nil {
		t.Fatalf("CreatePrometheusDatasource: %v", err)
	}
	out = append(out, seeded{table: "prometheus_datasources", want: map[string]bool{`{"bearer":"prom-token"}`: true},
		read: func(ctx context.Context) ([]crypto.Sealed, error) {
			r, err := q.GetPrometheusDatasourceByID(ctx, prom.ID)
			return []crypto.Sealed{crypto.FromParts(r.AuthKeyID, r.AuthAlgorithm, r.AuthCiphertext, r.AuthNonce)}, err
		}})
	return out
}

func countByKey(t *testing.T, pool *pgxpool.Pool, keyID string) map[string]int64 {
	t.Helper()
	usage, err := sealedcols.Usage(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, u := range usage {
		if u.KeyID == keyID {
			out[u.Column.String()] += u.Rows
		}
	}
	return out
}

func snapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var b bytes.Buffer
	for _, c := range sealedcols.Columns {
		var digest string
		sql := fmt.Sprintf(`SELECT coalesce(md5(string_agg(%[1]s_key_id || encode(coalesce(%[1]s_ciphertext, ''), 'hex'), ',' ORDER BY %[1]s_ciphertext)), '') FROM public.%[2]s`, c.Name, c.Table)
		if err := pool.QueryRow(context.Background(), sql).Scan(&digest); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s=%s\n", c, digest)
	}
	return b.String()
}

// The rotation proof: rows sealed with key A in six real tables; rotate to B
// (A kept as previous key) with the same code path as `astro admin
// rotate-keys`; afterwards no registered column references A and every row
// decrypts with ONLY key B loaded. A second run is a no-op.
func TestRotateKeysAtoB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testdb.Pool(t)
	keyA, _ := crypto.GenerateKey()
	keyB, _ := crypto.GenerateKey()
	encA, err := crypto.NewEncryptor("key-a:" + keyA)
	if err != nil {
		t.Fatal(err)
	}
	tables := seed(t, pool, encA)
	before := countByKey(t, pool, "key-a")
	t.Logf("before rotation, rows sealed under key-a: %v", before)

	ring, err := crypto.ParseKeyring("key-b:" + keyB + "\nkey-a:" + keyA)
	if err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	results, err := sealedcols.Rotate(ctx, pool, ring, sealedcols.RotateOptions{BatchSize: 100})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if err := sealedcols.WriteReport(&report, results, false); err != nil {
		t.Fatal(err)
	}
	t.Logf("astro admin rotate-keys report:\n%s", report.String())

	var rewritten int64
	for _, r := range results {
		rewritten += r.Rewritten
		if want := before[r.Column.String()]; r.Rewritten != want {
			t.Errorf("%s: rewrote %d rows, want %d", r.Column, r.Rewritten, want)
		}
	}
	if rewritten != 257 {
		t.Errorf("rewrote %d rows, want 257", rewritten)
	}
	for _, c := range sealedcols.Columns {
		var n int64
		sql := fmt.Sprintf(`SELECT count(*) FROM public.%s WHERE %s_key_id = 'key-a'`, c.Table, c.Name)
		if err := pool.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s still has %d rows under key-a", c, n)
		}
	}

	onlyB, _ := crypto.NewEncryptor("key-b:" + keyB)
	for _, s := range tables {
		sealed, err := s.read(ctx)
		if err != nil {
			t.Fatalf("%s: read: %v", s.table, err)
		}
		if len(sealed) != len(s.want) {
			t.Fatalf("%s: read %d sealed values, want %d", s.table, len(sealed), len(s.want))
		}
		for _, v := range sealed {
			if v.KeyID != "key-b" || v.Algorithm != crypto.AlgorithmAgeX25519 || len(v.Nonce) != 0 {
				t.Fatalf("%s: row not stamped with key-b/age: %q %q", s.table, v.KeyID, v.Algorithm)
			}
			plain, err := onlyB.DecryptString(v)
			if err != nil {
				t.Fatalf("%s: decrypt with only key-b: %v", s.table, err)
			}
			if !s.want[plain] {
				t.Fatalf("%s: decrypted unexpected plaintext", s.table)
			}
		}
		t.Logf("%s: %d rows decrypt with only key-b loaded", s.table, len(sealed))
	}

	again, err := sealedcols.Rotate(ctx, pool, ring, sealedcols.RotateOptions{})
	if err != nil {
		t.Fatalf("second Rotate: %v", err)
	}
	for _, r := range again {
		if r.Rewritten != 0 {
			t.Errorf("second run rewrote %d rows in %s; rotation must be idempotent", r.Rewritten, r.Column)
		}
	}
}

// With key A no longer loaded, rotation must refuse up front with a clear
// error and leave every row byte-for-byte unchanged.
func TestRotateKeysRefusesWhenOldKeyIsMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testdb.Pool(t)
	keyA, _ := crypto.GenerateKey()
	keyB, _ := crypto.GenerateKey()
	encA, _ := crypto.NewEncryptor("key-a:" + keyA)
	seed(t, pool, encA)
	before := snapshot(t, pool)

	onlyB, _ := crypto.NewEncryptor("key-b:" + keyB)
	_, err := sealedcols.Rotate(ctx, pool, onlyB, sealedcols.RotateOptions{BatchSize: 10})
	if !errors.Is(err, sealedcols.ErrMissingKey) {
		t.Fatalf("Rotate without key-a: err = %v, want ErrMissingKey", err)
	}
	t.Logf("rotation without the old key refused: %v", err)
	if after := snapshot(t, pool); after != before {
		t.Fatalf("rotation without the old key modified rows:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if n := countByKey(t, pool, "key-a"); n["webhook_subscriptions.secret"] != 250 {
		t.Fatalf("rows lost their key-a stamp: %v", n)
	}
}
