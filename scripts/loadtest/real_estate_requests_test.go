package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRealEstateDeterministicAssignmentCoverage(t *testing.T) {
	for _, tier := range []int{1, 10, 100} {
		t.Run(fmt.Sprint(tier), func(t *testing.T) {
			m := estateTestManifest()
			m.Tier = tier
			for i := range m.Members {
				a := m.Members[i].Assignments[0]
				m.Members[i].Assignments = nil
				for j := 0; j < tier; j++ {
					copy := a
					copy.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", 1000+i*100+j)
					m.Members[i].Assignments = append(m.Members[i].Assignments, copy)
				}
			}
			catalog := estateRequestCatalog(m, estatePhaseSpec{Mode: "delivery"})
			if len(catalog) != len(m.Members)*(tier+2) {
				t.Fatal("catalog coverage incomplete")
			}
			counts := map[string]int{}
			for sequence := 0; sequence < len(catalog)*3; sequence++ {
				q := catalog[sequence%len(catalog)]
				counts[q.Member+q.Scenario+q.Assignment]++
				if !strings.Contains(q.Path, "project_id=") {
					t.Fatal("project scope omitted")
				}
				if q.Scenario == "delivery_detail" && !strings.Contains(q.Path, q.Assignment) {
					t.Fatal("assignment scope omitted")
				}
			}
			var mu sync.Mutex
			scheduledCoverage := map[string]int{}
			// Exercise deterministic selection through the real scheduler with
			// a generous safety deadline; wall-window behavior is tested below.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cfg := &config{duration: time.Duration(len(catalog)*2) * time.Millisecond, workloadRPS: 1000, workloadRequest: func(_ context.Context, sequence uint64) {
				q := catalog[sequence%uint64(len(catalog))]
				mu.Lock()
				scheduledCoverage[q.Member+q.Scenario+q.Assignment]++
				mu.Unlock()
				if int(sequence) == len(catalog)*2-1 {
					cancel()
				}
			}}
			driveWorkload(ctx, ctx, cfg, "", newRecorder(), slog.Default())
			cancel()
			if len(scheduledCoverage) != len(catalog) {
				t.Fatal("scheduler starved a member/assignment")
			}
			for _, n := range scheduledCoverage {
				if n < 1 || n > 2 {
					t.Fatal("scheduler exceeded deterministic catalog cycles")
				}
			}
			if len(counts) != len(catalog) {
				t.Fatal("duplicate catalog identity")
			}
			for _, n := range counts {
				if n != 3 {
					t.Fatal("nonuniform coverage")
				}
			}
			resource := estateRequestCatalog(m, estatePhaseSpec{Mode: "resources"})
			if len(resource) != 6 {
				t.Fatal("resource scope missing")
			}
			for _, q := range resource {
				if !strings.Contains(q.Path, "/namespaces/benchmark/") {
					t.Fatal("unscoped resource request")
				}
			}
		})
	}
}

type estateBrokenBody struct{}

func (estateBrokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (estateBrokenBody) Close() error             { return nil }

type estateSlowBody struct {
	once   sync.Once
	reader io.Reader
}

func (b *estateSlowBody) Read(p []byte) (int, error) {
	b.once.Do(func() { time.Sleep(25 * time.Millisecond) })
	return b.reader.Read(p)
}
func (b *estateSlowBody) Close() error { return nil }
func TestRealEstateBodyTimingAndSingleTerminalOutcome(t *testing.T) {
	for _, kind := range []string{"complete", "truncated200", "truncated503", "status503", "transport", "validation"} {
		t.Run(kind, func(t *testing.T) {
			q := estateRequest{Scenario: "test", Path: "/test", Validate: func([]byte) error {
				if kind == "validation" {
					return errors.New("invalid_body")
				}
				return nil
			}}
			rec := newEstateRequestRecorder([]estateRequest{q})
			client := estateHTTPClient()
			client.Transport = estateRoundTrip(func(*http.Request) (*http.Response, error) {
				if kind == "transport" {
					return nil, errors.New("transport failed")
				}
				status := 200
				var body io.ReadCloser = &estateSlowBody{reader: strings.NewReader("{}")}
				if strings.HasPrefix(kind, "truncated") {
					body = estateBrokenBody{}
				}
				if strings.HasSuffix(kind, "503") {
					status = 503
				}
				return &http.Response{StatusCode: status, Body: body, Header: http.Header{}}, nil
			})
			doEstateRequest(context.Background(), client, "https://api.test", "token", q, 0, rec)
			got := rec.results()[0]
			if got.Scheduled != 1 || got.Completed != 1 || got.Success+got.Failed != 1 {
				t.Fatalf("terminal accounting %+v", got)
			}
			if kind == "complete" {
				if got.Success != 1 || got.FullResponseLatency.P99UpperBoundMS == nil || got.HeaderLatency.P99UpperBoundMS == nil || *got.FullResponseLatency.P99UpperBoundMS < 25 || *got.FullResponseLatency.P99UpperBoundMS < *got.HeaderLatency.P99UpperBoundMS {
					t.Fatal("full-response latency omitted body")
				}
			} else if got.Failed != 1 {
				t.Fatal("failure counted as success")
			}
			diagnostics := 0
			for _, n := range got.Diagnostics {
				diagnostics += n
			}
			if diagnostics != got.Failed {
				t.Fatal("double-counted failure")
			}
		})
	}
}
func TestRealEstateIdleWaitsAndSchedulesNothing(t *testing.T) {
	cfg := &config{rps: 0}
	var metrics atomic.Int32
	start := time.Now()
	window, drain := runEstatePhase(context.Background(), cfg, "", 35*time.Millisecond, newRecorder(), slog.New(slog.NewTextHandler(io.Discard, nil)), func(schedule, requests context.Context, _ time.Time) { metrics.Add(1); <-schedule.Done() })
	if time.Since(start) < 35*time.Millisecond || window.End.Sub(window.Start) != 35*time.Millisecond || drain.Start != window.End || metrics.Load() != 1 {
		t.Fatal("idle measurement ended early")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	window, _ = runEstatePhase(ctx, cfg, "", time.Hour, newRecorder(), slog.Default(), nil)
	if window.End.Sub(window.Start) > time.Second {
		t.Fatal("canceled idle hung")
	}
}
func TestRealEstateFractionalSchedulerNoInitialBurst(t *testing.T) {
	var count atomic.Int32
	cfg := &config{workloadRPS: .1, duration: 1100 * time.Millisecond, workloadRequest: func(context.Context, uint64) { count.Add(1) }}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.duration)
	defer cancel()
	driveWorkload(ctx, ctx, cfg, "", newRecorder(), slog.Default())
	if count.Load() != 1 {
		t.Fatalf("fractional window emitted %d requests", count.Load())
	}
}
func TestRealEstateSchedulerDeterministicDrainAndCancellation(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelParent), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			end := make(chan struct{})
			var count atomic.Int32
			cfg := &config{workloadRPS: 100, workloadRequest: func(requestCtx context.Context, index uint64) {
				count.Add(1)
				select {
				case <-end:
					if !cancelParent && requestCtx.Err() != nil {
						t.Error("normal end canceled request")
					}
				case <-requestCtx.Done():
					if !cancelParent {
						t.Error("drain canceled request")
					}
				}
			}}
			if cancelParent {
				time.AfterFunc(20*time.Millisecond, cancel)
			}
			runEstatePhase(ctx, cfg, "", 40*time.Millisecond, newRecorder(), slog.Default(), func(schedule, requests context.Context, _ time.Time) { <-schedule.Done(); close(end) })
			if count.Load() < 1 || count.Load() > 4 {
				t.Fatalf("scheduled outside window: %d", count.Load())
			}
		})
	}
}
