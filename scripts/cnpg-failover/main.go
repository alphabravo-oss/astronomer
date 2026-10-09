// cnpg-failover exercises the production pool through a stable CNPG service.
// The operator supplies the separately reviewed fault; this program cannot
// mutate Kubernetes and will only write its own schema in a dedicated DB.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db"
)

type receipt struct {
	SchemaVersion int             `json:"schemaVersion"`
	Status        string          `json:"status"`
	SourceCommit  string          `json:"sourceCommit"`
	ClusterUID    string          `json:"clusterUID"`
	Database      string          `json:"database"`
	Schema        string          `json:"schema"`
	StartedAt     time.Time       `json:"startedAt"`
	CompletedAt   time.Time       `json:"completedAt"`
	Fault         *faultRecord    `json:"fault"`
	BeforePrimary string          `json:"beforePrimary"`
	AfterPrimary  string          `json:"afterPrimary"`
	Acknowledged  int             `json:"acknowledgedWrites"`
	Failures      int             `json:"transientWriteFailures"`
	MaxGap        time.Duration   `json:"maximumSuccessfulWriteGapNanoseconds"`
	Recovery      time.Duration   `json:"faultToNewPrimaryCommitNanoseconds"`
	Lost          int             `json:"acknowledgedRowsLost"`
	Checks        map[string]bool `json:"checks"`
	Limits        []string        `json:"limits"`
	Failure       string          `json:"failureCategory,omitempty"`
}

func main() {
	if err := run(); err != nil {
		// Database error strings can contain credentials or query parameters.
		fmt.Fprintln(os.Stderr, "cnpg-failover: qualification failed; see content-free receipt")
		os.Exit(1)
	}
}

func run() (returned error) {
	refPath := flag.String("database-ref", "", "0600 JSON reference to a dedicated database_url")
	clusterUID := flag.String("cluster-uid", "", "expected owned CloudNativePG cluster UID")
	artifact := flag.String("artifact-dir", "", "new private artifact directory")
	commit := flag.String("source-commit", "", "immutable source commit being exercised")
	flag.Parse()
	if !validUID(*clusterUID) || !validCommit(*commit) || *artifact == "" {
		return errors.New("invalid qualification identity")
	}
	url, database, err := readDatabaseRef(*refPath)
	if err != nil {
		return err
	}
	if err := os.Mkdir(*artifact, 0o700); err != nil {
		return errors.New("artifact directory must be new")
	}
	r := receipt{SchemaVersion: 1, Status: "fail", SourceCommit: *commit, ClusterUID: *clusterUID,
		Database: database, Schema: "qualification_w12_" + uuid.New().String()[:8], StartedAt: time.Now().UTC(),
		Checks: map[string]bool{}, Limits: []string{
			"One synthetic workload and one separately recorded operator fault; not a whole-application RTO or SLA certification.",
			"Production internal/db pool is kept open throughout. Synthetic ledger/audit tables do not prove every production business handler.",
			"No Kubernetes mutation, cleanup, backup, PITR or encryption-key restoration is performed by this driver.",
			"Committed fixture schema is retained for a separately owned physical restore comparison.",
		}}
	defer func() {
		r.CompletedAt = time.Now().UTC()
		if returned != nil && r.Failure == "" {
			r.Failure = "qualification-incomplete"
		}
		data, _ := json.MarshalIndent(r, "", "  ")
		if err := os.WriteFile(filepath.Join(*artifact, "evidence.json"), append(data, '\n'), 0o600); err != nil {
			returned = errors.New("receipt write failed")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databasePool, err := db.ConnectWithConfig(ctx, url, db.PoolConfig{MaxConns: 4, MinConns: 1, HealthCheckPeriod: time.Second})
	if err != nil {
		r.Failure = "pool-connect"
		return errors.New("pool connect failed")
	}
	defer databasePool.Close()
	var actualDatabase string
	if err := databasePool.Pool().QueryRow(ctx, "SELECT current_database()").Scan(&actualDatabase); err != nil || actualDatabase != database {
		r.Failure = "dedicated-database-mismatch"
		return errors.New("connected database identity mismatch")
	}
	work := ledger{pool: databasePool.Pool(), schema: r.Schema}
	if err := work.create(ctx); err != nil {
		r.Failure = "owned-schema-create"
		return errors.New("owned schema initialization failed")
	}
	lastSuccess := time.Now()
	postFault := 0
	for sequence := 1; ; {
		if err := ctx.Err(); err != nil {
			r.Failure = "bounded-deadline"
			return errors.New("qualification deadline")
		}
		if r.Fault == nil {
			fault, err := readFault(filepath.Join(*artifact, "fault.json"), *clusterUID, r.StartedAt)
			if err != nil {
				r.Failure = "invalid-fault-marker"
				return err
			}
			r.Fault = fault
			if fault != nil && r.Acknowledged < 20 {
				r.Failure = "fault-before-armed"
				return errors.New("fault marker arrived before readiness")
			}
		}
		attempt, stop := context.WithTimeout(ctx, 2*time.Second)
		primary, err := work.write(attempt, sequence)
		stop()
		if err != nil {
			r.Failures++
		} else {
			now := time.Now()
			if gap := now.Sub(lastSuccess); gap > r.MaxGap {
				r.MaxGap = gap
			}
			lastSuccess = now
			if r.BeforePrimary == "" {
				r.BeforePrimary = primary
			}
			if r.Fault != nil && primary != r.BeforePrimary {
				if r.AfterPrimary == "" {
					r.AfterPrimary = primary
					r.Recovery = now.Sub(r.Fault.At)
				}
				postFault++
			}
			r.Acknowledged++
			sequence++
			if r.Acknowledged == 20 {
				ready, _ := json.Marshal(map[string]any{"clusterUID": *clusterUID, "schema": r.Schema, "acknowledgedWrites": r.Acknowledged, "primary": r.BeforePrimary})
				if err := os.WriteFile(filepath.Join(*artifact, "ready.json"), ready, 0o600); err != nil {
					return errors.New("ready marker failed")
				}
			}
			if postFault >= 20 {
				break
			}
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	r.Checks, r.Lost, err = work.verify(ctx, r.Acknowledged)
	if err != nil {
		r.Failure = "invariant-query"
		return errors.New("invariant verification failed")
	}
	r.Checks["primary-changed"] = r.AfterPrimary != "" && r.AfterPrimary != r.BeforePrimary
	r.Checks["fault-observed"] = r.Fault != nil
	r.Checks["post-fault-commits"] = postFault >= 20
	for _, passed := range r.Checks {
		if !passed {
			r.Failure = "invariant-failed"
			return errors.New("invariant failed")
		}
	}
	r.Status = "pass"
	return nil
}
