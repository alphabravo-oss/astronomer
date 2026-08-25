package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

func scheduleReconnectStorm(ctx context.Context, agents []*syntheticAgent, storm reconnectStormConfig, duration time.Duration, log *slog.Logger) {
	at := storm.AtDuration
	if at <= 0 {
		at = duration / 3
	}
	if at <= 0 {
		at = 30 * time.Second
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(at):
	}

	limit := len(agents)
	if storm.BatchPercent > 0 && storm.BatchPercent < 100 {
		limit = maxInt(1, len(agents)*storm.BatchPercent/100)
	}
	jitter := storm.JitterDuration
	if jitter <= 0 {
		jitter = 15 * time.Second
	}
	log.Warn("triggering reconnect storm", "agents", limit, "jitter", jitter)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < limit; i++ {
		agent := agents[i]
		delay := time.Duration(rng.Int63n(int64(jitter)))
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(delay):
				agent.CloseForStorm()
			}
		}()
	}
}

func waitForSyntheticAgents(ctx context.Context, agents []*syntheticAgent) error {
	for _, agent := range agents {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-agent.ready:
		}
	}
	return nil
}

// emitSyntheticStateEvents drives the profile's estate-wide eventsPerSecond
// declaration over the same authenticated tunnels used by the synthetic
// agents. The rate is global (not multiplied by cluster count) and successful
// writes are counted so certification can reject a shortfall.
func emitSyntheticStateEvents(ctx context.Context, agents []*syntheticAgent, eventsPerSecond int, rec *recorder, log *slog.Logger) {
	if eventsPerSecond <= 0 || len(agents) == 0 {
		return
	}
	const ticksPerSecond = 10
	ticker := time.NewTicker(time.Second / ticksPerSecond)
	defer ticker.Stop()
	var sequence uint64
	credit := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			credit += eventsPerSecond
			batch := credit / ticksPerSecond
			credit -= batch * ticksPerSecond
			emitted := 0
			for i := 0; i < batch; i++ {
				agent := agents[sequence%uint64(len(agents))]
				sequence++
				if agent.sendSyntheticStateUpdate(ctx, sequence) {
					emitted++
				}
			}
			rec.RecordStateEvents(emitted)
			if emitted < batch {
				log.Debug("synthetic state-event batch partially emitted", "expected", batch, "emitted", emitted)
			}
		}
	}
}

func (sa *syntheticAgent) cannedK8sBody(req *tunnelMessage) string {
	var request struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(req.Payload, &request)
	kind, apiVersion, prefix, count := "PodList", "v1", "pod", maxInt(0, sa.resources.PodsPerCluster)
	switch {
	case strings.Contains(request.Path, "/deployments"):
		kind, apiVersion, prefix, count = "DeploymentList", "apps/v1", "deployment", maxInt(0, sa.resources.DeploymentsPerCluster)
	case strings.Contains(request.Path, "/services"):
		kind, apiVersion, prefix, count = "ServiceList", "v1", "service", maxInt(0, sa.resources.ServicesPerCluster)
	case strings.Contains(request.Path, "/events"):
		kind, apiVersion, prefix, count = "EventList", "v1", "event", maxInt(1, minInt(sa.resources.EventsPerSecond, 1000))
	}
	items := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		item := map[string]any{
			"metadata": map[string]any{
				"name": fmt.Sprintf("%s-%05d", prefix, i), "namespace": fmt.Sprintf("ns-%02d", i%100),
				"resourceVersion": fmt.Sprintf("%d", i+1),
				"labels":          map[string]string{"app": fmt.Sprintf("app-%03d", i%250), "profile": sa.resources.ProfileName},
			},
		}
		switch kind {
		case "PodList":
			item["status"] = map[string]any{"phase": "Running"}
		case "DeploymentList":
			item["spec"] = map[string]any{"replicas": 3}
			item["status"] = map[string]any{"readyReplicas": 3}
		case "ServiceList":
			item["spec"] = map[string]any{"clusterIP": fmt.Sprintf("10.96.%d.%d", (i/250)%250, i%250+1), "ports": []map[string]any{{"port": 80}}}
		case "EventList":
			item["type"] = "Normal"
			item["reason"] = "LoadTest"
			item["message"] = "synthetic cardinality event"
		}
		items = append(items, item)
	}
	sa.rec.RecordResourceCardinality(kind, count)
	body, _ := json.Marshal(map[string]any{
		"kind":       kind,
		"apiVersion": apiVersion,
		"items":      items,
	})
	return string(body)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// driveWorkload maintains cfg.rps requests per second until scheduleCtx is
// done. Requests retain requestCtx so the declared window does not cancel
// already-started work; all in-flight work is joined before returning.
func driveWorkload(scheduleCtx, requestCtx context.Context, cfg *config, token string, rec *recorder, log *slog.Logger) {
	limiter := rate.NewLimiter(rate.Limit(cfg.rps), cfg.rps)
	scs := defaultScenarios()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	client := &http.Client{Timeout: 30 * time.Second}
	var sequence atomic.Uint64
	var inflight sync.WaitGroup
	defer inflight.Wait()
	for {
		if err := limiter.Wait(scheduleCtx); err != nil {
			return
		}
		sc := pickScenario(scs, rng.Float64())
		clusterID := ""
		if len(cfg.fixtureClusterIDs) > 0 {
			clusterID = cfg.fixtureClusterIDs[(sequence.Add(1)-1)%uint64(len(cfg.fixtureClusterIDs))]
		}
		inflight.Add(1)
		go func() {
			defer inflight.Done()
			doRequest(requestCtx, client, cfg.server, token, sc, clusterID, rec)
		}()
	}
}

func doRequest(ctx context.Context, client *http.Client, server, token string, sc scenario, clusterID string, rec *recorder) {
	if strings.Contains(sc.path, fixtureClusterPathToken) && clusterID == "" {
		rec.RecordHTTP(sc.name, 0, 0, fmt.Errorf("scenario requires a provisioned fixture cluster ID"))
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+sc.pathForCluster(clusterID), nil)
	if err != nil {
		rec.RecordHTTP(sc.name, 0, 0, err)
		return
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		rec.RecordHTTP(sc.name, 0, elapsed, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	rec.RecordHTTP(sc.name, resp.StatusCode, elapsed, nil)
}
