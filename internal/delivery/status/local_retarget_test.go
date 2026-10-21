package status

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type retargetTransaction struct {
	*systemFakeTransaction
	retargeted bool
}

func (tx *retargetTransaction) RetargetLocalDeliverySystemAssignment(context.Context, uuid.UUID) (int64, error) {
	tx.retargeted = true
	return 1, nil
}
func TestCoalescedInventoryRetargetsLocalSystemAssignment(t *testing.T) {
	t.Parallel()
	tx := &retargetTransaction{systemFakeTransaction: &systemFakeTransaction{fakeTransaction: &fakeTransaction{unchanged: true}}}
	payload := validStatus()
	payload.Deployments = nil
	payload.StatusDigest = payload.SemanticDigest()
	if err := NewIngester(&fakeRunner{tx: tx}).Ingest(t.Context(), clusterID, connectionID, "session", payload); err != nil {
		t.Fatal(err)
	}
	if !tx.retargeted || tx.observeArg == nil {
		t.Fatal("coalesced inventory skipped system reconciliation after promotion")
	}
}
