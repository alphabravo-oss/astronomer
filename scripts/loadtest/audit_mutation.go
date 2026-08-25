package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const mandatoryAuditDrainTimeout = 2 * time.Minute

// runMandatoryAuditWorkload performs bounded, reversible metadata-only PATCHes on
// run-owned fixture clusters. Fixture decommission remains the exact cleanup.
// Each accepted request carries a unique UUID correlation ID that is later
// reconciled through the public audit API.
func runMandatoryAuditWorkload(ctx context.Context, cfg *config, token string, rec *recorder, log *slog.Logger) {
	profile := cfg.mandatoryAudit
	if profile.RatePerSecond <= 0 || profile.MaxOperations <= 0 || len(cfg.fixtureClusterIDs) == 0 {
		return
	}
	runID := uuid.NewString()
	rec.beginAuditConservation(runID)
	defer rec.endAuditConservation()
	interval := time.Second / time.Duration(profile.RatePerSecond)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	client := &http.Client{Timeout: 15 * time.Second}
	base := strings.TrimRight(cfg.server, "/")
	for sequence := 0; sequence < profile.MaxOperations; sequence++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		clusterID := cfg.fixtureClusterIDs[sequence%len(cfg.fixtureClusterIDs)]
		requestID := uuid.NewString()
		body, err := json.Marshal(map[string]any{
			"display_name": fmt.Sprintf("Load test audit %s %06d", runID[:8], sequence),
			"description":  "bounded mandatory-audit scale qualification mutation",
			"environment":  "scale", "region": "synthetic",
			"labels":      map[string]string{"astronomer.io/loadtest-audit-run": runID},
			"annotations": map[string]string{},
		})
		if err != nil {
			rec.recordAuditMutation(requestID, false, time.Time{})
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPatch, base+"/api/v1/clusters/"+clusterID+"/", bytes.NewReader(body))
		if err != nil {
			rec.recordAuditMutation(requestID, false, time.Time{})
			continue
		}
		// The mounted route is PATCH; it reaches the same transactional update
		// handler and avoids the deprecated PUT alias.
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Correlation-Id", requestID)
		requestedAt := time.Now().UTC()
		response, err := client.Do(req)
		accepted := err == nil && response.StatusCode >= 200 && response.StatusCode < 300
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		rec.recordAuditMutation(requestID, accepted, requestedAt)
		if !accepted {
			log.Warn("mandatory-audit qualification mutation rejected", "sequence", sequence, "transport_error", err != nil)
		}
	}
}

// observeMandatoryAuditIntents uses a separate, read-only PostgreSQL connection
// to prove that each accepted mutation produced a durable audit_outbox row. The
// HTTP acceptance counter is deliberately not accepted as evidence of intent
// persistence.
func observeMandatoryAuditIntents(ctx context.Context, cfg *config, rec *recorder) error {
	if strings.TrimSpace(cfg.auditObserverPath) == "" {
		return fmt.Errorf("LOADTEST_AUDIT_OBSERVER_DATABASE_URL_FILE is required")
	}
	rawDSN, err := os.ReadFile(cfg.auditObserverPath)
	if err != nil {
		return fmt.Errorf("read audit observer DSN: %w", err)
	}
	dsn := strings.TrimSpace(string(rawDSN))
	if dsn == "" {
		return fmt.Errorf("audit observer DSN file is empty")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open audit observer configuration")
	}
	defer pool.Close()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire audit observer connection")
	}
	defer connection.Release()
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin read-only audit observer transaction")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	rec.mu.Lock()
	requestIDs := make([]string, 0, len(rec.auditConservation.RequestAcceptedAt))
	for requestID := range rec.auditConservation.RequestAcceptedAt {
		requestIDs = append(requestIDs, requestID)
	}
	rec.mu.Unlock()
	if len(requestIDs) == 0 {
		return fmt.Errorf("mandatory-audit workload accepted no mutations")
	}

	const batchSize = 5000
	for {
		observed := make(map[string]int, len(requestIDs))
		for start := 0; start < len(requestIDs); start += batchSize {
			end := min(start+batchSize, len(requestIDs))
			rows, queryErr := tx.Query(ctx, `
				SELECT correlation_id, count(*)
				FROM audit_outbox
				WHERE correlation_id = ANY($1::text[])
				GROUP BY correlation_id`, requestIDs[start:end])
			if queryErr != nil {
				return fmt.Errorf("query durable audit intents: %w", queryErr)
			}
			for rows.Next() {
				var requestID string
				var count int
				if scanErr := rows.Scan(&requestID, &count); scanErr != nil {
					rows.Close()
					return fmt.Errorf("scan durable audit intent: %w", scanErr)
				}
				observed[requestID] += count
			}
			if rows.Err() != nil {
				err = rows.Err()
				rows.Close()
				return fmt.Errorf("read durable audit intents: %w", err)
			}
			rows.Close()
		}
		unique := 0
		duplicates := false
		for _, requestID := range requestIDs {
			if observed[requestID] > 0 {
				unique++
			}
			if observed[requestID] > 1 {
				duplicates = true
			}
		}
		rec.recordObservedAuditIntents(unique)
		if unique == len(requestIDs) && !duplicates {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("observed %d of %d unique durable audit intents before deadline", unique, len(requestIDs))
		case <-time.After(2 * time.Second):
		}
	}
}

func reconcileMandatoryAudit(ctx context.Context, cfg *config, token string, rec *recorder) error {
	rec.mu.Lock()
	requests := make(map[string]time.Time, len(rec.auditConservation.RequestAcceptedAt))
	var earliest time.Time
	for requestID, acceptedAt := range rec.auditConservation.RequestAcceptedAt {
		requests[requestID] = acceptedAt
		if earliest.IsZero() || acceptedAt.Before(earliest) {
			earliest = acceptedAt
		}
	}
	rec.mu.Unlock()
	if len(requests) == 0 {
		return fmt.Errorf("mandatory-audit workload accepted no mutations")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	for {
		rows, err := fetchAuditRows(ctx, client, cfg.server, token, earliest.Add(-time.Minute), requests)
		if err == nil && rec.finishAuditConservation(rows) {
			return nil
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return err
			}
			return fmt.Errorf("mandatory-audit conservation did not converge before deadline")
		case <-time.After(2 * time.Second):
		}
	}
}

func fetchAuditRows(ctx context.Context, client *http.Client, server, token string, from time.Time, wanted map[string]time.Time) (map[string][]time.Time, error) {
	matched := make(map[string][]time.Time, len(wanted))
	for offset := 0; ; offset += 500 {
		query := url.Values{}
		query.Set("action", "cluster.update")
		query.Set("resource_type", "cluster")
		query.Set("from", from.UTC().Format(time.RFC3339))
		query.Set("page_size", "500")
		query.Set("offset", strconv.Itoa(offset))
		endpoint := strings.TrimRight(server, "/") + "/api/v1/audit/?" + query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var page struct {
			Data []struct {
				CorrelationID string `json:"correlation_id"`
				Action        string `json:"action"`
				ResourceType  string `json:"resource_type"`
				CreatedAt     string `json:"created_at"`
			} `json:"data"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&page)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("audit reconciliation returned HTTP %d", response.StatusCode)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("decode audit reconciliation page: %w", decodeErr)
		}
		for _, row := range page.Data {
			if row.Action != "cluster.update" || row.ResourceType != "cluster" {
				continue
			}
			if _, ok := wanted[row.CorrelationID]; !ok {
				continue
			}
			createdAt, err := time.Parse(time.RFC3339, row.CreatedAt)
			if err != nil {
				return nil, fmt.Errorf("audit row %s has invalid created_at", row.CorrelationID)
			}
			matched[row.CorrelationID] = append(matched[row.CorrelationID], createdAt)
		}
		if len(page.Data) < 500 {
			return matched, nil
		}
	}
}
