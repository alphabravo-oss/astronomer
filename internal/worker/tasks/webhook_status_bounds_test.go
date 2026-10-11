package tasks

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/webhook"
)

func TestWebhookDispatchRejectsInvalidResponseStatus(t *testing.T) {
	statuses := []int{-1, 1, 99, 1000, math.MaxInt}
	if strconv.IntSize == 64 {
		large := int64(1<<32) + 200
		statuses = append(statuses, int(large))
	}
	for _, status := range statuses {
		for _, attempts := range []int{0, 4} {
			t.Run(strconv.Itoa(status)+"/attempts-"+strconv.Itoa(attempts), func(t *testing.T) {
				id := uuid.New()
				q := &fakeWebhookQuerier{}
				sender := &fakeWebhookSender{out: webhook.Outcome{Status: status}}
				runtime := setupDispatch(t, sender, q)
				runtime.dispatchWebhook(context.Background(), newDelivery(t, id, attempts), webhook.Subscription{MaxRetries: 5})
				if len(q.delivered) != 0 {
					t.Fatal("invalid remote status was delivered")
				}
				if attempts == 0 {
					if len(q.failed) != 1 || len(q.dropped) != 0 {
						t.Fatalf("expected one retry: failed=%d dropped=%d", len(q.failed), len(q.dropped))
					}
					got := q.failed[0]
					if got.ResponseStatus != 0 || got.Attempts != 1 || !got.NextAttemptAt.Valid || !strings.Contains(got.LastError, "invalid webhook response status") {
						t.Fatalf("invalid status persisted or retry evidence lost: %+v", got)
					}
				} else {
					if len(q.dropped) != 1 || len(q.failed) != 0 {
						t.Fatalf("expected exhausted retry: failed=%d dropped=%d", len(q.failed), len(q.dropped))
					}
					got := q.dropped[0]
					if got.ResponseStatus != 0 || got.Attempts != 5 || !strings.Contains(got.LastError, "invalid webhook response status") {
						t.Fatalf("invalid status persisted or dropped evidence lost: %+v", got)
					}
				}
			})
		}
	}
}
