package observation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Options is injected at composition, not read from hidden environment settings.
// Defaults bound quiet-source freshness to four minutes. Quiet streams are proactively repaired before
// the next bounded watch could outlive that deadline.
type Options struct {
	Now                                              func() time.Time
	ListTimeout, WatchTimeout, MaxAge, RolloverGrace time.Duration
	RecordRequest                                    func(kind Kind, verb, outcome string)
	RecordWatchEvent                                 func(kind Kind, outcome string)
}

type Tracker struct {
	mu                  sync.Mutex
	kind                Kind
	opts                Options
	store               cache.Store
	synced              func() bool
	expected, pages     map[string][32]byte
	pageRV              string
	rv                  string
	candidate, verified time.Time
	state               protocol.ObservationState
	active              bool
	graceUntil          time.Time
	repairing           bool
	repairUntil         time.Time
}

func NewTracker(kind Kind, opts Options) *Tracker {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ListTimeout <= 0 {
		opts.ListTimeout = 15 * time.Second
	}
	if opts.WatchTimeout <= 0 {
		opts.WatchTimeout = 75 * time.Second
	}
	if opts.MaxAge <= 0 {
		opts.MaxAge = 4 * time.Minute
	}
	if opts.RolloverGrace <= 0 {
		opts.RolloverGrace = 5 * time.Second
	}
	return &Tracker{kind: kind, opts: opts, state: protocol.ObservationUnsynced}
}

func (t *Tracker) Bind(store cache.Store, synced func() bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.store = store
	t.synced = synced
}
func (t *Tracker) recordRequest(verb, outcome string) {
	if t.opts.RecordRequest != nil {
		t.opts.RecordRequest(t.kind, verb, outcome)
	}
}

// List stores only compact object identities, not a second payload cache. A
// complete paginated LIST is a candidate until the informer applies its objects.
func (t *Tracker) List(ctx context.Context, opts metav1.ListOptions, call func(context.Context, metav1.ListOptions) (runtime.Object, error)) (runtime.Object, error) {
	t.mu.Lock()
	if opts.Continue == "" {
		t.pages = make(map[string][32]byte)
		t.pageRV = ""
		if !t.repairing {
			t.state = protocol.ObservationUnsynced
		}
	}
	t.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, t.opts.ListTimeout)
	defer cancel()
	result, err := call(bounded, opts)
	if err != nil {
		t.recordRequest("list", t.failed(err))
		return nil, err
	}
	t.recordRequest("list", "success")
	items, err := meta.ExtractList(result)
	if err != nil {
		t.failed(err)
		return nil, err
	}
	accessor, err := meta.ListAccessor(result)
	if err != nil {
		t.failed(err)
		return nil, err
	}
	t.mu.Lock()
	if opts.Continue != "" && (t.pages == nil || t.pageRV != accessor.GetResourceVersion()) {
		t.mu.Unlock()
		err := fmt.Errorf("inconsistent observation LIST continuation")
		t.failed(err)
		return nil, err
	}
	t.pageRV = accessor.GetResourceVersion()
	for _, item := range items {
		key, value, err := fingerprint(item)
		if err != nil {
			t.mu.Unlock()
			t.failed(err)
			return nil, err
		}
		if previous, exists := t.pages[key]; exists && previous != value {
			t.mu.Unlock()
			err := fmt.Errorf("conflicting observation LIST identity")
			t.failed(err)
			return nil, err
		}
		t.pages[key] = value
	}
	if accessor.GetContinue() == "" {
		t.expected = t.pages
		t.pages = nil
		t.rv = accessor.GetResourceVersion()
		t.candidate = t.opts.Now()
		t.state = protocol.ObservationCurrent
	}
	t.mu.Unlock()
	return result, nil
}

func fingerprint(object any) (string, [32]byte, error) {
	accessor, err := meta.Accessor(object)
	if err != nil {
		return "", [32]byte{}, err
	}
	key := accessor.GetNamespace() + "/" + accessor.GetName()
	value := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s", key, accessor.GetUID(), accessor.GetResourceVersion())))
	return key, value, nil
}

func (t *Tracker) failed(err error) string {
	state, outcome := protocol.ObservationUnavailable, "error"
	switch {
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		state, outcome = protocol.ObservationDenied, "denied"
	case apierrors.IsResourceExpired(err), apierrors.IsGone(err):
		state, outcome = protocol.ObservationStale, "expired"
	case err == context.Canceled:
		state, outcome = protocol.ObservationDisconnected, "canceled"
	}
	t.mu.Lock()
	t.state = state
	t.active = false
	t.repairing = false
	t.graceUntil = time.Time{}
	t.mu.Unlock()
	return outcome
}

func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := Snapshot{Kind: t.kind, ResourceVersion: t.rv, Observation: protocol.DeliveryObservation{State: t.state}}
	if t.store == nil {
		return out
	}
	objects := t.store.List()
	matched := t.matches(objects)
	now := t.opts.Now()
	if t.state == protocol.ObservationDisconnected && matched && now.Before(t.graceUntil) && now.Sub(t.candidate) <= t.opts.MaxAge {
		out.Observation.State = protocol.ObservationCurrent
		t.verified = t.candidate
	}
	if t.state == protocol.ObservationCurrent {
		switch {
		case !matched:
			out.Observation.State = protocol.ObservationUnsynced
		case !t.active && (!t.repairing || !now.Before(t.repairUntil)) && !now.Before(t.graceUntil):
			out.Observation.State = protocol.ObservationDisconnected
		case now.Sub(t.candidate) > t.opts.MaxAge:
			out.Observation.State = protocol.ObservationStale
		default:
			t.verified = t.candidate
		}
	}
	if !t.verified.IsZero() {
		at := t.verified
		out.Observation.ObservedAt = &at
	}
	// Protocol requires stale to carry a previously verified time. A source that
	// expires before application has never been observed and remains unsynced.
	if out.Observation.State == protocol.ObservationStale && out.Observation.ObservedAt == nil {
		out.Observation.State = protocol.ObservationUnsynced
	}
	out.copyObjects(objects)
	return out
}

// Disconnect fences retained stores even if shutdown occurs during reflector
// backoff, when there is no active watch to deliver a cancellation callback.
func (t *Tracker) Disconnect() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = protocol.ObservationDisconnected
	t.active = false
	t.repairing = false
	t.graceUntil = time.Time{}
}

// matches is called with the lifecycle mutex held; informer objects are immutable.
func (t *Tracker) matches(objects []any) bool {
	matched := t.expected != nil && len(objects) == len(t.expected) && t.synced != nil && t.synced()
	if matched {
		for _, obj := range objects {
			key, value, err := fingerprint(obj)
			expected, ok := t.expected[key]
			if err != nil || !ok || expected != value {
				matched = false
				break
			}
		}
	}
	return matched
}
