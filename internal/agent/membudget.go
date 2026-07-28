package agent

import (
	"errors"
	"io"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
)

// The agent proxies Kubernetes and service responses by buffering the whole
// body in memory before framing it. Capping ONE body (maxK8sResponseBodyBytes,
// MaxServiceProxyResponseSize) is not a memory bound: with goroutine-per-message
// dispatch, N concurrent reads buffer N caps at once, and any N large enough
// exceeds the container limit. The types here add the missing second half — an
// agent-wide ceiling on the SUM of buffered response bytes — so the per-request
// cap decides how big one answer may be and the budget decides how many of them
// may be in memory at the same time.

// errResponseBudgetExhausted is returned by readBodyWithinBudget when the
// agent-wide buffer budget cannot cover this body. Callers turn it into a
// retryable 429 rather than a hard failure: the request is not malformed, the
// agent is simply out of room right now.
var errResponseBudgetExhausted = errors.New("agent response buffer budget exhausted")

const (
	// defaultAgentMemoryLimitBytes is the ceiling assumed when neither
	// GOMEMLIMIT nor a cgroup limit is readable. Matches the shipped manifest
	// (deploy/agent/install.yaml.template resources.limits.memory: 512Mi), so
	// the derived budget on a normally-installed agent is the same whether or
	// not the cgroup filesystem is visible.
	defaultAgentMemoryLimitBytes int64 = 512 * 1024 * 1024

	// responseBudgetDivisor sets the budget at 1/4 of the memory limit. The
	// other three quarters are not spare: the informer caches (state and mirror
	// subscribers hold full objects), the outbound frame queues, the base64 and
	// JSON copies each buffered body spawns on its way to the wire, and the Go
	// heap's own slack all live there. A quarter is the largest share that
	// still leaves an OOM-free margin for the transient ~1.4x encode
	// amplification on top of every byte the budget admits.
	responseBudgetDivisor int64 = 4

	// minResponseBudgetBytes keeps a deliberately small agent usable: below
	// this the budget would reject ordinary LIST responses and the agent would
	// be uselessly safe rather than safely useful.
	minResponseBudgetBytes int64 = 32 * 1024 * 1024

	// hardMaxResponseBodyBytes is the historical per-request cap and the
	// server's own reassembly cap (tunnel.maxInternalK8sResponseBytes,
	// handler-side equivalent). The derived cap never exceeds it, so nothing
	// that fits through the server can be newly rejected here for being small
	// enough.
	hardMaxResponseBodyBytes int64 = 64 * 1024 * 1024

	// initialBodyBufferBytes / bodyBufferGrowthCapBytes bound the read
	// buffer's growth steps. Growth is geometric from the first value up to the
	// second so a small GET does not charge (or allocate) a full chunk, while a
	// multi-megabyte LIST still grows in few steps. The ceiling matches the
	// wire chunk size the body is about to be cut into.
	initialBodyBufferBytes   int64 = 32 * 1024
	bodyBufferGrowthCapBytes int64 = 256 * 1024
)

// agentResponseBudget is the process-wide buffered-response ceiling. A package
// var rather than a field so it is shared by every proxy in the process (k8s
// and service proxy buffer against the same physical memory), and so tests can
// swap in a small budget.
var agentResponseBudget = newMemBudget(deriveResponseBudgetBytes(agentMemoryLimitBytes()))

// memBudget is a counting semaphore over BYTES with a strictly non-blocking
// acquire.
//
// Non-blocking is a correctness requirement, not a simplification. If reserve
// waited for room, K concurrent readers each part-way through a large body
// would each hold a fraction of the budget and wait for the rest — none able to
// finish, none able to release. That is a deadlock with no timeout that can
// safely break it (releasing early would hand out memory that is still in use).
// Failing the reservation instead means the loser unwinds immediately, returns
// everything it held, and its caller answers with a retryable 429.
type memBudget struct {
	limit int64
	used  atomic.Int64
	// peak is the high-water mark of used, never reset. Tests assert against it
	// because sampling used() during a burst races the burst itself.
	peak atomic.Int64
}

func newMemBudget(limit int64) *memBudget {
	if limit <= 0 {
		limit = minResponseBudgetBytes
	}
	return &memBudget{limit: limit}
}

// reserve charges n bytes to the budget, reporting whether they fit.
func (b *memBudget) reserve(n int64) bool {
	if n <= 0 {
		return true
	}
	for {
		cur := b.used.Load()
		if cur+n > b.limit {
			return false
		}
		if b.used.CompareAndSwap(cur, cur+n) {
			b.observe(cur + n)
			return true
		}
	}
}

// release returns n bytes to the budget.
func (b *memBudget) release(n int64) {
	if n <= 0 {
		return
	}
	agentResponseBufferBytes.Set(float64(b.used.Add(-n)))
}

func (b *memBudget) observe(v int64) {
	agentResponseBufferBytes.Set(float64(v))
	for {
		peak := b.peak.Load()
		if v <= peak || b.peak.CompareAndSwap(peak, v) {
			return
		}
	}
}

// Used reports the currently reserved bytes.
func (b *memBudget) Used() int64 { return b.used.Load() }

// Peak reports the high-water mark of reserved bytes.
func (b *memBudget) Peak() int64 { return b.peak.Load() }

// readBodyWithinBudget reads r to EOF into a buffer whose every allocated byte
// is charged against budget, and returns the body together with the release
// func that returns that charge. release is always non-nil, so callers can
// unconditionally defer it; it is a no-op after the first call.
//
// perRequestCap is the existing single-response cap. The buffer is grown to at
// most perRequestCap+1 bytes so the caller can still distinguish "exactly at
// the cap" from "over it" — the one-byte probe io.LimitReader used to provide.
func readBodyWithinBudget(r io.Reader, perRequestCap int64, budget *memBudget) ([]byte, func(), error) {
	var (
		buf      []byte
		reserved int64
	)
	release := func() {
		budget.release(reserved)
		reserved = 0
	}
	for {
		if int64(len(buf)) == reserved {
			step := initialBodyBufferBytes
			if reserved > 0 {
				if step = reserved; step > bodyBufferGrowthCapBytes {
					step = bodyBufferGrowthCapBytes
				}
			}
			next := reserved + step
			if next > perRequestCap+1 {
				next = perRequestCap + 1
			}
			if next <= reserved {
				// perRequestCap+1 bytes buffered: the caller reports 413
				// without reading (or charging for) the rest of the body.
				return buf, release, nil
			}
			if !budget.reserve(next - reserved) {
				release()
				return nil, func() {}, errResponseBudgetExhausted
			}
			grown := make([]byte, len(buf), next)
			copy(grown, buf)
			buf = grown
			reserved = next
		}
		n, err := r.Read(buf[len(buf):reserved])
		buf = buf[:len(buf)+n]
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf, release, nil
			}
			release()
			return nil, func() {}, err
		}
	}
}

// deriveResponseBudgetBytes turns a memory limit into the buffered-response
// budget.
func deriveResponseBudgetBytes(limit int64) int64 {
	budget := limit / responseBudgetDivisor
	if budget < minResponseBudgetBytes {
		budget = minResponseBudgetBytes
	}
	return budget
}

// deriveMaxResponseBodyBytes returns the per-request cap for a given budget: a
// single response may consume the whole budget, but never more than the
// historical/server-side 64 MiB. At the shipped 512Mi limit the budget is 128
// MiB and this returns exactly the previous 64 MiB, so no request that
// succeeded before is newly rejected; on a smaller limit the cap shrinks with
// it instead of promising a body the agent cannot hold.
func deriveMaxResponseBodyBytes(budget int64) int64 {
	if budget < hardMaxResponseBodyBytes {
		return budget
	}
	return hardMaxResponseBodyBytes
}

// agentMemoryLimitBytes reports the process memory ceiling, preferring an
// explicitly configured GOMEMLIMIT over the cgroup limit (an operator who set
// GOMEMLIMIT meant it) and falling back to the shipped manifest's limit.
func agentMemoryLimitBytes() int64 {
	// SetMemoryLimit(-1) reads the current limit without changing it. Unset is
	// math.MaxInt64.
	if limit := debug.SetMemoryLimit(-1); limit > 0 && limit < math.MaxInt64 {
		return limit
	}
	if limit, ok := cgroupMemoryLimitBytes(); ok {
		return limit
	}
	return defaultAgentMemoryLimitBytes
}

// cgroupMemoryLimitBytes reads the container memory limit from cgroup v2 then
// v1. "max" (v2) and v1's near-int64-max sentinel both mean unlimited, which is
// not a useful ceiling, so they are treated as absent.
func cgroupMemoryLimitBytes() (int64, bool) {
	for _, path := range []string{
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		limit, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || limit <= 0 || limit >= int64(1)<<62 {
			continue
		}
		return limit, true
	}
	return 0, false
}
