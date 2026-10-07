package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func registerEstateFlags(c *config) {
	flag.StringVar(&c.realEstate, "real-estate", "", "engineering-only pre-provisioned real-estate manifest")
	flag.BoolVar(&c.checkOnly, "check-only", false, "validate real-estate manifest/config without network or credentials")
	flag.DurationVar(&c.warmup, "warmup", 5*time.Minute, "real-estate warmup, separate from measurement")
}
func collectEstateMixedFlags(c *config) {
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "rps" || f.Name == "duration" || f.Name == "warmup" || f.Name == "clusters" || f.Name == "metrics-server" || f.Name == "skip-agents" || f.Name == "keep-fixtures" || f.Name == "profile" || f.Name == "certification" {
			c.estateMixedFlags = append(c.estateMixedFlags, f.Name)
		}
	})
	for _, key := range []string{"LOADTEST_RPS", "LOADTEST_DURATION", "LOADTEST_CLUSTERS", "LOADTEST_METRICS_SERVER", "LOADTEST_SKIP_AGENTS", "LOADTEST_KEEP_FIXTURES", "LOADTEST_PROFILE", "LOADTEST_CERTIFICATION"} {
		if os.Getenv(key) != "" {
			c.estateMixedFlags = append(c.estateMixedFlags, key)
		}
	}
}
func runRealEstate(c *config, log *slog.Logger) error {
	if err := validateEstateConfig(c); err != nil {
		return err
	}
	m, digest, err := loadEstateManifest(c.realEstate)
	if err != nil {
		return err
	}
	if c.checkOnly {
		log.Info("real estate manifest valid; no network or credential access; live qualification NOT_RUN")
		return nil
	}
	token, err := loadToken(c.tokenPath)
	if err != nil || token == "" {
		return errors.New("real estate requires readable nonempty API token file")
	}
	metricTokens := make([]string, len(m.Members))
	for i, member := range m.Members {
		metricTokens[i], err = loadToken(member.Metrics.TokenFile)
		if err != nil {
			return errors.New("cannot load metric target token")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return executeRealEstate(ctx, c, m, digest, token, metricTokens, estateHTTPClient(), log)
}
func executeRealEstate(ctx context.Context, c *config, m estateManifest, digest, token string, metricTokens []string, client *http.Client, log *slog.Logger) error {
	r := newEstateReport(m, digest, c)
	r.Preflight.Start = time.Now().UTC()
	base := strings.TrimRight(c.server, "/")
	verify := func() ([]estateVerification, error) {
		verifyCtx, cancelVerify := context.WithTimeout(ctx, 15*time.Minute)
		defer cancelVerify()
		results := []estateVerification{}
		for i, member := range m.Members {
			if err := verifyEstateMetricIdentity(verifyCtx, client, member, metricTokens[i]); err != nil {
				return results, fmt.Errorf("%s: metric identity unavailable", member.Name)
			}
			v, err := verifyEstateMember(verifyCtx, client, base, token, member, time.Now())
			results = append(results, v)
			if err != nil {
				return results, fmt.Errorf("%s: %w", member.Name, err)
			}
		}
		return results, nil
	}
	var err error
	r.StartVerification, err = verify()
	r.Preflight.End = time.Now().UTC()
	if err != nil {
		r.Problems = append(r.Problems, err.Error())
		return finishEstateReport(c.outPath, r)
	}
	searchToken := token
	if m.Search != nil && m.Search.TokenFile != "" {
		searchToken, err = loadToken(m.Search.TokenFile)
		if err != nil || searchToken == "" {
			r.Problems = append(r.Problems, "search token unavailable")
			return finishEstateReport(c.outPath, r)
		}
	}
	phaseConfig := *c
	phaseConfig.server = base
	for _, spec := range m.Phases {
		phase := runMeasuredEstatePhase(ctx, &phaseConfig, m, spec, token, searchToken, metricTokens, client, log)
		r.Phases = append(r.Phases, phase)
		if len(phase.Problems) > 0 || ctx.Err() != nil {
			break
		}
	}
	r.EndVerification, err = verify()
	if err != nil {
		r.Problems = append(r.Problems, err.Error())
	}
	return finishEstateReport(c.outPath, r)
}

func collectEstateMetrics(ctx, requests context.Context, deadline time.Time, c *http.Client, m estateMember, token string, r *estateMetricReport) {
	ticker := time.NewTicker(metricsScrape)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		err := verifyEstateMetricIdentity(requests, c, m, token)
		var raw []byte
		if err == nil {
			raw, err = readEstateMetrics(requests, c, m.Metrics.URL, token)
		}
		if time.Now().After(deadline) {
			r.BoundarySkipped++
			return
		}
		r.Attempts++
		if err == nil {
			var points []estatePoint
			points, err = parseEstateMetrics(raw, m.Metrics.InstanceID)
			if err == nil {
				r.observe(raw, points, time.Now().UTC())
			}
		}
		if err != nil {
			r.recordScrapeError()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func readEstateMetrics(ctx context.Context, c *http.Client, endpoint, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("invalid metric request")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, errors.New("metric transport failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, errors.New("metric scrape failed")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, errors.New("metric read failed or exceeds bound")
	}
	return raw, nil
}
