package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/siem"
)

// fakeSIEMQuerier is a minimal SIEMQuerier for the dispatcher tests.
type fakeSIEMQuerier struct {
	mu              sync.Mutex
	forwarders      []sqlc.SiemForwarder
	queue           map[uuid.UUID][]sqlc.SiemForwardQueue
	deleted         [][]int64
	incremented     [][]int64
	statusCalls     []sqlc.UpsertSIEMForwarderStatusParams
	oldDeleteCutoff time.Time
	oldDeleteCount  int64
}

func (f *fakeSIEMQuerier) ListEnabledSIEMForwarders(_ context.Context) ([]sqlc.SiemForwarder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.SiemForwarder, len(f.forwarders))
	copy(out, f.forwarders)
	return out, nil
}

func (f *fakeSIEMQuerier) ListSIEMQueueBatch(_ context.Context, arg sqlc.ListSIEMQueueBatchParams) ([]sqlc.SiemForwardQueue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.queue[arg.ForwarderID]
	if int(arg.Limit) < len(rows) {
		rows = rows[:arg.Limit]
	}
	out := make([]sqlc.SiemForwardQueue, len(rows))
	copy(out, rows)
	return out, nil
}

func (f *fakeSIEMQuerier) ListSIEMQueueExhausted(_ context.Context, arg sqlc.ListSIEMQueueExhaustedParams) ([]sqlc.SiemForwardQueue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sqlc.SiemForwardQueue
	for _, r := range f.queue[arg.ForwarderID] {
		if r.Attempts >= arg.Attempts {
			out = append(out, r)
		}
		if int32(len(out)) >= arg.Limit {
			break
		}
	}
	return out, nil
}

func (f *fakeSIEMQuerier) DeleteSIEMQueueByIDs(_ context.Context, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]int64, len(ids))
	copy(cp, ids)
	f.deleted = append(f.deleted, cp)
	// Apply the delete to the in-memory queue.
	dropped := map[int64]struct{}{}
	for _, id := range ids {
		dropped[id] = struct{}{}
	}
	for fwd, rows := range f.queue {
		var keep []sqlc.SiemForwardQueue
		for _, r := range rows {
			if _, drop := dropped[r.ID]; !drop {
				keep = append(keep, r)
			}
		}
		f.queue[fwd] = keep
	}
	return nil
}

func (f *fakeSIEMQuerier) IncrementSIEMQueueAttempts(_ context.Context, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]int64, len(ids))
	copy(cp, ids)
	f.incremented = append(f.incremented, cp)
	dropped := map[int64]struct{}{}
	for _, id := range ids {
		dropped[id] = struct{}{}
	}
	for fwd, rows := range f.queue {
		for i := range rows {
			if _, hit := dropped[rows[i].ID]; hit {
				rows[i].Attempts++
			}
		}
		f.queue[fwd] = rows
	}
	return nil
}

func (f *fakeSIEMQuerier) CountSIEMQueueByForwarder(_ context.Context, id uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.queue[id])), nil
}

func (f *fakeSIEMQuerier) UpsertSIEMForwarderStatus(_ context.Context, arg sqlc.UpsertSIEMForwarderStatusParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls = append(f.statusCalls, arg)
	return nil
}

func (f *fakeSIEMQuerier) DeleteSIEMQueueOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.oldDeleteCutoff = cutoff
	return f.oldDeleteCount, nil
}

// fakeTransport records sent batches.
type fakeTransport struct {
	mu     sync.Mutex
	sent   [][][]byte
	err    error
	closed bool
}

func (t *fakeTransport) Send(_ context.Context, batch [][]byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return t.err
	}
	cp := make([][]byte, len(batch))
	for i, b := range batch {
		cp[i] = make([]byte, len(b))
		copy(cp[i], b)
	}
	t.sent = append(t.sent, cp)
	return nil
}

func (t *fakeTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	return nil
}

func TestDispatcher_BatchSendThenDelete(t *testing.T) {
	fid := uuid.New()
	fwd := sqlc.SiemForwarder{
		ID:        fid,
		Name:      "splunk-primary",
		Transport: siem.TransportSplunkHEC,
		Endpoint:  "https://splunk:8088",
		Format:    siem.FormatNDJSONID,
		BatchSize: 10,
		Enabled:   true,
	}
	q := &fakeSIEMQuerier{
		forwarders: []sqlc.SiemForwarder{fwd},
		queue: map[uuid.UUID][]sqlc.SiemForwardQueue{
			fid: {
				queueRow(1, fid, "audit.user.login"),
				queueRow(2, fid, "audit.user.logout"),
				queueRow(3, fid, "audit.cluster.delete"),
			},
		},
	}
	transport := &fakeTransport{}
	ConfigureSIEM(SIEMDeps{
		Queries: q,
		TransportFactory: func(sqlc.SiemForwarder, authBlob) (siem.Transport, error) {
			return transport, nil
		},
	})
	defer ConfigureSIEM(SIEMDeps{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	dispatchForwarder(ctx, fwd)

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.deleted) != 1 || len(q.deleted[0]) != 3 {
		t.Fatalf("expected one DELETE of 3 rows; got %v", q.deleted)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.sent) != 1 {
		t.Fatalf("expected one Send call; got %d", len(transport.sent))
	}
	if len(transport.sent[0]) != 3 {
		t.Fatalf("expected 3 formatted events in the batch; got %d", len(transport.sent[0]))
	}
	// Confirm the dispatcher updated status with dispatched_total = 3
	// and cleared last_error.
	foundOK := false
	for _, s := range q.statusCalls {
		if s.ForwarderID == fid && s.DispatchedTotal == 3 && s.LastError == "" {
			foundOK = true
			break
		}
	}
	if !foundOK {
		t.Errorf("expected a success status upsert with dispatched_total=3; calls=%+v", q.statusCalls)
	}
}

func TestDispatcher_KeepsRowsOnFailure(t *testing.T) {
	fid := uuid.New()
	fwd := sqlc.SiemForwarder{
		ID:        fid,
		Name:      "broken-sink",
		Transport: siem.TransportSyslogTCP,
		Endpoint:  "127.0.0.1:1",
		Format:    siem.FormatRFC5424ID,
		BatchSize: 10,
		Enabled:   true,
	}
	q := &fakeSIEMQuerier{
		forwarders: []sqlc.SiemForwarder{fwd},
		queue: map[uuid.UUID][]sqlc.SiemForwardQueue{
			fid: {queueRow(7, fid, "audit.x"), queueRow(8, fid, "audit.y")},
		},
	}
	transport := &fakeTransport{err: errors.New("connection refused")}
	ConfigureSIEM(SIEMDeps{
		Queries: q,
		TransportFactory: func(sqlc.SiemForwarder, authBlob) (siem.Transport, error) {
			return transport, nil
		},
	})
	defer ConfigureSIEM(SIEMDeps{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	dispatchForwarder(ctx, fwd)

	q.mu.Lock()
	defer q.mu.Unlock()
	// No deletes after a failed send.
	if len(q.deleted) != 0 {
		t.Errorf("expected no deletes on failure; got %v", q.deleted)
	}
	// Attempts should have been bumped.
	if len(q.incremented) != 1 || len(q.incremented[0]) != 2 {
		t.Errorf("expected IncrementAttempts on 2 rows; got %v", q.incremented)
	}
	// Status should carry last_error.
	foundErr := false
	for _, s := range q.statusCalls {
		if s.ForwarderID == fid && s.LastError != "" {
			foundErr = true
			break
		}
	}
	if !foundErr {
		t.Errorf("expected a failure status upsert with last_error; calls=%+v", q.statusCalls)
	}
}

func TestDispatcher_DropsRowsPastRetryCap(t *testing.T) {
	fid := uuid.New()
	fwd := sqlc.SiemForwarder{
		ID:        fid,
		Name:      "stuck",
		Transport: siem.TransportSyslogUDP,
		Endpoint:  "127.0.0.1:1",
		BatchSize: 10,
		Enabled:   true,
	}
	q := &fakeSIEMQuerier{
		forwarders: []sqlc.SiemForwarder{fwd},
		queue: map[uuid.UUID][]sqlc.SiemForwardQueue{
			fid: {
				{ID: 1, ForwarderID: fid, EventName: "audit.x", Payload: json.RawMessage(`{}`), Attempts: SIEMRetryCap + 5},
				{ID: 2, ForwarderID: fid, EventName: "audit.y", Payload: json.RawMessage(`{}`), Attempts: 1},
			},
		},
	}
	transport := &fakeTransport{}
	ConfigureSIEM(SIEMDeps{
		Queries: q,
		TransportFactory: func(sqlc.SiemForwarder, authBlob) (siem.Transport, error) {
			return transport, nil
		},
	})
	defer ConfigureSIEM(SIEMDeps{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	dispatchForwarder(ctx, fwd)

	q.mu.Lock()
	defer q.mu.Unlock()
	// First DELETE evicts the exhausted row; second DELETE is the
	// post-send cleanup of the survivor.
	if len(q.deleted) < 1 {
		t.Fatalf("expected at least one DELETE; got %v", q.deleted)
	}
	// One of the deletes must contain row ID 1 (the exhausted one).
	deletedExhausted := false
	for _, ids := range q.deleted {
		for _, id := range ids {
			if id == 1 {
				deletedExhausted = true
			}
		}
	}
	if !deletedExhausted {
		t.Errorf("expected row 1 (past retry cap) to be deleted; got %v", q.deleted)
	}
}

func TestDispatcher_CleanupOldUsesRetentionWindow(t *testing.T) {
	q := &fakeSIEMQuerier{oldDeleteCount: 42}
	ConfigureSIEM(SIEMDeps{Queries: q})
	defer ConfigureSIEM(SIEMDeps{})

	if err := HandleSIEMCleanupOld(context.Background(), nil); err != nil {
		t.Fatalf("HandleSIEMCleanupOld: %v", err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.oldDeleteCutoff.IsZero() {
		t.Fatalf("expected DeleteSIEMQueueOlderThan to be called with a cutoff")
	}
	// Cutoff should be ~7 days ago.
	delta := time.Since(q.oldDeleteCutoff)
	if delta < SIEMQueueRetention-time.Minute || delta > SIEMQueueRetention+time.Minute {
		t.Errorf("cutoff = %v, want ~7d ago", q.oldDeleteCutoff)
	}
}

func TestDispatcher_PerForwarderLockSerializes(t *testing.T) {
	fid := uuid.New()
	l := lockForForwarder(fid)
	l.Lock()
	defer l.Unlock()
	// Second TryLock should fail because we still hold the lock.
	l2 := lockForForwarder(fid)
	if l2.TryLock() {
		t.Errorf("expected lock to be held; TryLock succeeded")
		l2.Unlock()
	}
}

func queueRow(id int64, fid uuid.UUID, name string) sqlc.SiemForwardQueue {
	return sqlc.SiemForwardQueue{
		ID:          id,
		ForwarderID: fid,
		EventName:   name,
		Payload:     json.RawMessage(`{"event_name":"` + name + `","timestamp":"2026-05-12T13:55:22Z"}`),
		Severity:    "info",
		CreatedAt:   time.Now(),
	}
}

// silence unused linter warnings on pgtype import when no other test uses it.
var _ = pgtype.Timestamptz{}

// TestHTTPClientForForwarder_ReusesPooledClient pins the keep-alive fix: the
// HEC/NDJSON forwarders must reuse a single pooled *http.Client across drains
// instead of allocating a fresh *http.Transport (and a full TLS handshake) on
// every 2s tick. The client is only rebuilt when a TLS-relevant field changes.
func TestHTTPClientForForwarder_ReusesPooledClient(t *testing.T) {
	// Start from a clean cache.
	ConfigureSIEM(SIEMDeps{})

	fid := uuid.New()
	fwd := sqlc.SiemForwarder{ID: fid, TimeoutSeconds: 10}

	c1 := httpClientForForwarder(fwd, 10*time.Second)
	c2 := httpClientForForwarder(fwd, 10*time.Second)
	if c1 != c2 {
		t.Fatal("expected the same pooled *http.Client to be reused across drains")
	}

	// A TLS-relevant change must rebuild the client so stale config is not
	// silently reused.
	fwd.TlsSkipVerify = true
	c3 := httpClientForForwarder(fwd, 10*time.Second)
	if c3 == c1 {
		t.Fatal("expected a new client after tls_skip_verify changed")
	}

	// Different forwarder id => different client.
	other := sqlc.SiemForwarder{ID: uuid.New(), TimeoutSeconds: 10}
	if httpClientForForwarder(other, 10*time.Second) == c1 {
		t.Fatal("expected distinct clients per forwarder id")
	}
}
