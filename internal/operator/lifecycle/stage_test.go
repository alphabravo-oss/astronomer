package lifecycle

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type testRun struct{ trail []string }

type recordingObserver struct {
	mu       sync.Mutex
	outcomes []string
}

func (o *recordingObserver) StageStarted(context.Context, string, string, string) {}
func (o *recordingObserver) StageFinished(_ context.Context, _, stage, _ string, outcome Outcome, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.outcomes = append(o.outcomes, stage+"="+string(outcome))
}

func step(name string, result Step, err error) Stage[testRun] {
	return Stage[testRun]{Name: name, FailureReason: "Reason_" + name, Run: func(_ context.Context, s *testRun) (Step, error) {
		s.trail = append(s.trail, name)
		return result, err
	}}
}

func newTestPipeline(t *testing.T, observer Observer, failures *[]string, stages ...Stage[testRun]) *Pipeline[testRun] {
	t.Helper()
	p, err := NewPipeline(PipelineConfig[testRun]{
		Controller: "test",
		Stages:     stages,
		NewState:   func(reconcile.Request) *testRun { return &testRun{} },
		OnFailure: func(_ context.Context, s *testRun, reason string, err error) error {
			*failures = append(*failures, reason+":"+err.Error()+":"+strings.Join(s.trail, ","))
			return nil
		},
		Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var testReq = reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "a"}}

func TestNewPipelineRejectsInvalidRegistrations(t *testing.T) {
	t.Parallel()
	ok := step("a", Next(), nil)
	cases := map[string]PipelineConfig[testRun]{
		"no controller": {Stages: []Stage[testRun]{ok}, NewState: func(reconcile.Request) *testRun { return nil }},
		"no state":      {Controller: "c", Stages: []Stage[testRun]{ok}},
		"no stages":     {Controller: "c", NewState: func(reconcile.Request) *testRun { return nil }},
		"unnamed":       {Controller: "c", Stages: []Stage[testRun]{{Run: ok.Run}}, NewState: func(reconcile.Request) *testRun { return nil }},
		"no run":        {Controller: "c", Stages: []Stage[testRun]{{Name: "a"}}, NewState: func(reconcile.Request) *testRun { return nil }},
		"duplicate":     {Controller: "c", Stages: []Stage[testRun]{ok, ok}, NewState: func(reconcile.Request) *testRun { return nil }},
	}
	for name, cfg := range cases {
		if _, err := NewPipeline(cfg); err == nil {
			t.Errorf("%s: expected registration error", name)
		}
	}
}

func TestPipelineRunsStagesInOrderUntilDone(t *testing.T) {
	t.Parallel()
	observer := &recordingObserver{}
	var failures []string
	p := newTestPipeline(t, observer, &failures,
		step("one", Next(), nil),
		step("two", Done(reconcile.Result{RequeueAfter: time.Minute}), nil),
		step("three", Next(), nil),
	)
	if got := strings.Join(p.StageNames(), ","); got != "one,two,three" {
		t.Fatalf("stage names = %s", got)
	}
	res, err := p.Reconcile(context.Background(), testReq)
	if err != nil || res.RequeueAfter != time.Minute {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if got := strings.Join(observer.outcomes, ","); got != "one=continue,two=done" {
		t.Fatalf("outcomes = %s", got)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures %v", failures)
	}
}

func TestPipelineStageErrorStopsChainRecordsReasonAndRequeues(t *testing.T) {
	t.Parallel()
	observer := &recordingObserver{}
	var failures []string
	boom := errors.New("boom")
	p := newTestPipeline(t, observer, &failures,
		step("one", Next(), nil),
		step("two", Step{}, boom),
		step("three", Next(), nil),
	)
	_, err := p.Reconcile(context.Background(), testReq)
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "stage two") {
		t.Fatalf("err = %v, want wrapped boom naming the stage", err)
	}
	if got := strings.Join(observer.outcomes, ","); got != "one=continue,two=error" {
		t.Fatalf("outcomes = %s", got)
	}
	if len(failures) != 1 || failures[0] != "Reason_two:boom:one,two" {
		t.Fatalf("failures = %v", failures)
	}
}

func TestPipelinePanicIsAnErrorAndReleasesTheLock(t *testing.T) {
	t.Parallel()
	observer := &recordingObserver{}
	var failures []string
	locks := &KeyedLock{}
	panicked := false
	p, err := NewPipeline(PipelineConfig[testRun]{
		Controller: "test",
		NewState:   func(reconcile.Request) *testRun { return &testRun{} },
		Locks:      locks,
		Observer:   observer,
		OnFailure: func(_ context.Context, _ *testRun, reason string, _ error) error {
			failures = append(failures, reason)
			return nil
		},
		Stages: []Stage[testRun]{{Name: "explode", FailureReason: "Exploded", Run: func(context.Context, *testRun) (Step, error) {
			if !panicked {
				panicked = true
				panic("kaboom")
			}
			return Done(reconcile.Result{}), nil
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Reconcile(context.Background(), testReq); err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("err = %v, want panic surfaced as error", err)
	}
	if locks.Tracked() != 0 {
		t.Fatal("lock still held after panic")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := p.Reconcile(ctx, testReq); err != nil {
		t.Fatalf("reconcile after panic: %v", err)
	}
	if got := strings.Join(observer.outcomes, ","); got != "explode=panic,explode=done" {
		t.Fatalf("outcomes = %s", got)
	}
	if len(failures) != 1 || failures[0] != "Exploded" {
		t.Fatalf("failures = %v", failures)
	}
}

func TestPipelineCancelledContextStopsBetweenStagesAndReleases(t *testing.T) {
	t.Parallel()
	locks := &KeyedLock{}
	ctx, cancel := context.WithCancel(context.Background())
	ran := []string{}
	p, err := NewPipeline(PipelineConfig[testRun]{
		Controller: "test",
		NewState:   func(reconcile.Request) *testRun { return &testRun{} },
		Locks:      locks,
		Stages: []Stage[testRun]{
			{Name: "first", Run: func(context.Context, *testRun) (Step, error) {
				ran = append(ran, "first")
				cancel()
				return Next(), nil
			}},
			{Name: "second", Run: func(context.Context, *testRun) (Step, error) { ran = append(ran, "second"); return Next(), nil }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Reconcile(ctx, testReq); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if strings.Join(ran, ",") != "first" {
		t.Fatalf("ran = %v", ran)
	}
	if locks.Tracked() != 0 {
		t.Fatal("lock still held after cancellation")
	}
}
