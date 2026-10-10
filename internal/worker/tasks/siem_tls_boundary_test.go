package tasks

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/siem"
)

func TestSIEMHTTPSFactoryRejectsMalformedCustomCA(t *testing.T) {
	for _, mode := range []string{siem.TransportSplunkHEC, siem.TransportNDJSONHTTPS} {
		for _, skip := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/verify", true: "/explicit-skip"}[skip], func(t *testing.T) {
				runtime := DispatchRuntime{}.normalized()
				forwarder := sqlc.SiemForwarder{ID: uuid.New(), Transport: mode, Endpoint: "https://example.invalid/", CaCertPem: "malformed-ca-fixture", TlsSkipVerify: skip}
				transport, err := runtime.defaultSIEMTransportFactory(forwarder, authBlob{})
				if transport != nil {
					defer func() {
						if closeErr := transport.Close(); closeErr != nil {
							t.Error(closeErr)
						}
					}()
				}
				if err == nil || transport != nil {
					t.Fatalf("malformed custom CA accepted: transport=%T error=%v", transport, err)
				}
				if strings.Contains(err.Error(), forwarder.CaCertPem) {
					t.Fatal("configuration error exposed CA input")
				}
				if len(runtime.siemState.httpClients) != 0 {
					t.Fatal("invalid TLS configuration cached a client")
				}
			})
		}
	}
}

func TestSIEMMalformedCustomCACannotDeliverOrDeletePendingQueue(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	forwarder := sqlc.SiemForwarder{ID: uuid.New(), Name: "tls-boundary", Transport: siem.TransportNDJSONHTTPS, Endpoint: server.URL, TlsSkipVerify: true, CaCertPem: "malformed-ca-fixture", TimeoutSeconds: 2}
	q := &fakeSIEMQuerier{queue: map[uuid.UUID][]sqlc.SiemForwardQueue{forwarder.ID: {queueRow(1, forwarder.ID, "tls.boundary")}}}
	runtime := DispatchRuntime{SIEM: SIEMDeps{Queries: q}}.normalized()
	runtime.dispatchSIEMForwarder(context.Background(), forwarder)
	if requests.Load() != 0 || len(q.queue[forwarder.ID]) != 1 || len(q.deleted) != 0 || len(q.succeededTests) != 0 || len(q.incremented) != 0 {
		t.Fatalf("invalid CA crossed delivery boundary: requests=%d pending=%d deletes=%d success=%d attempts=%d", requests.Load(), len(q.queue[forwarder.ID]), len(q.deleted), len(q.succeededTests), len(q.incremented))
	}
	if len(q.statusCalls) != 1 || q.statusCalls[0].LastError != "transport: ca_cert_pem could not be parsed" {
		t.Fatalf("missing safe retryable configuration status: %+v", q.statusCalls)
	}
	if len(runtime.siemState.httpClients) != 0 {
		t.Fatal("failed CA policy cached a client")
	}

	// Recovery must use the corrected policy, rather than an invalid cached
	// fallback. A custom CA verifies the actual peer without skip verification.
	forwarder.CaCertPem = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	forwarder.TlsSkipVerify = false
	runtime.dispatchSIEMForwarder(context.Background(), forwarder)
	if requests.Load() != 1 || len(q.queue[forwarder.ID]) != 0 || len(q.deleted) != 1 || len(q.succeededTests) != 1 {
		t.Fatal("valid custom CA did not recover pending delivery")
	}

	// A subsequent malformed policy cannot reuse the old valid client's pool.
	q.queue[forwarder.ID] = []sqlc.SiemForwardQueue{queueRow(2, forwarder.ID, "tls.boundary")}
	forwarder.CaCertPem = "malformed-ca-fixture"
	runtime.dispatchSIEMForwarder(context.Background(), forwarder)
	if requests.Load() != 1 || len(q.queue[forwarder.ID]) != 1 || len(q.deleted) != 1 {
		t.Fatal("invalid changed policy reused a previous valid client")
	}

	// Explicit operator skip with no custom CA remains a supported mode.
	forwarder.CaCertPem = ""
	forwarder.TlsSkipVerify = true
	runtime.dispatchSIEMForwarder(context.Background(), forwarder)
	if requests.Load() != 2 || len(q.queue[forwarder.ID]) != 0 || len(q.deleted) != 2 {
		t.Fatal("explicit skip without custom CA changed behavior")
	}
}
