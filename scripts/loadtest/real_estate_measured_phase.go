package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"
)

type estatePhaseTotals struct {
	Requested int `json:"requested"`
	Scheduled int `json:"scheduled"`
	Completed int `json:"completed"`
	Success   int `json:"success"`
	Failed    int `json:"failed"`
}

func estateTotals(spec estatePhaseSpec, rows []estateRequestResult) estatePhaseTotals {
	totals := estatePhaseTotals{Requested: int(math.Ceil(spec.RPS * float64(spec.MeasurementSeconds)))}
	for _, row := range rows {
		totals.Scheduled += row.Scheduled
		totals.Completed += row.Completed
		totals.Success += row.Success
		totals.Failed += row.Failed
	}
	return totals
}

type estateMeasuredPhase struct {
	Totals      estatePhaseTotals     `json:"request_totals"`
	Spec        estatePhaseSpec       `json:"definition"`
	Warmup      estateWindow          `json:"warmup"`
	WarmupDrain estateWindow          `json:"warmup_request_drain"`
	Measurement estateWindow          `json:"measurement"`
	Drain       estateWindow          `json:"measurement_request_drain"`
	WarmupHTTP  []estateRequestResult `json:"warmup_http"`
	HTTP        []estateRequestResult `json:"http"`
	Metrics     []estateMetricReport  `json:"member_metrics"`
	ScopeChecks []estateScopeCheck    `json:"search_scope_checks,omitempty"`
	Problems    []string              `json:"problems"`
}

func runMeasuredEstatePhase(parent context.Context, c *config, m estateManifest, spec estatePhaseSpec, token, searchToken string, metricTokens []string, client *http.Client, log *slog.Logger) estateMeasuredPhase {
	result := estateMeasuredPhase{Spec: spec, Problems: []string{}}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	cfg := *c
	cfg.workloadClient = client
	cfg.rps = 0
	cfg.workloadRPS = spec.RPS
	catalog := estateRequestCatalog(m, spec)
	if spec.Mode == "search" {
		token = searchToken
	}
	checkScope := func(checkCtx context.Context) bool {
		err := verifyEstateSearch(checkCtx, client, c.server, token, m.Search)
		if err != nil && checkCtx.Err() != nil {
			return false
		}
		result.ScopeChecks = append(result.ScopeChecks, estateScopeCheck{At: time.Now().UTC(), OK: err == nil})
		if err != nil {
			result.Problems = append(result.Problems, err.Error())
			cancel()
			return false
		}
		return true
	}
	if spec.Mode == "search" && !checkScope(ctx) {
		return result
	}
	// One scope monitor for the entire phase, including warmup. It is joined
	// before postflight; report mutation has one owner at a time.
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	var monitor sync.WaitGroup
	if spec.Mode == "search" {
		monitor.Add(1)
		go func() {
			defer monitor.Done()
			monitorEstateSearch(monitorCtx, time.Minute, checkScope)
		}()
	}
	run := func(duration time.Duration, measured bool) (estateWindow, estateWindow) {
		recorder := newEstateRequestRecorder(catalog)
		if len(catalog) > 0 {
			cfg.workloadRequest = func(reqCtx context.Context, index uint64) {
				i := int(index % uint64(len(catalog)))
				if failure := doEstateRequest(reqCtx, client, c.server, token, catalog[i], i, recorder); failure != "" && spec.Mode == "search" {
					cancel()
				}
			}
		}
		var metrics func(context.Context, context.Context, time.Time)
		if measured {
			result.Metrics = make([]estateMetricReport, len(m.Members))
			metrics = func(schedule, requests context.Context, deadline time.Time) {
				var wg sync.WaitGroup
				for i, member := range m.Members {
					result.Metrics[i].Member = member.Name
					result.Metrics[i].TargetSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(member.Metrics.URL+"\x00"+member.Metrics.InstanceID)))
					wg.Add(1)
					go func() {
						defer wg.Done()
						collectEstateMetrics(schedule, requests, deadline, client, member, metricTokens[i], &result.Metrics[i])
					}()
				}
				wg.Wait()
			}
		}
		window, drain := runEstatePhase(ctx, &cfg, token, duration, newRecorder(), log, metrics)
		if measured {
			result.HTTP = recorder.results()
		} else {
			result.WarmupHTTP = recorder.results()
		}
		return window, drain
	}
	result.Warmup, result.WarmupDrain = run(time.Duration(spec.WarmupSeconds)*time.Second, false)
	if ctx.Err() == nil {
		result.Measurement, result.Drain = run(time.Duration(spec.MeasurementSeconds)*time.Second, true)
	}
	stopMonitor()
	monitor.Wait()
	result.Totals = estateTotals(spec, result.HTTP)
	if spec.Mode == "search" && parent.Err() == nil {
		checkScope(parent)
	}
	if ctx.Err() != nil {
		result.Problems = append(result.Problems, "phase interrupted")
	}
	return result
}

func monitorEstateSearch(ctx context.Context, interval time.Duration, check func(context.Context) bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !check(ctx) {
				return
			}
		}
	}
}
