package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Schedule only inside the declared window; finish its requests in a bounded,
// separately reported drain. Parent cancellation still cancels all work.
func runEstatePhase(ctx context.Context, c *config, token string, duration time.Duration, rec *recorder, log *slog.Logger, metrics func(context.Context, context.Context, time.Time)) (estateWindow, estateWindow) {
	start := time.Now().UTC()
	deadline := start.Add(duration)
	schedule, cancelSchedule := context.WithDeadline(ctx, deadline)
	defer cancelSchedule()
	requests, cancelRequests := context.WithDeadline(ctx, deadline.Add(30*time.Second))
	defer cancelRequests()
	var wg sync.WaitGroup
	if metrics != nil {
		wg.Add(1)
		go func() { defer wg.Done(); metrics(schedule, requests, deadline) }()
	}
	cfg := *c
	cfg.duration = duration
	driveWorkload(schedule, requests, &cfg, token, rec, log)
	wg.Wait()
	end := time.Now().UTC()
	scheduledEnd := deadline
	if end.Before(deadline) {
		scheduledEnd = end
	}
	return estateWindow{start, scheduledEnd}, estateWindow{scheduledEnd, end}
}
func verifyEstateMetricIdentity(ctx context.Context, c *http.Client, m estateMember, token string) error {
	endpoint, err := url.Parse(m.Metrics.URL)
	if err != nil {
		return errors.New("invalid metric origin")
	}
	endpoint.Path = "/healthz"
	var health struct {
		Status    string `json:"status"`
		ClusterID string `json:"cluster_id"`
	}
	if err := estateGET(ctx, c, endpoint.String(), token, &health, false); err != nil {
		return err
	}
	if health.Status != "ok" || health.ClusterID != m.ClusterID {
		return errors.New("metrics origin does not identify the declared member")
	}
	return nil
}
