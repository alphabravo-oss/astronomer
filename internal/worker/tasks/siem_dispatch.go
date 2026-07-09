package tasks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/siem"
)

// Migration 055 — SIEM dispatcher + retention task types.
const (
	// SIEMDispatchType is the periodic task that drains the per-
	// forwarder queue tables. Runs every 2 seconds; cooperative DB
	// lease keeps multiple worker pods from racing on the same
	// forwarder. The 2s cadence is the SLA between event-fire and
	// SIEM receipt under healthy conditions.
	SIEMDispatchType = "siem:dispatch"

	// SIEMCleanupOldType deletes queue rows older than the retention
	// window (7 days) regardless of forwarder status. Daily cadence.
	SIEMCleanupOldType = "siem:cleanup_old"
)

// SIEMRetryCap is the per-row retry budget. After this many failed
// dispatch attempts, the row is dropped (force-deleted) and the
// dropped_total counter ticks. 100 covers ~3 minutes of sustained
// failures at the 2s dispatch cadence — plenty for transient outages
// but short enough that a permanently-broken forwarder doesn't pin
// disk.
const SIEMRetryCap int32 = 100

// SIEMQueueRetention is the queue row retention window. The daily
// cleanup task removes rows older than this regardless of forwarder
// status so a stuck or disabled forwarder doesn't grow the queue
// unbounded.
const SIEMQueueRetention = 7 * 24 * time.Hour

// SIEMQuerier is the database surface the dispatch task needs.
// *sqlc.Queries satisfies it directly.
type SIEMQuerier interface {
	ListEnabledSIEMForwarders(ctx context.Context) ([]sqlc.SiemForwarder, error)
	ListSIEMQueueBatch(ctx context.Context, arg sqlc.ListSIEMQueueBatchParams) ([]sqlc.SiemForwardQueue, error)
	ListSIEMQueueExhausted(ctx context.Context, arg sqlc.ListSIEMQueueExhaustedParams) ([]sqlc.SiemForwardQueue, error)
	DeleteSIEMQueueByIDs(ctx context.Context, ids []int64) error
	IncrementSIEMQueueAttempts(ctx context.Context, ids []int64) error
	CountSIEMQueueByForwarder(ctx context.Context, forwarderID uuid.UUID) (int64, error)
	UpsertSIEMForwarderStatus(ctx context.Context, arg sqlc.UpsertSIEMForwarderStatusParams) error
	DeleteSIEMQueueOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// SIEMTransportFactory builds a Transport for a forwarder row. The
// factory pattern lets tests stub the transport layer without standing
// up real syslog / HEC sinks.
type SIEMTransportFactory func(sub sqlc.SiemForwarder, secret authBlob) (siem.Transport, error)

// SIEMDeps is the dependency bag the dispatcher reads. Wired by the
// server at startup; stays in a package-level var so the asynq
// HandleFunc signature can stay standard.
type SIEMDeps struct {
	Queries          SIEMQuerier
	Encryptor        *auth.Encryptor
	TransportFactory SIEMTransportFactory
	HTTPClient       *http.Client
}

var siemDeps SIEMDeps

// perForwarderLock keeps per-forwarder dispatch concurrency at 1. The
// dispatcher tick walks every enabled forwarder; the lock means a
// long-running send for forwarder A can't double-fire while the next
// tick comes around. Lock map keyed by forwarder UUID; entries live for
// the process lifetime — there are only ever ~few forwarders so
// unbounded growth isn't a concern.
var (
	perForwarderLockMu sync.Mutex
	perForwarderLock   = map[uuid.UUID]*sync.Mutex{}
)

func lockForForwarder(id uuid.UUID) *sync.Mutex {
	perForwarderLockMu.Lock()
	defer perForwarderLockMu.Unlock()
	l, ok := perForwarderLock[id]
	if !ok {
		l = &sync.Mutex{}
		perForwarderLock[id] = l
	}
	return l
}

// ConfigureSIEM wires the dispatcher's dependencies. Safe to call
// multiple times (last call wins).
func ConfigureSIEM(deps SIEMDeps) {
	siemDeps = deps
	if siemDeps.HTTPClient == nil {
		siemDeps.HTTPClient = httpclient.New(10 * time.Second)
	}
	if siemDeps.TransportFactory == nil {
		siemDeps.TransportFactory = defaultSIEMTransportFactory
	}
	// Drop any cached per-forwarder HTTP clients so a re-wire (or a test
	// swapping deps) doesn't hand out a client bound to stale TLS config.
	siemHTTPClientMu.Lock()
	siemHTTPClientCache = map[uuid.UUID]cachedSIEMHTTPClient{}
	siemHTTPClientMu.Unlock()
}

// cachedSIEMHTTPClient memoizes a forwarder's *http.Client so keep-alive
// connections are reused across the 2s drains. key fingerprints the TLS-relevant
// forwarder fields; when they change the client is rebuilt.
type cachedSIEMHTTPClient struct {
	key    string
	client *http.Client
}

var (
	siemHTTPClientMu    sync.Mutex
	siemHTTPClientCache = map[uuid.UUID]cachedSIEMHTTPClient{}
)

// httpClientForForwarder returns a pooled *http.Client for the forwarder,
// building it once and reusing it across drains. The HEC/NDJSON transports
// previously allocated a fresh *http.Transport on every 2s tick (Close() is a
// no-op, so nothing was ever reused) — thousands of redundant TLS handshakes an
// hour under sustained load. Caching keyed by (tls_skip_verify, ca_cert_pem,
// timeout) keeps a single keep-alive pool per forwarder while still rebuilding
// when the operator changes TLS settings.
func httpClientForForwarder(sub sqlc.SiemForwarder, timeout time.Duration) *http.Client {
	key := fmt.Sprintf("%t|%d|%s", sub.TlsSkipVerify, int64(timeout), sub.CaCertPem)
	siemHTTPClientMu.Lock()
	defer siemHTTPClientMu.Unlock()
	if c, ok := siemHTTPClientCache[sub.ID]; ok && c.key == key {
		return c.client
	}
	client := buildHTTPClient(sub, timeout)
	siemHTTPClientCache[sub.ID] = cachedSIEMHTTPClient{key: key, client: client}
	return client
}

// authBlob is the decrypted shape stored in siem_forwarders.auth_encrypted.
// The handler accepts free-form JSON on input so future auth styles
// (mTLS cert, OAuth client_credentials, AWS sigv4) can be added without
// a migration; today we read just token / username / password.
type authBlob struct {
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Headers is a free-form header bag added to every HTTPS request.
	// Used for generic NDJSON sinks that gate on a custom header key.
	Headers map[string]string `json:"headers,omitempty"`
}

// HandleSIEMDispatch is the periodic task that drains every enabled
// forwarder's queue. Pattern matches the webhook dispatcher:
//
//  1. Leader-elect so only one worker pod runs the loop.
//  2. List enabled forwarders.
//  3. Per forwarder, lock + drain a batch + ship + DELETE.
func HandleSIEMDispatch(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, SIEMDispatchType, func() error {
		if siemDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "siem dispatcher not configured, skipping")
			return nil
		}
		forwarders, err := siemDeps.Queries.ListEnabledSIEMForwarders(ctx)
		if err != nil {
			return fmt.Errorf("list siem forwarders: %w", err)
		}
		for _, sub := range forwarders {
			if err := ctx.Err(); err != nil {
				return nil
			}
			dispatchForwarder(ctx, sub)
		}
		return nil
	})
}

// dispatchForwarder runs one forwarder's drain. Per-forwarder lock
// keeps the dispatch concurrency at 1 even if a slow Send keeps a
// goroutine alive when the next tick arrives.
func dispatchForwarder(ctx context.Context, sub sqlc.SiemForwarder) {
	l := lockForForwarder(sub.ID)
	if !tryLock(l) {
		// Previous tick's dispatch is still in flight. Skip this
		// forwarder for now; the next tick picks it up.
		runtimeLogger().DebugContext(ctx, "siem: skipping forwarder, dispatch in flight",
			"forwarder", sub.Name)
		return
	}
	defer l.Unlock()

	batchSize := int32(100)
	if sub.BatchSize > 0 {
		batchSize = sub.BatchSize
	}

	// First, evict rows that crossed the retry cap. These can't make
	// progress and would starve the rest of the batch.
	if exhausted, err := siemDeps.Queries.ListSIEMQueueExhausted(ctx, sqlc.ListSIEMQueueExhaustedParams{
		ForwarderID: sub.ID,
		Attempts:    SIEMRetryCap,
		Limit:       batchSize,
	}); err == nil && len(exhausted) > 0 {
		ids := rowIDs(exhausted)
		_ = siemDeps.Queries.DeleteSIEMQueueByIDs(ctx, ids)
		siem.RecordDropped(sub.Name, "retries_exhausted", len(ids))
		_ = siemDeps.Queries.UpsertSIEMForwarderStatus(ctx, sqlc.UpsertSIEMForwarderStatusParams{
			ForwarderID:  sub.ID,
			LastError:    fmt.Sprintf("dropped %d rows past retry cap (%d)", len(ids), SIEMRetryCap),
			QueueDepth:   0, // updated below after the new depth read
			DroppedTotal: int64(len(ids)),
		})
	}

	rows, err := siemDeps.Queries.ListSIEMQueueBatch(ctx, sqlc.ListSIEMQueueBatchParams{
		ForwarderID: sub.ID,
		Limit:       batchSize,
	})
	if err != nil {
		runtimeLogger().WarnContext(ctx, "siem: list queue failed",
			"forwarder", sub.Name, "error", err)
		return
	}
	if len(rows) == 0 {
		// Refresh queue_depth gauge to 0 so dashboards see the
		// "caught up" state.
		_ = siemDeps.Queries.UpsertSIEMForwarderStatus(ctx, sqlc.UpsertSIEMForwarderStatusParams{
			ForwarderID: sub.ID,
			QueueDepth:  0,
		})
		siem.RecordQueueDepth(sub.Name, 0)
		return
	}

	// Decrypt the auth blob once per drain.
	secret, err := decryptAuthBlob(sub.AuthEncrypted)
	if err != nil {
		runtimeLogger().WarnContext(ctx, "siem: decrypt auth blob failed",
			"forwarder", sub.Name, "error", err)
		_ = siemDeps.Queries.UpsertSIEMForwarderStatus(ctx, sqlc.UpsertSIEMForwarderStatusParams{
			ForwarderID: sub.ID,
			LastError:   truncateDispatchLastError("decrypt auth: "+err.Error(), 1024),
		})
		return
	}

	transport, err := siemDeps.TransportFactory(sub, secret)
	if err != nil {
		runtimeLogger().WarnContext(ctx, "siem: build transport failed",
			"forwarder", sub.Name, "error", err)
		_ = siemDeps.Queries.UpsertSIEMForwarderStatus(ctx, sqlc.UpsertSIEMForwarderStatusParams{
			ForwarderID: sub.ID,
			LastError:   truncateDispatchLastError("transport: "+err.Error(), 1024),
		})
		return
	}
	defer func() {
		_ = transport.Close()
	}()

	formatID := sub.Format
	if formatID == "" {
		formatID = siem.DefaultFormatForTransport(sub.Transport)
	}

	formatted := make([][]byte, 0, len(rows))
	for _, row := range rows {
		ev, err := rowToSIEMEvent(row, sub)
		if err != nil {
			runtimeLogger().WarnContext(ctx, "siem: decode payload failed",
				"forwarder", sub.Name, "row", row.ID, "error", err)
			continue
		}
		bytes := siem.FormatForID(formatID, ev)
		if len(bytes) == 0 {
			siem.RecordDropped(sub.Name, "format_error", 1)
			continue
		}
		formatted = append(formatted, bytes)
	}
	if len(formatted) == 0 {
		// Every row failed to format; drop them so they don't block
		// progress.
		_ = siemDeps.Queries.DeleteSIEMQueueByIDs(ctx, rowIDs(rows))
		return
	}

	// Apply the forwarder's timeout for the Send call.
	timeout := time.Duration(sub.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ids := rowIDs(rows)
	sendErr := transport.Send(sendCtx, formatted)
	now := time.Now().UTC()
	if sendErr != nil {
		// Failure: keep the rows, bump their attempts counter, and
		// record the error on the status row.
		_ = siemDeps.Queries.IncrementSIEMQueueAttempts(ctx, ids)
		depth, _ := siemDeps.Queries.CountSIEMQueueByForwarder(ctx, sub.ID)
		siem.RecordDispatched(sub.Name, formatID, "failed", len(rows))
		siem.RecordQueueDepth(sub.Name, int(depth))
		_ = siemDeps.Queries.UpsertSIEMForwarderStatus(ctx, sqlc.UpsertSIEMForwarderStatusParams{
			ForwarderID: sub.ID,
			LastError:   truncateDispatchLastError(sendErr.Error(), 1024),
			QueueDepth:  int32(depth),
		})
		runtimeLogger().WarnContext(ctx, "siem: dispatch failed",
			"forwarder", sub.Name, "rows", len(rows), "error", sendErr)
		return
	}

	// Success: DELETE the rows, refresh the status row.
	if err := siemDeps.Queries.DeleteSIEMQueueByIDs(ctx, ids); err != nil {
		runtimeLogger().WarnContext(ctx, "siem: delete after send failed",
			"forwarder", sub.Name, "rows", len(rows), "error", err)
		// Rows will be retried next tick — Send already succeeded so
		// the SIEM has the data, but the platform may re-ship until
		// the DELETE succeeds. Operators see duplicate events; the
		// alternative is losing rows on a DELETE failure.
		return
	}
	depth, _ := siemDeps.Queries.CountSIEMQueueByForwarder(ctx, sub.ID)
	siem.RecordDispatched(sub.Name, formatID, "delivered", len(rows))
	siem.RecordQueueDepth(sub.Name, int(depth))
	_ = siemDeps.Queries.UpsertSIEMForwarderStatus(ctx, sqlc.UpsertSIEMForwarderStatusParams{
		ForwarderID:     sub.ID,
		LastSentAt:      pgtype.Timestamptz{Time: now, Valid: true},
		LastError:       "",
		QueueDepth:      int32(depth),
		DispatchedTotal: int64(len(rows)),
	})
}

// HandleSIEMCleanupOld deletes queue rows older than the retention
// window. Daily cadence; cooperative DB lease.
func HandleSIEMCleanupOld(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, SIEMCleanupOldType, func() error {
		if siemDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "siem cleanup not configured, skipping")
			return nil
		}
		cutoff := time.Now().UTC().Add(-SIEMQueueRetention)
		removed, err := siemDeps.Queries.DeleteSIEMQueueOlderThan(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("delete old siem queue: %w", err)
		}
		runtimeLogger().InfoContext(ctx, "siem retention sweep",
			"rows_deleted", removed,
			"cutoff", cutoff.Format(time.RFC3339),
		)
		return nil
	})
}

// rowToSIEMEvent rehydrates the JSONB payload into a siem.SIEMEvent.
// The tap writes the envelope {event_name, event_id, timestamp,
// detail}; we layer the forwarder-derived hostname + severity on top.
func rowToSIEMEvent(row sqlc.SiemForwardQueue, sub sqlc.SiemForwarder) (siem.SIEMEvent, error) {
	var env struct {
		EventName string          `json:"event_name"`
		EventID   string          `json:"event_id"`
		Timestamp time.Time       `json:"timestamp"`
		Detail    json.RawMessage `json:"detail,omitempty"`
	}
	if len(row.Payload) > 0 {
		if err := json.Unmarshal(row.Payload, &env); err != nil {
			return siem.SIEMEvent{}, fmt.Errorf("unmarshal payload: %w", err)
		}
	}
	if env.EventName == "" {
		env.EventName = row.EventName
	}
	if env.Timestamp.IsZero() {
		env.Timestamp = row.CreatedAt
	}
	// Resource hints from the detail JSON. Best-effort — we don't fail
	// the format if the keys are missing.
	resourceID, resourceType, actorUserID := extractResourceHints(env.Detail)
	hostname := "" // operator-supplied via auth blob headers if needed
	return siem.SIEMEvent{
		EventName:    env.EventName,
		EventID:      env.EventID,
		Timestamp:    env.Timestamp,
		Severity:     row.Severity,
		Hostname:     hostname,
		ActorUserID:  actorUserID,
		ResourceID:   resourceID,
		ResourceType: resourceType,
		Detail:       env.Detail,
	}, nil
}

// extractResourceHints scans the detail JSON for the canonical audit
// fields. Returns ("", "", "") on a malformed detail; the format layer
// handles empty values gracefully.
func extractResourceHints(detail json.RawMessage) (resourceID, resourceType, actorUserID string) {
	if len(detail) == 0 {
		return "", "", ""
	}
	var m map[string]any
	if err := json.Unmarshal(detail, &m); err != nil {
		return "", "", ""
	}
	if v, ok := m["resource_id"].(string); ok {
		resourceID = v
	}
	if v, ok := m["resource_type"].(string); ok {
		resourceType = v
	}
	if v, ok := m["actor_user_id"].(string); ok {
		actorUserID = v
	}
	return
}

// decryptAuthBlob undoes the Fernet wrap the handler applies on create.
// Returns the empty blob (no token, no auth) when auth_encrypted is
// empty — that's the default for forwarders that don't need auth (e.g.
// a syslog UDP sink on a private network).
func decryptAuthBlob(encrypted string) (authBlob, error) {
	if strings.TrimSpace(encrypted) == "" {
		return authBlob{}, nil
	}
	if siemDeps.Encryptor == nil {
		return authBlob{}, errors.New("encryptor unavailable")
	}
	plain, err := siemDeps.Encryptor.Decrypt(encrypted)
	if err != nil {
		return authBlob{}, err
	}
	var blob authBlob
	if err := json.Unmarshal([]byte(plain), &blob); err != nil {
		return authBlob{}, fmt.Errorf("decode auth: %w", err)
	}
	return blob, nil
}

// defaultSIEMTransportFactory is the production transport builder.
// Tests substitute via SIEMDeps.TransportFactory.
func defaultSIEMTransportFactory(sub sqlc.SiemForwarder, secret authBlob) (siem.Transport, error) {
	dialTimeout := time.Duration(sub.TimeoutSeconds) * time.Second
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	switch sub.Transport {
	case siem.TransportSyslogUDP:
		return siem.NewSyslogUDP(sub.Endpoint), nil
	case siem.TransportSyslogTCP:
		return siem.NewSyslogTCP(sub.Endpoint, dialTimeout), nil
	case siem.TransportSyslogTLS:
		cfg, err := buildTLSConfig(sub)
		if err != nil {
			return nil, err
		}
		return siem.NewSyslogTLS(sub.Endpoint, cfg, dialTimeout), nil
	case siem.TransportSplunkHEC:
		client := httpClientForForwarder(sub, dialTimeout)
		return siem.NewSplunkHEC(sub.Endpoint, secret.Token, client), nil
	case siem.TransportNDJSONHTTPS:
		client := httpClientForForwarder(sub, dialTimeout)
		hdr := http.Header{}
		for k, v := range secret.Headers {
			hdr.Set(k, v)
		}
		if secret.Token != "" && hdr.Get("Authorization") == "" {
			hdr.Set("Authorization", "Bearer "+secret.Token)
		}
		return siem.NewNDJSONHTTPS(sub.Endpoint, client, hdr), nil
	default:
		return nil, fmt.Errorf("%w: %q", siem.ErrUnsupportedTransport, sub.Transport)
	}
}

// buildHTTPClient applies the per-forwarder TLS toggles to an *http.Client
// that still enforces dial-time public-IP checks (SEC-03 SafeClient).
// When tls_skip_verify is on, we log a warn at startup elsewhere; the
// client itself just honors the operator's choice.
func buildHTTPClient(sub sqlc.SiemForwarder, timeout time.Duration) *http.Client {
	cfg, err := buildTLSConfig(sub)
	if err != nil {
		// Fall back to default TLS if the CA bundle is malformed; the
		// dispatcher logs the error elsewhere via the status row.
		cfg = &tls.Config{InsecureSkipVerify: sub.TlsSkipVerify} //nolint:gosec // operator-controlled toggle
	}
	return httpclient.SafeClientWithTLS(timeout, cfg)
}

// buildTLSConfig assembles the tls.Config from the forwarder's
// tls_skip_verify + ca_cert_pem columns. When both are empty we return
// nil (the std-lib TLS dialer falls back to the system roots).
func buildTLSConfig(sub sqlc.SiemForwarder) (*tls.Config, error) {
	cfg := &tls.Config{InsecureSkipVerify: sub.TlsSkipVerify} //nolint:gosec // operator-controlled toggle
	if strings.TrimSpace(sub.CaCertPem) != "" {
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM([]byte(sub.CaCertPem)); !ok {
			return nil, errors.New("ca_cert_pem could not be parsed")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

func rowIDs(rows []sqlc.SiemForwardQueue) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// tryLock is the non-blocking variant of sync.Mutex.Lock. Returns
// false when the lock is already held.
func tryLock(m *sync.Mutex) bool {
	// Go 1.18+ exposes (*sync.Mutex).TryLock; we use it directly.
	return m.TryLock()
}
