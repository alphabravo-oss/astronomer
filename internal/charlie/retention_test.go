package charlie

import (
	"context"
	"testing"
	"time"
)

type retentionFake struct {
	expireAt, findingsBefore, sessionsBefore time.Time
	calls                                    int
	err                                      error
}

func (f *retentionFake) ExpireCharlieFindings(_ context.Context, at time.Time) (int64, error) {
	f.calls++
	f.expireAt = at
	return 1, f.err
}
func (f *retentionFake) RevokeExpiredCharlieDelegations(context.Context) (int64, error) {
	f.calls++
	return 1, f.err
}
func (f *retentionFake) DeleteCharlieFindingMetadataBefore(_ context.Context, before time.Time) (int64, error) {
	f.calls++
	f.findingsBefore = before
	return 1, f.err
}
func (f *retentionFake) DeleteCharlieSessionMetadataBefore(_ context.Context, before time.Time) (int64, error) {
	f.calls++
	f.sessionsBefore = before
	return 1, f.err
}

func TestCharlieRetentionIsActiveOnlyAndPreservesConfiguredWindows(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	store := &retentionFake{}
	service := NewRetentionService(store, func() bool { return true })
	service.now = func() time.Time { return now }
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.calls != 4 || !store.expireAt.Equal(now) || !store.findingsBefore.Equal(now.Add(-CharlieFindingMetadataRetention)) || !store.sessionsBefore.Equal(now.Add(-CharlieSessionMetadataRetention)) {
		t.Fatalf("retention boundaries are wrong: %#v", store)
	}
	inert := &retentionFake{}
	if err := NewRetentionService(inert, func() bool { return false }).Run(context.Background()); err != nil || inert.calls != 0 {
		t.Fatalf("disabled Charlie performed retention work: err=%v calls=%d", err, inert.calls)
	}
}
