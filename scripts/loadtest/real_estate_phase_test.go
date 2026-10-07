package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRealEstateNormalDeadlineDrainsRequests(t *testing.T) {
	scheduleEnded := make(chan struct{})
	c := estateHTTPClient()
	c.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
		<-scheduleEnded
		if r.Context().Err() != nil {
			t.Error("normal scheduling deadline canceled in-flight request")
		}
		return estateResponse(map[string]any{"data": []any{}}), nil
	})
	cfg := &config{server: "https://api.test", rps: 1, workloadClient: c, fixtureClusterIDs: []string{estateTestManifest().Members[0].ClusterID}}
	rec := newRecorder()
	window, drain := runEstatePhase(context.Background(), cfg, "", 30*time.Millisecond, rec, slog.New(slog.NewTextHandler(io.Discard, nil)), func(schedule, requests context.Context, _ time.Time) {
		<-schedule.Done()
		if requests.Err() != nil {
			t.Error("request context did not outlive scheduler")
		}
		close(scheduleEnded)
	})
	if window.End.Sub(window.Start) != 30*time.Millisecond || drain.Start != window.End || drain.End.Before(drain.Start) {
		t.Fatal("window/drain accounting wrong")
	}
	total := 0
	for _, v := range rec.httpCount {
		total += v
	}
	if total != 1 {
		t.Fatal("expected one started request")
	}
	for _, v := range rec.httpErrors {
		if v != 0 {
			t.Fatal("normal deadline recorded error")
		}
	}
}
func TestRealEstateLateMetricSampleExcluded(t *testing.T) {
	m := estateTestManifest().Members[0]
	schedule, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := estateHTTPClient()
	deadline := time.Now().Add(30 * time.Millisecond)
	c.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/healthz" {
			return estateResponse(map[string]any{"status": "ok", "cluster_id": m.ClusterID}), nil
		}
		timer := time.AfterFunc(max(0, time.Until(deadline)), cancel)
		defer timer.Stop()
		<-schedule.Done()
		if r.Context().Err() != nil {
			t.Error("scrape canceled at scheduling boundary")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(estateMetricFixture(time.Now())))), Header: http.Header{}}, nil
	})
	r := estateMetricReport{}
	collectEstateMetrics(schedule, context.Background(), deadline, c, m, "", &r)
	if r.Errors != 0 || r.Samples != 0 || r.Attempts != 0 || r.BoundarySkipped != 1 {
		t.Fatalf("late sample counted in measurement: %+v", r)
	}
}
func TestRealEstateMetricAliasesCannotImpersonateTwoMembers(t *testing.T) {
	m := estateTestManifest()
	c := estateHTTPClient()
	calls := 0
	c.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer metrics-only" {
			t.Fatal("metric credential not isolated")
		}
		return estateResponse(map[string]any{"status": "ok", "cluster_id": m.Members[0].ClusterID}), nil
	})
	if err := verifyEstateMetricIdentity(context.Background(), c, m.Members[0], "metrics-only"); err != nil {
		t.Fatal(err)
	}
	if err := verifyEstateMetricIdentity(context.Background(), c, m.Members[1], "metrics-only"); err == nil {
		t.Fatal("second URL alias attributed same endpoint to another member")
	}
	if calls != 2 {
		t.Fatal("identity not verified per target")
	}
}
