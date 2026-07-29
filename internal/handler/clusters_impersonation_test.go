package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// impersonationFlagQuerier adds the two capabilities the flag gate asserts for
// — user lookup (superuser check) and cluster health (agent capability
// advertisement) — on top of the shared cluster querier fake.
type impersonationFlagQuerier struct {
	*fakeAutoAttachClusterQuerier
	user       sqlc.User
	conditions []byte
	updated    sqlc.UpdateClusterParams
}

func (q *impersonationFlagQuerier) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return q.user, nil
}

func (q *impersonationFlagQuerier) GetClusterHealthStatus(context.Context, uuid.UUID) (sqlc.ClusterHealthStatus, error) {
	return sqlc.ClusterHealthStatus{Conditions: q.conditions}, nil
}

func (q *impersonationFlagQuerier) UpdateCluster(_ context.Context, arg sqlc.UpdateClusterParams) (sqlc.Cluster, error) {
	q.updated = arg
	return sqlc.Cluster{ID: arg.ID, Annotations: arg.Annotations}, nil
}

func newImpersonationFlagQuerier(t *testing.T, clusterID uuid.UUID, storedAnnotations string) *impersonationFlagQuerier {
	t.Helper()
	base := newFakeAutoAttachClusterQuerier()
	base.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Annotations: json.RawMessage(storedAnnotations)}
	return &impersonationFlagQuerier{fakeAutoAttachClusterQuerier: base}
}

func updateClusterRequest(t *testing.T, clusterID uuid.UUID, annotations string, caller sqlc.User) *http.Request {
	t.Helper()
	body := []byte(`{"display_name":"edited","annotations":` + annotations + `}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/clusters/"+clusterID.String()+"/", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", clusterID.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = middleware.SetAuthenticatedUserForTest(ctx, &middleware.AuthenticatedUser{ID: caller.ID.String()})
	return req.WithContext(ctx)
}

// TestDownstreamImpersonationFlagIsSuperuserWriteOnly covers §8 item 7's write
// gate end to end through the Update handler.
//
// PRE-FIX BEHAVIOUR: `clusters.annotations` was a free-form JSONB blob written
// wholesale by any caller who could PUT the cluster — there was no per-key gate
// at all, so had the mode been stored there without this guard, any cluster
// editor could have set it.
func TestDownstreamImpersonationFlagIsSuperuserWriteOnly(t *testing.T) {
	const enforceAdvertised = `{"enabled_features":["impersonation"]}`

	tests := []struct {
		name       string
		stored     string
		incoming   string
		superuser  bool
		conditions string
		wantStatus int
		wantMode   string
		// wantAnnotations, when set, is the exact persisted blob. Used by the
		// data-loss cases where the mode alone is not the whole story.
		wantAnnotations string
	}{
		{
			name:       "superuser may enable attribute",
			stored:     `{}`,
			incoming:   `{"astronomer.io/downstream-impersonation":"attribute"}`,
			superuser:  true,
			wantStatus: http.StatusOK,
			wantMode:   "attribute",
		},
		{
			name:       "non-superuser may not enable it",
			stored:     `{}`,
			incoming:   `{"astronomer.io/downstream-impersonation":"attribute"}`,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "non-superuser may not turn it off either",
			stored:     `{"astronomer.io/downstream-impersonation":"attribute"}`,
			incoming:   `{"astronomer.io/downstream-impersonation":"off"}`,
			wantStatus: http.StatusForbidden,
		},
		{
			// The UI PUTs the whole annotation blob. Omitting the key must not
			// silently clear a superuser's setting, and must not 403 an
			// ordinary editor who is only renaming the cluster.
			name:       "omitting the key preserves the stored value for a non-superuser",
			stored:     `{"astronomer.io/downstream-impersonation":"attribute"}`,
			incoming:   `{"owner":"sre"}`,
			wantStatus: http.StatusOK,
			wantMode:   "attribute",
		},
		{
			// REGRESSION: the preserve branch used to round-trip through
			// map[string]string, and clusterAnnotations returns an EMPTY map on
			// any unmarshal failure — so a single non-string value destroyed the
			// caller's whole blob and persisted only the mode key.
			name:            "preserving the mode does not destroy non-string annotations",
			stored:          `{"astronomer.io/downstream-impersonation":"attribute"}`,
			incoming:        `{"owner":"sre","replicas":3}`,
			wantStatus:      http.StatusOK,
			wantMode:        "attribute",
			wantAnnotations: `{"astronomer.io/downstream-impersonation":"attribute","owner":"sre","replicas":3}`,
		},
		{
			// Same shape with the flag off: the caller's bytes must come back
			// UNTOUCHED, which is the default path for every cluster.
			name:            "a non-string annotation round-trips untouched when the mode is off",
			stored:          `{}`,
			incoming:        `{"owner":"sre","replicas":3}`,
			wantStatus:      http.StatusOK,
			wantMode:        "off",
			wantAnnotations: `{"owner":"sre","replicas":3}`,
		},
		{
			// REGRESSION: clusterAnnotations(json.RawMessage("null")) returns a
			// NIL map (unmarshalling `null` into a map succeeds), and the
			// preserve branch then assigned into it — panicking on a live
			// authenticated route, upstream of the superuser gate.
			name:       "a null annotation blob is rejected, not a nil-map panic",
			stored:     `{"astronomer.io/downstream-impersonation":"attribute"}`,
			incoming:   `null`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:            "a null annotation blob is accepted unchanged when the mode is off",
			stored:          `{}`,
			incoming:        `null`,
			wantStatus:      http.StatusOK,
			wantMode:        "off",
			wantAnnotations: `null`,
		},
		{
			// The key present with a non-string value must not slip past the
			// gate by being unrepresentable in map[string]string.
			name:       "a non-string mode value is rejected",
			stored:     `{}`,
			incoming:   `{"astronomer.io/downstream-impersonation":3}`,
			superuser:  true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "an invalid value is rejected, not coerced to off",
			stored:     `{}`,
			incoming:   `{"astronomer.io/downstream-impersonation":"enfroce"}`,
			superuser:  true,
			wantStatus: http.StatusBadRequest,
		},
		{
			// The capability handshake: without an advertisement, enforce is
			// refused even for a superuser. In Phase 0 no agent can advertise
			// it, so this branch is unreachable in practice — which is the
			// intended fail-closed state.
			name:       "enforce is refused when the agent has not advertised the capability",
			stored:     `{}`,
			incoming:   `{"astronomer.io/downstream-impersonation":"enforce"}`,
			superuser:  true,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "enforce is allowed once the agent advertises it",
			stored:     `{}`,
			incoming:   `{"astronomer.io/downstream-impersonation":"enforce"}`,
			superuser:  true,
			conditions: enforceAdvertised,
			wantStatus: http.StatusOK,
			wantMode:   "enforce",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterID := uuid.New()
			q := newImpersonationFlagQuerier(t, clusterID, tt.stored)
			q.user = sqlc.User{ID: uuid.New(), IsSuperuser: tt.superuser}
			q.conditions = []byte(tt.conditions)

			h := NewClusterHandler(q)
			w := httptest.NewRecorder()
			h.Update(w, updateClusterRequest(t, clusterID, tt.incoming, q.user))

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			if got := clusterDownstreamImpersonationMode(q.updated.Annotations); got != tt.wantMode {
				t.Fatalf("persisted mode = %q, want %q (annotations=%s)", got, tt.wantMode, q.updated.Annotations)
			}
			if tt.wantAnnotations != "" && string(q.updated.Annotations) != tt.wantAnnotations {
				t.Fatalf("persisted annotations = %s, want %s", q.updated.Annotations, tt.wantAnnotations)
			}
		})
	}
}

// preReadFailingQuerier fails the Update handler's pre-read of the stored
// annotations while leaving every other query working.
type preReadFailingQuerier struct {
	*impersonationFlagQuerier
	err error
}

func (q *preReadFailingQuerier) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, q.err
}

// TestDownstreamImpersonationGateFailsClosedOnReadError is the fail-open
// regression.
//
// PRE-FIX: the guard ran inside `if existing, gerr := GetClusterByID(...);
// gerr == nil { ... }`, so ANY error from that one read — pool exhaustion,
// context deadline, replica lag — skipped the superuser check, the capability
// check and the validation entirely, and the caller's raw annotation blob went
// straight into UpdateCluster. A non-superuser could set `enforce` by racing a
// DB hiccup.
func TestDownstreamImpersonationGateFailsClosedOnReadError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "infrastructure fault is a 500, not a free pass", err: errors.New("connection pool exhausted"), wantStatus: http.StatusInternalServerError},
		{name: "a vanished row is a 404", err: pgx.ErrNoRows, wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterID := uuid.New()
			base := newImpersonationFlagQuerier(t, clusterID, `{}`)
			base.user = sqlc.User{ID: uuid.New(), IsSuperuser: false}
			q := &preReadFailingQuerier{impersonationFlagQuerier: base, err: tt.err}

			h := NewClusterHandler(q)
			w := httptest.NewRecorder()
			h.Update(w, updateClusterRequest(t, clusterID,
				`{"astronomer.io/downstream-impersonation":"enforce"}`, base.user))

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.wantStatus, w.Body.String())
			}
			if base.updated.Annotations != nil {
				t.Fatalf("ungated write reached UpdateCluster: %s", base.updated.Annotations)
			}
		})
	}
}

// TestDownstreamImpersonationDefaultsOffOnTheResponse pins the read surface:
// a cluster nobody has touched reports "off".
func TestDownstreamImpersonationDefaultsOffOnTheResponse(t *testing.T) {
	resp := clusterToResponse(sqlc.Cluster{ID: uuid.New(), Annotations: json.RawMessage(`{"owner":"sre"}`)})
	if resp.DownstreamImpersonation != string(callerid.ModeOff) {
		t.Fatalf("downstream_impersonation = %q, want off", resp.DownstreamImpersonation)
	}
}
