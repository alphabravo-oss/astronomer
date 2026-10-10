package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// This fixture requires a separately live-fenced, exclusively owned database.
// It uses the production registrar, JWT middleware, sqlc transaction adapter and
// generated SDK over a real loopback listener. It does not qualify public TLS.
func TestPreferencePostgresProductionRouteGeneratedClientContract(t *testing.T) {
	dsn := os.Getenv("PAGINATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PAGINATION_TEST_DATABASE_URL is not set")
	}
	expectedName := os.Getenv("PREFERENCE_ROUTE_TEST_DATABASE_NAME")
	expectedMajor, err := strconv.Atoi(os.Getenv("PREFERENCE_ROUTE_TEST_DATABASE_MAJOR"))
	if expectedName == "" || err != nil || (expectedMajor != 17 && expectedMajor != 18) {
		t.Fatal("owned preference route database identity is incomplete")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	database, err := db.ConnectWithConfig(ctx, dsn, db.PoolConfig{MaxConns: 4, MinConns: 1})
	if err != nil {
		t.Fatal("owned preference route database connection failed")
	}
	t.Cleanup(database.Close)
	var actualName string
	var actualMajor int
	if err := database.Pool().QueryRow(ctx, `SELECT current_database(),current_setting('server_version_num')::int/10000`).Scan(&actualName, &actualMajor); err != nil || actualName != expectedName || actualMajor != expectedMajor {
		t.Fatal("owned preference route database identity mismatch")
	}
	version, dirty, err := database.SchemaVersion(ctx)
	if err != nil || dirty || version != db.ExpectedSchemaVersion {
		t.Fatal("owned preference route database schema mismatch")
	}
	actor, other := uuid.New(), uuid.New()
	key := "w13-route-fixture-" + uuid.NewString()
	// Install exact owned-row cleanup BEFORE any committed insertion, including
	// ambiguous client cancellation. Never delete another actor's state.
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for _, id := range []uuid.UUID{actor, other} {
			if _, err := database.Pool().Exec(cleanup, `DELETE FROM operation_idempotency_keys WHERE scope LIKE 'user-preferences:v1:user:'||$1||':%' AND idempotency_key=$2`, id.String(), key); err != nil {
				t.Error("owned preference route receipt cleanup failed")
			}
			if _, err := database.Pool().Exec(cleanup, `DELETE FROM audit_outbox WHERE user_id=$1 AND action='user.preferences.updated' AND resource_type='user_preferences' AND path='/api/v1/auth/me/preferences/'`, id); err != nil {
				t.Error("owned preference route audit cleanup failed")
			}
			if _, err := database.Pool().Exec(cleanup, `DELETE FROM users WHERE id=$1 AND username=$2 AND email=$3`, id, "w13-route-fixture-"+id.String(), "w13-route-fixture-"+id.String()+"@example.invalid"); err != nil {
				t.Error("owned preference route actor cleanup failed")
			}
		}
	})
	for _, id := range []uuid.UUID{actor, other} {
		if _, err := database.Pool().Exec(ctx, `INSERT INTO users(id,username,email) VALUES($1,$2,$3)`, id, "w13-route-fixture-"+id.String(), "w13-route-fixture-"+id.String()+"@example.invalid"); err != nil {
			t.Fatal("owned preference route actor insertion failed")
		}
	}
	queries := sqlc.New(database.Pool())
	var signingKey [32]byte
	if _, err := rand.Read(signingKey[:]); err != nil {
		t.Fatal("owned preference route signing key generation failed")
	}
	manager, err := auth.NewJWTManager(base64.RawStdEncoding.EncodeToString(signingKey[:]), 15)
	if err != nil {
		t.Fatal("owned preference route JWT manager failed")
	}
	manager.SetRevocationChecker(handler.NewJWTRevocationChecker(queries))
	h := handler.NewAuthHandlerWithTokens(queries, queries, manager)
	h.SetUserPreferences(queries, sqlcMutationTxRunner[handler.UserPreferencesMutationTx](database))
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) {
		registerAPIIdentityEntryRoutes(r, RouterDependencies{CoreAuth: CoreAuthDependencies{Auth: h, JWT: manager, AuthQueries: queries}})
	})
	server := httptest.NewUnstartedServer(router)
	server.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	httpClient := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() {
		cancel()
		httpClient.CloseIdleConnections()
		server.Close()
	})
	server.Start()
	client, err := astroclient.NewClient(server.URL, astroclient.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal("generated preference client construction failed")
	}
	token := func(id uuid.UUID) string {
		t.Helper()
		value, err := manager.GenerateAccessTokenContext(ctx, id)
		if err != nil {
			t.Fatal("owned preference route token mint failed")
		}
		return value
	}
	request, err := astroclient.NewGetAuthMePreferencesRequest(server.URL)
	if err != nil || request.URL.Path != "/api/v1/auth/me/preferences/" {
		t.Fatal("released SDK preference path does not match the registered route")
	}
	actorToken, otherToken := token(actor), token(other)
	editor := func(tokenValue, requestKey string) astroclient.RequestEditorFn {
		return func(_ context.Context, r *http.Request) error {
			if tokenValue != "" {
				r.Header.Set("Authorization", "Bearer "+tokenValue)
			}
			if requestKey != "" {
				r.Header.Set("Idempotency-Key", requestKey)
			}
			return nil
		}
	}
	read := func(response *http.Response, err error, expected int) []byte {
		t.Helper()
		if err != nil {
			t.Fatal("generated preference client transport failed")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		if err != nil || len(body) > 64<<10 || response.StatusCode != expected {
			t.Fatalf("generated preference response mismatch: status=%d expected=%d", response.StatusCode, expected)
		}
		return body
	}
	get := func(value string, status int) []byte {
		t.Helper()
		response, err := client.GetAuthMePreferences(ctx, editor(value, ""))
		return read(response, err, status)
	}
	put := func(value string, document []byte, requestKey string, status int) []byte {
		t.Helper()
		response, err := client.PutAuthMePreferencesWithBody(ctx, "application/json", bytes.NewReader(document), editor(value, requestKey))
		return read(response, err, status)
	}
	putTyped := func(value string, document astroclient.UserPreferences, requestKey string, status int) []byte {
		t.Helper()
		response, err := client.PutAuthMePreferences(ctx, document, editor(value, requestKey))
		return read(response, err, status)
	}
	counts := func() (int, int, int) {
		t.Helper()
		var rows, audits, receipts int
		if err := database.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM user_preferences WHERE user_id=$1),(SELECT count(*) FROM audit_outbox WHERE user_id=$1 AND action='user.preferences.updated'),(SELECT count(*) FROM operation_idempotency_keys WHERE scope LIKE 'user-preferences:v1:user:'||$2||':%' AND idempotency_key=$3)`, actor, actor.String(), key).Scan(&rows, &audits, &receipts); err != nil {
			t.Fatal("owned preference route state count failed")
		}
		return rows, audits, receipts
	}
	assertCounts := func(rows, audits, receipts int) {
		t.Helper()
		r, a, k := counts()
		if r != rows || a != audits || k != receipts {
			t.Fatalf("owned preference route state mismatch: %d/%d/%d expected %d/%d/%d", r, a, k, rows, audits, receipts)
		}
	}
	var document astroclient.UserPreferences
	defaults, err := json.Marshal(userpreferences.Defaults())
	if err != nil || json.Unmarshal(defaults, &document) != nil {
		t.Fatal("domain to released SDK preference conversion failed")
	}
	document.Theme = "dark"
	dark, err := json.Marshal(document)
	if err != nil {
		t.Fatal("generated preference body encoding failed")
	}
	get("", http.StatusUnauthorized)
	get("invalid-owned-jwt", http.StatusUnauthorized)
	get(actorToken, http.StatusOK)
	assertCounts(0, 0, 0)
	for _, invalid := range []string{strings.Replace(string(dark), `"favorites":[]`, `"favorites":null`, 1), strings.Replace(string(dark), `"favorites":[],`, "", 1)} {
		if invalid == string(dark) {
			t.Fatal("required favorites fixture did not change the actual SDK body")
		}
		put(actorToken, []byte(invalid), key, http.StatusBadRequest)
		assertCounts(0, 0, 0)
	}
	first := putTyped(actorToken, document, key, http.StatusOK)
	second := putTyped(actorToken, document, key, http.StatusOK)
	if !bytes.Equal(first, second) {
		t.Fatal("keyed generated client retry changed the committed preference response")
	}
	assertCounts(1, 1, 1)
	document.Theme = "light"
	putTyped(actorToken, document, key, http.StatusConflict)
	assertCounts(1, 1, 1)
	var theme, auditTheme string
	if err := database.Pool().QueryRow(ctx, `SELECT p.theme,a.detail->>'theme' FROM user_preferences p JOIN audit_outbox a ON a.user_id=p.user_id WHERE p.user_id=$1 AND a.action='user.preferences.updated'`, actor).Scan(&theme, &auditTheme); err != nil || theme != "dark" || auditTheme != "dark" {
		t.Fatal("changed keyed generated request changed domain state or audit detail")
	}
	otherBody := get(otherToken, http.StatusOK)
	var otherDocument struct {
		Data astroclient.UserPreferences `json:"data"`
	}
	if json.Unmarshal(otherBody, &otherDocument) != nil || otherDocument.Data.Theme != "system" {
		t.Fatal("generated preference reader leaked another actor's document")
	}
	putTyped(actorToken, document, "", http.StatusOK)
	assertCounts(1, 2, 1)
	var ordinaryDocument struct {
		Data astroclient.UserPreferences `json:"data"`
	}
	if json.Unmarshal(get(actorToken, http.StatusOK), &ordinaryDocument) != nil || ordinaryDocument.Data.Theme != "light" {
		t.Fatal("ordinary generated replacement did not persist the current actor document")
	}
	// A newly minted JTI exercises a fresh database identity lookup. It does not
	// claim immediate invalidation of an already cached session identity.
	if _, err := database.Pool().Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1 AND username=$2`, other, "w13-route-fixture-"+other.String()); err != nil {
		t.Fatal("owned preference route deactivation fixture failed")
	}
	get(token(other), http.StatusUnauthorized)
}
