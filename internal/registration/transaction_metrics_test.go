package registration

import "testing"

type capturedPhaseHook struct{ captures, publications, direct int }

func (m *capturedPhaseHook) CapturePhaseTransition(string, string, string) func() {
	m.captures++
	return func() { m.publications++ }
}
func (m *capturedPhaseHook) RecordPhaseTransition(string, string, string) { m.direct++ }
func (*capturedPhaseHook) RecordDuration(string, string, bool, float64)   {}

func TestBufferedPhaseMetricsCaptureBeforeFlush(t *testing.T) {
	hook := &capturedPhaseHook{}
	service := New(nil, nil)
	service.SetMetricsHook(hook)
	scoped, flush := service.Buffered(nil)
	scoped.metrics.RecordPhaseTransition("owned", "created", "connected")
	if hook.captures != 1 || hook.publications != 0 || hook.direct != 0 {
		t.Fatalf("precommit hook=%+v", hook)
	}
	flush()
	if hook.publications != 1 || hook.direct != 0 {
		t.Fatalf("committed publication hook=%+v", hook)
	}
	rolledBack, _ := service.Buffered(nil)
	rolledBack.metrics.RecordPhaseTransition("owned", "connected", "provisioning")
	if hook.captures != 2 || hook.publications != 1 {
		t.Fatalf("discarded transaction published: %+v", hook)
	}
}
