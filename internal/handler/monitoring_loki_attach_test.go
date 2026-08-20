package handler

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

func fatSingleBinaryAttachInput(clusters int) sizerEvalInput {
	in := sizerOKStorage()
	in.NodeCount = 1
	in.ReadySchedulableCount = 1
	in.CPUAllocatableMillicores = 16_000
	in.MemoryAllocatableBytes = 64 << 30
	in.CPURequestsMillicores = 3_000
	in.MemoryRequestsBytes = 6 << 30
	in.ConnectedClusters = clusters
	return in
}

func TestEvaluateLokiAttachCapacity(t *testing.T) {
	t.Parallel()

	t.Run("pass under singleBinary cap", func(t *testing.T) {
		code, msg, ok := evaluateLokiAttachCapacity(sizerModeSingleBinary, fatSingleBinaryAttachInput(3), true)
		if !ok {
			t.Fatalf("ok=false code=%s msg=%s", code, msg)
		}
	})

	t.Run("sixth cluster exceeds singleBinary", func(t *testing.T) {
		code, _, ok := evaluateLokiAttachCapacity(sizerModeSingleBinary, fatSingleBinaryAttachInput(5), true)
		if ok {
			t.Fatal("want ingest_cap_exceeded for 6th cluster on singleBinary")
		}
		if code != apierror.IngestCapExceeded {
			t.Fatalf("code = %s, want %s", code, apierror.IngestCapExceeded)
		}
	})

	t.Run("already-connected cluster does not double count", func(t *testing.T) {
		_, _, ok := evaluateLokiAttachCapacity(sizerModeSingleBinary, fatSingleBinaryAttachInput(5), false)
		if !ok {
			t.Fatal("attaching an already-connected cluster at the cap should still pass")
		}
	})

	t.Run("observed ingest at 80 percent of budget", func(t *testing.T) {
		in := fatSingleBinaryAttachInput(2)
		// 80% of SingleBinary 8 MB/s = 6.4 MB/s → 6.4e6 * 86400 bytes/day
		in.ObservedLogBytesPerDay = int64(6.4 * 1_000_000 * 86400)
		code, _, ok := evaluateLokiAttachCapacity(sizerModeSingleBinary, in, true)
		if ok {
			t.Fatal("want ingest_cap_exceeded at 80% global budget")
		}
		if code != apierror.IngestCapExceeded {
			t.Fatalf("code = %s, want %s", code, apierror.IngestCapExceeded)
		}
	})
}
