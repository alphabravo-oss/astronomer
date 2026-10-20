//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

// End to end through the cobra command an operator runs: seal with key A,
// `astro admin rotate-keys --key-file <B,A>`, then a second run is a no-op and
// every row decrypts with only key B.
func TestAdminRotateKeysCommand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := testdb.DSN(t)
	keyA, _ := crypto.GenerateKey()
	keyB, _ := crypto.GenerateKey()
	encA, _ := crypto.NewEncryptor("a:" + keyA)

	seedPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(seedPool.Close)
	sq := sqlc.New(seedPool)
	for i := range 5 {
		p := sqlc.CreateWebhookSubscriptionParams{
			Name: fmt.Sprintf("hook-%d", i), Url: "https://hooks.example.com",
			EventFilters: json.RawMessage(`["*"]`), ExtraHeaders: json.RawMessage(`{}`), Enabled: true, MaxRetries: 1, TimeoutSeconds: 5,
		}
		sealed, err := encA.EncryptString(fmt.Sprintf("secret-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		p.SecretKeyID, p.SecretAlgorithm, p.SecretCiphertext, p.SecretNonce = sealed.Parts()
		if _, err := sq.CreateWebhookSubscription(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	keyFile := filepath.Join(t.TempDir(), "keys")
	if err := os.WriteFile(keyFile, []byte("# rotated\nb:"+keyB+"\na:"+keyA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := newAdminRotateKeysCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(append([]string{"--database-url", dsn, "--key-file", keyFile, "--batch-size", "2"}, args...))
		if err := cmd.ExecuteContext(ctx); err != nil {
			t.Fatalf("rotate-keys %v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}
	dry := run("--dry-run")
	if !strings.Contains(dry, "webhook_subscriptions.secret") || !strings.Contains(dry, "5 to rewrite") {
		t.Fatalf("dry run output:\n%s", dry)
	}
	first := run()
	t.Logf("astro admin rotate-keys:\n%s", first)
	if !strings.Contains(first, "TOTAL                                                          5 rewritten") {
		t.Fatalf("first run output:\n%s", first)
	}
	second := run()
	if !strings.Contains(second, "TOTAL                                                          0 rewritten") {
		t.Fatalf("second run must be a no-op:\n%s", second)
	}

	onlyB, _ := crypto.NewEncryptor("b:" + keyB)
	rows, err := sq.ListWebhookSubscriptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := onlyB.DecryptString(crypto.FromParts(r.SecretKeyID, r.SecretAlgorithm, r.SecretCiphertext, r.SecretNonce)); err != nil || r.SecretKeyID != "b" {
			t.Fatalf("row %s: key %q: %v", r.Name, r.SecretKeyID, err)
		}
	}
}
