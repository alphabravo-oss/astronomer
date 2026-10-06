package observation

import (
	"context"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func (t *Tracker) Watch(ctx context.Context, opts metav1.ListOptions, call func(context.Context, metav1.ListOptions) (watch.Interface, error)) (watch.Interface, error) {
	t.mu.Lock()
	repair := !t.candidate.IsZero() && t.opts.Now().Sub(t.candidate)+t.opts.WatchTimeout+t.opts.ListTimeout+t.opts.RolloverGrace >= t.opts.MaxAge
	if repair {
		now := t.opts.Now()
		grace := now.Before(t.graceUntil)
		live := t.state == protocol.ObservationCurrent && (t.active || grace) || t.state == protocol.ObservationDisconnected && grace
		retain := live && now.Sub(t.candidate) <= t.opts.MaxAge && t.store != nil && t.matches(t.store.List())
		t.repairing = true
		t.repairUntil = now.Add(t.opts.ListTimeout + t.opts.RolloverGrace)
		if retain {
			t.state = protocol.ObservationCurrent
		} else if t.state == protocol.ObservationCurrent {
			t.state = protocol.ObservationDisconnected
			if now.Sub(t.candidate) > t.opts.MaxAge {
				t.state = protocol.ObservationStale
			}
		}
	}
	t.mu.Unlock()
	if repair {
		err := apierrors.NewResourceExpired("observation source requires progress verification")
		t.recordWatchEvent("repair")
		return nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, t.opts.WatchTimeout)
	opts.AllowWatchBookmarks = true
	seconds := int64(t.opts.WatchTimeout / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	opts.TimeoutSeconds = &seconds
	source, err := call(bounded, opts)
	if err != nil {
		cancel()
		t.recordRequest("watch", t.failed(err))
		return nil, err
	}
	t.mu.Lock()
	t.active = true
	t.repairing = false
	t.mu.Unlock()
	t.recordRequest("watch", "success")
	result := &trackedWatch{source: source, events: make(chan watch.Event), cancel: cancel, done: make(chan struct{})}
	go t.forward(ctx, bounded, result)
	return result, nil
}

type trackedWatch struct {
	source watch.Interface
	events chan watch.Event
	cancel context.CancelFunc
	once   sync.Once
	done   chan struct{}
}

func (w *trackedWatch) stop()                          { w.once.Do(func() { w.cancel(); w.source.Stop() }) }
func (w *trackedWatch) Stop()                          { w.stop(); <-w.done }
func (w *trackedWatch) ResultChan() <-chan watch.Event { return w.events }

func (t *Tracker) forward(parent, ctx context.Context, w *trackedWatch) {
	defer close(w.done)
	defer close(w.events)
	defer w.stop()
	for {
		select {
		case <-ctx.Done():
			t.closed(parent, ctx)
			return
		case event, ok := <-w.source.ResultChan():
			if !ok {
				t.closed(parent, ctx)
				return
			}
			t.observe(event)
			select {
			case w.events <- event:
			case <-ctx.Done():
				t.closed(parent, ctx)
				return
			}
		}
	}
}

func (t *Tracker) observe(event watch.Event) {
	if event.Type == watch.Error {
		t.recordWatchEvent(t.failed(apierrors.FromObject(event.Object)))
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Events cannot repair a failed/partial LIST: the complete expected set is
	// unknown. The reflector will recover it through its normal relist lifecycle.
	if t.expected == nil || (t.state != protocol.ObservationCurrent && t.state != protocol.ObservationDisconnected) {
		return
	}
	switch event.Type {
	case watch.Added, watch.Modified, watch.Deleted:
		key, value, err := fingerprint(event.Object)
		if err != nil {
			t.state = protocol.ObservationUnavailable
			return
		}
		if event.Type == watch.Deleted {
			delete(t.expected, key)
		} else {
			t.expected[key] = value
		}
	case watch.Bookmark:
	default:
		return
	}
	if accessor, err := meta.Accessor(event.Object); err == nil {
		t.state = protocol.ObservationCurrent
		t.rv = accessor.GetResourceVersion()
		t.candidate = t.opts.Now()
	}
}

func (t *Tracker) closed(parent, ctx context.Context) {
	t.mu.Lock()
	t.active = false
	outcome := "closed"
	switch {
	case parent.Err() != nil:
		t.state = protocol.ObservationDisconnected
		t.repairing = false
		t.graceUntil = time.Time{}
		outcome = "canceled"
	case ctx.Err() == context.DeadlineExceeded:
		// Deliberate rollover preserves the old timestamp. Clean server EOF below
		// also gets bounded grace, but requires resumed progress to recover.
		t.graceUntil = t.opts.Now().Add(t.opts.RolloverGrace)
		outcome = "rollover"
	default:
		if t.state == protocol.ObservationCurrent {
			t.state = protocol.ObservationDisconnected
			if ctx.Err() == nil {
				t.graceUntil = t.opts.Now().Add(t.opts.RolloverGrace)
			} else {
				t.graceUntil = time.Time{}
			}
		}
	}
	t.mu.Unlock()
	t.recordWatchEvent(outcome)
}

func (t *Tracker) recordWatchEvent(outcome string) {
	if t.opts.RecordWatchEvent != nil {
		t.opts.RecordWatchEvent(t.kind, outcome)
	}
}
