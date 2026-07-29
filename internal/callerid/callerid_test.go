package callerid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestOriginZeroValueIsNeitherUserNorMachine is invariant §7.4 stated as a unit
// test. It is the single most important property in this package: if the zero
// value ever came to mean "machine", a route that forgot to populate identity
// would silently acquire the exemption meant for astronomer's own traffic.
func TestOriginZeroValueIsNeitherUserNorMachine(t *testing.T) {
	var zero protocol.CallerIdentity
	if zero.IsUser() {
		t.Fatal("zero identity must not be a user")
	}
	if zero.IsMachine() {
		t.Fatal("zero identity must not be machine — that is the fail-open §7.4 forbids")
	}
	if zero.Attributed() {
		t.Fatal("zero identity must report itself unattributed")
	}
	if protocol.OriginUnset.Valid() {
		t.Fatal("the unset origin is not a valid origin")
	}

	// A user subject WITHOUT an origin is still not a user: both halves are
	// required, so a partially-built identity cannot pass for a real one.
	half := protocol.CallerIdentity{User: UserSubject(uuid.New())}
	if half.IsUser() || half.IsMachine() {
		t.Fatalf("a subject with no origin is unattributed, got %+v", half)
	}
	// And an origin with no subject is likewise not a user.
	half = protocol.CallerIdentity{Origin: protocol.OriginUser}
	if half.IsUser() {
		t.Fatal("an origin with no subject is malformed, not a user")
	}
}

func TestResolvePrecedence(t *testing.T) {
	userID := uuid.New()
	userCtx := middleware.SetAuthenticatedUserForTest(context.Background(), &middleware.AuthenticatedUser{ID: userID.String()})

	t.Run("authenticated session yields the user subject", func(t *testing.T) {
		got := Resolve(userCtx)
		if got.User != UserSubject(userID) || !got.IsUser() {
			t.Fatalf("resolve = %+v", got)
		}
	})

	t.Run("machine marker beats an ambient authenticated user", func(t *testing.T) {
		// Self-management triggered from a UI click: a human is in ctx, but the
		// work is astronomer acting on itself and §10 says it must never be
		// impersonated. The positive marker is authoritative.
		got := Resolve(WithMachine(userCtx, SourceSelfManage))
		if !got.IsMachine() || got.IsUser() {
			t.Fatalf("resolve = %+v, want machine", got)
		}
		if got.User != MachineSubject(SourceSelfManage) {
			t.Fatalf("machine subject = %q", got.User)
		}
	})

	t.Run("no session and no marker is unattributed", func(t *testing.T) {
		got := Resolve(context.Background())
		if got.Attributed() {
			t.Fatalf("resolve = %+v, want unattributed", got)
		}
	})

	t.Run("nil context is unattributed, not machine", func(t *testing.T) {
		//nolint:staticcheck // deliberately passing nil to prove it fails safe.
		got := Resolve(nil)
		if got.IsMachine() || got.Attributed() {
			t.Fatalf("resolve(nil) = %+v", got)
		}
	})
}

// TestResolveNeverCarriesAClientSuppliedRequestID closes the one route by which
// a client could influence the identity envelope.
//
// PRE-FIX: RequestID was copied from middleware.GetCorrelationID, which returns
// whatever the caller put in X-Correlation-Id / X-Request-ID verbatim. Under
// Option D that value would ride downstream as
// Impersonate-Extra-Astronomer-Request and land in the apiserver audit event
// and in AdmissionReview.request.userInfo.extra — letting a caller forge the
// correlation between a downstream action and an arbitrary control-plane
// request, i.e. the exact forensic attribution this feature exists to deliver.
func TestResolveNeverCarriesAClientSuppliedRequestID(t *testing.T) {
	userID := uuid.New()
	resolveUnder := func(t *testing.T, clientCorrelationID string) (protocol.CallerIdentity, string) {
		t.Helper()
		var identity protocol.CallerIdentity
		var correlation string
		h := middleware.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			ctx := middleware.SetAuthenticatedUserForTest(r.Context(), &middleware.AuthenticatedUser{ID: userID.String()})
			identity = Resolve(ctx)
			correlation = middleware.GetCorrelationID(ctx)
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/", nil)
		if clientCorrelationID != "" {
			req.Header.Set("X-Correlation-Id", clientCorrelationID)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		return identity, correlation
	}

	t.Run("a client-chosen correlation id is dropped, not propagated", func(t *testing.T) {
		const forged = "0000-attacker-chosen-0000"
		identity, correlation := resolveUnder(t, forged)
		if identity.RequestID != "" {
			t.Fatalf("identity carried a client-supplied request id %q", identity.RequestID)
		}
		// The rest of the envelope is unaffected: the caller is still the
		// authenticated user, and the audit trail still records what the client
		// claimed. Only the identity envelope refuses it.
		if !identity.IsUser() || identity.User != UserSubject(userID) {
			t.Fatalf("identity = %+v, want the session user", identity)
		}
		if correlation != forged {
			t.Fatalf("correlation id = %q, want the client value preserved for logs/audit", correlation)
		}
	})

	t.Run("a server-minted id is carried", func(t *testing.T) {
		identity, correlation := resolveUnder(t, "")
		if identity.RequestID == "" {
			t.Fatal("a server-minted request id must be carried")
		}
		if identity.RequestID != correlation {
			t.Fatalf("request id = %q, want the minted correlation id %q", identity.RequestID, correlation)
		}
	})
}

// TestWithMachineRejectsUnknownSource proves the exemption list is the
// authority (design item 10). A source that is not in the table cannot mint a
// machine marker, so a typo degrades to "unattributed" rather than silently
// creating a new exemption.
func TestWithMachineRejectsUnknownSource(t *testing.T) {
	ctx := WithMachine(context.Background(), Source("not-in-the-table"))
	if IsMachineOrigin(ctx) {
		t.Fatal("an unknown source must not mark a context machine")
	}
	if Machine(Source("not-in-the-table")).Attributed() {
		t.Fatal("an unknown source must not mint a machine identity")
	}
}

// TestExemptionListMatchesDesignSection10 pins the list itself. If someone
// deletes an exemption, this fails; if they add one, they have to add it here
// too — which is the point of holding it as data.
func TestExemptionListMatchesDesignSection10(t *testing.T) {
	want := map[Source]bool{
		SourceArgoCDProxy:       false,
		SourceSelfManage:        false,
		SourceCrossPodTransport: true, // transport, not origin (§10.3)
		SourceKubectlShell:      false,
		SourceAgentLifecycle:    false,
		SourceBaselineReconcile: false,
	}
	if len(Exemptions()) != len(want) {
		t.Fatalf("exemption count = %d, want %d", len(Exemptions()), len(want))
	}
	for src, transport := range want {
		e, ok := Exempt(src)
		if !ok {
			t.Fatalf("exemption %q missing from the table", src)
		}
		if e.Transport != transport {
			t.Errorf("%q transport = %v, want %v", src, e.Transport, transport)
		}
		if e.Reason == "" {
			t.Errorf("%q has no reason; an exemption without a justification is not reviewable", src)
		}
	}
}

// TestFillPreservesAttributedIdentity is the rule the HA hop depends on.
func TestFillPreservesAttributedIdentity(t *testing.T) {
	existing := protocol.CallerIdentity{User: UserSubject(uuid.New()), Origin: protocol.OriginUser}
	got := Fill(WithMachine(context.Background(), SourceCrossPodTransport), existing)
	if got.User != existing.User || !got.IsUser() {
		t.Fatalf("fill overwrote an attributed identity: %+v", got)
	}
	got = Fill(WithMachine(context.Background(), SourceCrossPodTransport), protocol.CallerIdentity{})
	if !got.IsMachine() {
		t.Fatalf("fill should supply the marker for an unattributed identity, got %+v", got)
	}
}

func TestSubjectMinters(t *testing.T) {
	id := uuid.New()
	if got, want := UserSubject(id), "astronomer:user:"+id.String(); got != want {
		t.Fatalf("UserSubject = %q, want %q", got, want)
	}
	if UserSubject(uuid.Nil) != "" {
		t.Fatal("the nil uuid has no subject")
	}
	if got, want := RoleGroup("cluster-operator"), "astronomer:role:cluster-operator"; got != want {
		t.Fatalf("RoleGroup = %q, want %q", got, want)
	}
	if RoleGroup("  ") != "" {
		t.Fatal("a blank slug has no group")
	}
	if !protocol.IsUserSubject(UserSubject(id)) || protocol.IsMachineSubject(UserSubject(id)) {
		t.Fatal("subject classification is wrong")
	}
	if !protocol.IsMachineSubject(MachineSubject(SourceArgoCDProxy)) {
		t.Fatal("machine subject classification is wrong")
	}
}
