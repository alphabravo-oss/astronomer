package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type ownedTerminalPeer struct {
	meta          metav1.ObjectMeta
	deleted       bool
	gets, deletes int
	recreate      bool
	cancel        context.CancelFunc
	missing       bool
	options       metav1.DeleteOptions
}

func (peer *ownedTerminalPeer) Do(ctx context.Context, _, method, _ string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if method == http.MethodDelete {
		peer.deletes++
		if err := json.Unmarshal(body, &peer.options); err != nil {
			return nil, err
		}
		if peer.cancel != nil {
			peer.cancel()
			return nil, ctx.Err()
		}
		peer.deleted = true
		return &protocol.K8sResponsePayload{StatusCode: 202}, nil
	}
	if method != http.MethodGet {
		return nil, errors.New("unexpected terminal peer verb")
	}
	peer.gets++
	if peer.missing {
		return &protocol.K8sResponsePayload{StatusCode: 404}, nil
	}
	if peer.deleted && !peer.recreate {
		return &protocol.K8sResponsePayload{StatusCode: 404}, nil
	}
	value := peer.meta
	if peer.deleted && peer.recreate {
		value.UID = types.UID(uuid.NewString())
	}
	payload, _ := json.Marshal(corev1.PersistentVolumeClaim{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"}, ObjectMeta: value})
	return &protocol.K8sResponsePayload{StatusCode: 200, Body: base64.StdEncoding.EncodeToString(payload)}, nil
}
func terminalResourceFixture() (sqlc.VulnerabilityDatabaseScanClaim, sqlc.VulnerabilityDatabaseScanResource, *ownedTerminalPeer) {
	identifier, cluster, uid := uuid.New(), uuid.New(), uuid.New()
	name := "database-scan-" + identifier.String() + "-input"
	claim := sqlc.VulnerabilityDatabaseScanClaim{ID: identifier, ClusterID: cluster, Phase: "completed", UpdatedAt: time.Now().UTC().Add(-2 * time.Hour)}
	resource := sqlc.VulnerabilityDatabaseScanResource{ClaimID: identifier, ClusterID: cluster, Role: "input", ResourcePath: "/api/v1/namespaces/" + databasejob.ScanNamespace + "/persistentvolumeclaims/" + name, ResourceUid: pgtype.UUID{Bytes: uid, Valid: true}}
	peer := &ownedTerminalPeer{meta: metav1.ObjectMeta{Name: name, Namespace: databasejob.ScanNamespace, UID: types.UID(uid.String()), ResourceVersion: "19", Labels: map[string]string{"app.kubernetes.io/managed-by": "astronomer", "astronomer.io/database-claim": identifier.String()}}}
	return claim, resource, peer
}
func TestOwnedTerminalResourceCleanupUsesUIDVersionAndLeavesReportsUntouched(t *testing.T) {
	claim, resource, peer := terminalResourceFixture()
	ctx := callerid.WithMachine(t.Context(), callerid.SourceDatabaseScan)
	if err := removeTerminalDatabaseResource(ctx, peer, claim, resource); err != nil {
		t.Fatal(err)
	}
	if peer.deletes != 1 || peer.gets != 2 || peer.options.Preconditions == nil || peer.options.Preconditions.UID == nil || string(*peer.options.Preconditions.UID) != uuid.UUID(resource.ResourceUid.Bytes).String() || peer.options.Preconditions.ResourceVersion == nil || *peer.options.Preconditions.ResourceVersion != "19" || peer.options.PropagationPolicy == nil || *peer.options.PropagationPolicy != metav1.DeletePropagationForeground {
		t.Fatal("terminal deletion lacked exact preconditions or joined absence readback")
	}
	// This boundary has only tunnel/resource arguments: no report/CVE/witness
	// writer. Actual retained-row/rollback proofs are deferred owned PG controls.
}
func TestOwnedTerminalResourceCleanupRefusesUnknownRecreatedForeignAndCanceled(t *testing.T) {
	for _, name := range []string{"unknown-uid", "unknown-uid-and-absent", "foreign-uid", "foreign-claim-label", "foreign-cluster", "recreated-after-delete", "active-claim", "retention", "canceled", "canceled-during-delete"} {
		t.Run(name, func(t *testing.T) {
			claim, resource, peer := terminalResourceFixture()
			ctx, cancel := context.WithCancel(callerid.WithMachine(t.Context(), callerid.SourceDatabaseScan))
			defer cancel()
			switch name {
			case "unknown-uid":
				resource.ResourceUid.Valid = false
			case "unknown-uid-and-absent":
				resource.ResourceUid.Valid = false
				peer.missing = true
			case "foreign-uid":
				peer.meta.UID = types.UID(uuid.NewString())
			case "foreign-claim-label":
				peer.meta.Labels["astronomer.io/database-claim"] = uuid.NewString()
			case "foreign-cluster":
				resource.ClusterID = uuid.New()
			case "recreated-after-delete":
				peer.recreate = true
			case "active-claim":
				claim.Phase = "scanning"
			case "retention":
				claim.UpdatedAt = time.Now().UTC()
			case "canceled":
				cancel()
			case "canceled-during-delete":
				peer.cancel = cancel
			}
			err := removeTerminalDatabaseResource(ctx, peer, claim, resource)
			if err == nil {
				t.Fatal("unknown/foreign/recreated/active resource reported cleaned")
			}
			if strings.HasPrefix(name, "canceled") && !errors.Is(err, context.Canceled) {
				t.Fatal("cleanup lost cancellation identity")
			}
			if name != "recreated-after-delete" && name != "canceled-during-delete" && peer.deletes != 0 {
				t.Fatal("unowned terminal object was deleted")
			}
			if name == "recreated-after-delete" && (peer.deletes != 1 || peer.gets != 2) {
				t.Fatal("changed post-delete UID was not inspected")
			}
		})
	}
}
func TestOwnedTerminalDeleteScopeCannotPromoteUserOrBroadenResourceAndOptions(t *testing.T) {
	claim, resource, peer := terminalResourceFixture()
	scope, body, err := newDatabaseScanTerminalRequester(peer, claim.ClusterID, claim.ID, resource.ResourcePath, peer.meta.UID, peer.meta.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(body)
	machine := callerid.WithMachine(t.Context(), callerid.SourceDatabaseScan)
	for _, name := range []string{"user", "foreign-origin", "foreign-cluster", "foreign-namespace", "changed-options", "collection", "post", "headers"} {
		t.Run(name, func(t *testing.T) {
			ctx, cluster, path, method, payload, headers := machine, claim.ClusterID.String(), resource.ResourcePath, http.MethodDelete, body, map[string]string{"Content-Type": "application/json"}
			switch name {
			case "user":
				ctx = callerid.WithMachine(callerid.WithUser(t.Context(), uuid.New()), callerid.SourceDatabaseScan)
			case "foreign-origin":
				ctx = callerid.WithMachine(t.Context(), callerid.SourceAgentLifecycle)
			case "foreign-cluster":
				cluster = uuid.NewString()
			case "foreign-namespace":
				path = strings.Replace(path, databasejob.ScanNamespace, "tenant", 1)
			case "changed-options":
				payload = append(append([]byte(nil), body...), ' ')
			case "collection":
				path = path[:strings.LastIndex(path, "/")]
			case "post":
				method = http.MethodPost
			case "headers":
				headers["Authorization"] = "not-authority"
			}
			before := peer.deletes
			if _, err := scope.Do(ctx, cluster, method, path, payload, headers); err == nil || peer.deletes != before {
				t.Fatal("terminal scope was broadened by caller DATA")
			}
		})
	}
}
