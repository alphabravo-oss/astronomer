// Package tasks — gitops:sync periodic worker (migration 060).
//
// Every 60s the scheduler fires gitops:sync. The task pulls every enabled
// gitops_registration_sources row whose sync_mode='interval' AND
// (last_synced_at IS NULL OR last_synced_at + interval < now), clones (or
// fetches) the repo via go-git/v5 into a per-source cache directory
// (/tmp/gitops/<source_id>/), walks path_prefix/**/*.{yaml,yml}, parses
// each ClusterRegistration doc, and runs the gitops.Apply helper to
// reconcile the cluster row + template binding. The missing-set
// (clusters previously known to this source, minus clusters seen this
// tick) is processed under the source's on_delete policy
// (log / tombstone / decommission). After every successful tick the
// reaper sweeps tombstoned rows older than the grace window and
// enqueues cluster:decommission for each.
//
// Hard rules from the spec:
//
//   - go-git/v5 only (no shell-out).
//   - Cache clone idempotent on worker restart.
//   - on_delete='decommission' with an empty path_prefix warns at
//     startup (large blast radius — whole repo is monitored).
//   - The dry-run /preview/ handler reuses the same walker + apply
//     logic via PreviewSource below, so the worker and handler tier
//     share the implementation.
//   - The tombstone reaper enqueues the existing cluster:decommission
//     task — we never reimplement decom logic.

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/gitops"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// GitOpsSyncType is the asynq task type registered on the scheduler.
const GitOpsSyncType = "gitops:sync"

// GitOpsTombstoneGrace is the default grace window between
// tombstone and forced decommission. Exposed as a package variable so
// tests can shrink it.
var GitOpsTombstoneGrace = 24 * time.Hour

// GitOpsCloneRoot is the per-host cache directory under which each
// source's clone lives at GitOpsCloneRoot/<source_id>. Tests override
// this to t.TempDir().
var GitOpsCloneRoot = "/tmp/gitops"

// GitOpsMassDecommissionRatio is the fraction of a source's
// previously-known clusters that, if removed in a single sync under
// on_delete=decommission, trips the mass-decommission guard (H10).
// Package var so tests can drive the boundary.
var GitOpsMassDecommissionRatio = 0.5

// GitOpsMassDecommissionMinMissing is an absolute floor so the ratio can
// never trip on a tiny fleet — a 1- or 2-cluster removal is always
// allowed regardless of fleet size.
var GitOpsMassDecommissionMinMissing = 3

// massDecommissionThreshold returns the missing-count at or above which
// the mass-decommission guard trips for a source with prevCount
// previously-known clusters. It is max(ceil(ratio*prev), floor).
func massDecommissionThreshold(prevCount int) int {
	t := int(math.Ceil(GitOpsMassDecommissionRatio * float64(prevCount)))
	if t < GitOpsMassDecommissionMinMissing {
		t = GitOpsMassDecommissionMinMissing
	}
	return t
}

// Metrics --------------------------------------------------------------

var (
	gitopsSyncsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "gitops_syncs_total",
			Help:      "Total number of gitops:sync ticks per source + outcome.",
		},
		observability.MetricLabels("source", "outcome"),
	)
	gitopsClustersManaged = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "gitops_clusters_managed",
			Help:      "Per-source count of clusters managed via gitops (status='active' + 'tombstoned').",
		},
		observability.MetricLabels("source"),
	)
	gitopsTombstonedClusters = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "gitops_tombstoned_clusters",
			Help:      "Per-source count of clusters currently tombstoned awaiting reap.",
		},
		observability.MetricLabels("source"),
	)
)

func init() {
	prometheus.MustRegister(gitopsSyncsTotal, gitopsClustersManaged, gitopsTombstonedClusters)
}

// GitOpsQuerier is the database surface the sync worker needs. It's a
// subset of *sqlc.Queries (plus gitops.ApplyQuerier surface). The
// concrete *sqlc.Queries value satisfies this interface in production;
// tests pass an in-process fake.
type GitOpsQuerier interface {
	gitops.ApplyQuerier

	ListEnabledGitOpsSources(ctx context.Context) ([]sqlc.GitopsRegistrationSource, error)
	GetGitOpsSource(ctx context.Context, id uuid.UUID) (sqlc.GitopsRegistrationSource, error)
	ListGitOpsRegisteredClustersBySource(ctx context.Context, sourceID uuid.UUID) ([]sqlc.GitopsRegisteredCluster, error)
	TombstoneGitOpsRegisteredCluster(ctx context.Context, arg sqlc.TombstoneGitOpsRegisteredClusterParams) error
	DeleteGitOpsRegisteredCluster(ctx context.Context, clusterID uuid.UUID) error
	StampGitOpsSourceSync(ctx context.Context, arg sqlc.StampGitOpsSourceSyncParams) error
	StampGitOpsSourceError(ctx context.Context, arg sqlc.StampGitOpsSourceErrorParams) error
	ConsumeGitOpsMassDecommissionOverride(ctx context.Context, id uuid.UUID) error
	ListExpiredTombstones(ctx context.Context, cutoff pgtype.Timestamptz) ([]sqlc.GitopsRegisteredCluster, error)
	CountGitOpsRegisteredClustersBySource(ctx context.Context, sourceID uuid.UUID) (int64, error)
	CountGitOpsTombstonedBySource(ctx context.Context, sourceID uuid.UUID) (int64, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	CreateClusterDecommission(ctx context.Context, arg sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error)
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}

// GitOpsDecryptor unwraps the encrypted-at-rest git auth blob
// (auth_encrypted). The production implementation is *auth.Encryptor;
// tests pass a stub that returns the input verbatim. Mirrors
// CloudCredentialDecryptor so the worker never imports the auth package
// directly.
type GitOpsDecryptor interface {
	Decrypt(token string) (string, error)
}

// GitOpsEnqueuer matches the asynq.Client surface used to enqueue
// cluster:decommission. nil-safe: when not wired, the reaper skips the
// enqueue and only writes the audit row + tombstone update.
type GitOpsEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// GitOpsDeps wires the worker. Set once at server startup via
// ConfigureGitOps; tests can supply a fake. Log is optional.
type GitOpsDeps struct {
	Queries    GitOpsQuerier
	Enqueuer   GitOpsEnqueuer
	TaskOutbox TaskOutboxWriter
	// Decryptor unwraps the source's auth_encrypted blob before it's
	// handed to go-git. nil in dev / when no Fernet key is configured, in
	// which case the stored value is used verbatim (plaintext fallback).
	Decryptor GitOpsDecryptor
	Log       *slog.Logger
	CloneRoot string // overrides GitOpsCloneRoot when non-empty
	// Now is the clock function. Defaults to time.Now. Tests override
	// to drive the tombstone-grace boundary without sleeping.
	Now func() time.Time
}

var gitopsDeps GitOpsDeps

// ConfigureGitOps wires the runtime deps. Called once from
// cmd/server / cmd/worker at startup.
func ConfigureGitOps(deps GitOpsDeps) {
	gitopsDeps = deps
	if gitopsDeps.Log == nil {
		gitopsDeps.Log = slog.Default()
	}
	if gitopsDeps.Now == nil {
		gitopsDeps.Now = time.Now
	}
}

// ResetGitOps clears the runtime deps. Tests only.
func ResetGitOps() {
	gitopsDeps = GitOpsDeps{}
}

// NewGitOpsSyncTask returns the periodic-tick task.
func NewGitOpsSyncTask() (*asynq.Task, error) {
	return asynq.NewTask(GitOpsSyncType, nil), nil
}

// HandleGitOpsSync is the asynq handler — runs once per scheduler tick.
// Returns nil even on per-source errors so asynq doesn't blow up the
// whole tick; the per-source error is stamped on the row instead.
func HandleGitOpsSync(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, GitOpsSyncType, func() error {
		if gitopsDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "gitops:sync runtime not configured, skipping")
			return nil
		}
		now := gitopsDeps.Now()

		sources, err := gitopsDeps.Queries.ListEnabledGitOpsSources(ctx)
		if err != nil {
			return fmt.Errorf("list enabled gitops sources: %w", err)
		}
		for _, src := range sources {
			if src.SyncMode != "interval" {
				continue
			}
			if src.LastSyncedAt.Valid {
				next := src.LastSyncedAt.Time.Add(time.Duration(src.SyncIntervalSeconds) * time.Second)
				if now.Before(next) {
					continue
				}
			}
			if err := SyncSource(ctx, src.ID); err != nil {
				gitopsDeps.Log.WarnContext(ctx, "gitops source sync failed",
					"source", src.Name, "source_id", src.ID.String(), "error", err)
			}
		}

		// Tombstone reaper. Pull every row that's been tombstoned
		// longer than the grace window and enqueue cluster:decommission.
		cutoff := pgtype.Timestamptz{Time: now.Add(-GitOpsTombstoneGrace), Valid: true}
		expired, err := gitopsDeps.Queries.ListExpiredTombstones(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("list expired tombstones: %w", err)
		}
		for _, row := range expired {
			if err := reapTombstone(ctx, row); err != nil {
				gitopsDeps.Log.WarnContext(ctx, "gitops tombstone reap failed",
					"cluster_id", row.ClusterID.String(), "error", err)
			}
		}

		return nil
	})
}

// SyncSource processes a single source through one tick. Exported so the
// handler's manual /sync/ endpoint can reuse the same code path.
//
// On any hard error the function stamps last_error on the source row and
// returns the error to the caller. Skippable per-file errors (non-
// ClusterRegistration YAML) are logged and continue.
func SyncSource(ctx context.Context, sourceID uuid.UUID) error {
	if gitopsDeps.Queries == nil {
		return fmt.Errorf("gitops runtime not configured")
	}
	src, err := gitopsDeps.Queries.GetGitOpsSource(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("load source: %w", err)
	}
	headSHA, parsedDocs, walkErr := walkSource(ctx, src)
	if walkErr != nil {
		_ = gitopsDeps.Queries.StampGitOpsSourceError(ctx, sqlc.StampGitOpsSourceErrorParams{
			ID:        src.ID,
			LastError: walkErr.Error(),
		})
		gitopsSyncsTotal.WithLabelValues(observability.MetricValues(src.Name, "failed")...).Inc()
		emitAudit(ctx, "gitops.sync.failed", src.ID.String(), src.Name, map[string]any{"error": walkErr.Error()})
		return walkErr
	}

	previousLinks, err := gitopsDeps.Queries.ListGitOpsRegisteredClustersBySource(ctx, src.ID)
	if err != nil {
		return fmt.Errorf("list previously-registered clusters: %w", err)
	}
	previousByPath := map[string]sqlc.GitopsRegisteredCluster{}
	for _, link := range previousLinks {
		previousByPath[link.RepoPath] = link
	}

	seenPaths := map[string]struct{}{}

	for _, parsed := range parsedDocs {
		seenPaths[parsed.Doc.RepoPath] = struct{}{}

		// Check the converged-sha short-circuit before any DB writes.
		if prev, ok := previousByPath[parsed.Doc.RepoPath]; ok {
			if prev.LastYamlSha == parsed.SHA && prev.Status == "active" {
				continue
			}
		}

		applied, err := gitops.Apply(ctx, gitopsDeps.Queries, gitops.ApplyInput{
			Doc:        parsed.Doc,
			SourceID:   src.ID,
			ContentSHA: parsed.SHA,
		})
		if err != nil {
			gitopsDeps.Log.WarnContext(ctx, "gitops apply failed",
				"source", src.Name, "path", parsed.Doc.RepoPath, "error", err)
			continue
		}

		// Audit + metric on the action that actually occurred.
		switch {
		case applied.Created:
			emitAudit(ctx, "gitops.cluster.registered", applied.ClusterID, applied.ClusterName, map[string]any{
				"source": src.Name,
				"path":   parsed.Doc.RepoPath,
			})
		case applied.Updated:
			emitAudit(ctx, "gitops.cluster.updated", applied.ClusterID, applied.ClusterName, map[string]any{
				"source": src.Name,
				"path":   parsed.Doc.RepoPath,
			})
		}

		// If the row was previously tombstoned, this is a "YAML
		// reappeared" restore. Apply already set status='active' via
		// UpsertGitOpsRegisteredCluster.
		if prev, ok := previousByPath[parsed.Doc.RepoPath]; ok && prev.Status == "tombstoned" {
			emitAudit(ctx, "gitops.cluster.restored", applied.ClusterID, applied.ClusterName, map[string]any{
				"source": src.Name,
				"path":   parsed.Doc.RepoPath,
			})
		}
	}

	// Mass-decommission guard (H10). Only the destructive on_delete
	// policy is gated; log/tombstone skip this block byte-for-byte. A
	// single bad-but-clean walk (empty parsedDocs) or a sync that wipes
	// out half-or-more of a source's known fleet is treated as a footgun
	// and REFUSED before any decommission is enqueued, unless the
	// operator has explicitly armed allow_mass_decommission.
	if src.OnDelete == "decommission" {
		prevCount := len(previousByPath)
		missingCount := 0
		for path := range previousByPath {
			if _, ok := seenPaths[path]; !ok {
				missingCount++
			}
		}
		threshold := massDecommissionThreshold(prevCount)
		guardWouldTrip := prevCount > 0 && (len(parsedDocs) == 0 || missingCount >= threshold)
		if guardWouldTrip && !src.AllowMassDecommission {
			msg := fmt.Sprintf("mass-decommission blocked: %d of %d clusters missing (parsed_docs=%d, threshold=%d); set allow_mass_decommission to override",
				missingCount, prevCount, len(parsedDocs), threshold)
			_ = gitopsDeps.Queries.StampGitOpsSourceError(ctx, sqlc.StampGitOpsSourceErrorParams{ID: src.ID, LastError: msg})
			gitopsSyncsTotal.WithLabelValues(observability.MetricValues(src.Name, "blocked")...).Inc()
			gitopsDeps.Log.ErrorContext(ctx, "gitops mass-decommission blocked",
				"source", src.Name, "source_id", src.ID.String(),
				"prev_count", prevCount, "missing_count", missingCount,
				"parsed_docs", len(parsedDocs), "threshold", threshold)
			emitAudit(ctx, "gitops.mass_decommission_blocked", src.ID.String(), src.Name, map[string]any{
				"source":        src.Name,
				"prev_count":    prevCount,
				"missing_count": missingCount,
				"parsed_docs":   len(parsedDocs),
				"threshold":     threshold,
				"ratio":         GitOpsMassDecommissionRatio,
				"on_delete":     "decommission",
				"head_sha":      headSHA,
			})
			return fmt.Errorf("%s", msg)
		}
		// Disarm an armed override on EVERY decommission-policy sync — whether it
		// was needed (guard tripped, mass removal honored) or not (a benign sync
		// happened to land first). The override is one-shot and covers exactly the
		// NEXT sync; consuming it unconditionally here means a stale arm can never
		// silently bypass a FUTURE bad sync. Only the honored case is audited.
		if src.AllowMassDecommission {
			if guardWouldTrip {
				emitAudit(ctx, "gitops.mass_decommission_override", src.ID.String(), src.Name, map[string]any{
					"source":        src.Name,
					"prev_count":    prevCount,
					"missing_count": missingCount,
					"parsed_docs":   len(parsedDocs),
					"threshold":     threshold,
					"head_sha":      headSHA,
				})
			}
			if err := gitopsDeps.Queries.ConsumeGitOpsMassDecommissionOverride(ctx, src.ID); err != nil {
				return fmt.Errorf("consume mass-decommission override: %w", err)
			}
		}
	}

	// Missing-set processing.
	for path, prev := range previousByPath {
		if _, ok := seenPaths[path]; ok {
			continue
		}
		if err := applyOnDelete(ctx, src, prev); err != nil {
			gitopsDeps.Log.WarnContext(ctx, "gitops on_delete failed",
				"source", src.Name, "path", path, "policy", src.OnDelete, "error", err)
		}
	}

	if err := gitopsDeps.Queries.StampGitOpsSourceSync(ctx, sqlc.StampGitOpsSourceSyncParams{
		ID:            src.ID,
		LastSyncedAt:  pgtype.Timestamptz{Time: gitopsDeps.Now(), Valid: true},
		LastSyncedSha: headSHA,
	}); err != nil {
		return fmt.Errorf("stamp last_synced: %w", err)
	}

	gitopsSyncsTotal.WithLabelValues(observability.MetricValues(src.Name, "succeeded")...).Inc()
	emitAudit(ctx, "gitops.sync.succeeded", src.ID.String(), src.Name, map[string]any{
		"head_sha":     headSHA,
		"docs_applied": len(parsedDocs),
	})
	updateSourceGauges(ctx, src)
	return nil
}

func applyOnDelete(ctx context.Context, src sqlc.GitopsRegistrationSource, prev sqlc.GitopsRegisteredCluster) error {
	cluster, err := gitopsDeps.Queries.GetClusterByID(ctx, prev.ClusterID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load cluster %s: %w", prev.ClusterID, err)
	}
	clusterName := cluster.Name
	if clusterName == "" {
		clusterName = prev.RepoPath
	}
	switch src.OnDelete {
	case "log":
		emitAudit(ctx, "gitops.cluster.missing", prev.ClusterID.String(), clusterName, map[string]any{
			"source": src.Name,
			"path":   prev.RepoPath,
		})
	case "tombstone":
		if prev.Status == "tombstoned" {
			return nil
		}
		if err := gitopsDeps.Queries.TombstoneGitOpsRegisteredCluster(ctx, sqlc.TombstoneGitOpsRegisteredClusterParams{
			ClusterID:    prev.ClusterID,
			TombstonedAt: pgtype.Timestamptz{Time: gitopsDeps.Now(), Valid: true},
		}); err != nil {
			return fmt.Errorf("tombstone: %w", err)
		}
		emitAudit(ctx, "gitops.cluster.tombstoned", prev.ClusterID.String(), clusterName, map[string]any{
			"source": src.Name,
			"path":   prev.RepoPath,
			"grace":  GitOpsTombstoneGrace.String(),
		})
	case "decommission":
		if err := enqueueDecommission(ctx, prev.ClusterID, clusterName); err != nil {
			return err
		}
		if err := gitopsDeps.Queries.DeleteGitOpsRegisteredCluster(ctx, prev.ClusterID); err != nil {
			return fmt.Errorf("clear link: %w", err)
		}
		emitAudit(ctx, "gitops.cluster.missing", prev.ClusterID.String(), clusterName, map[string]any{
			"source": src.Name,
			"path":   prev.RepoPath,
			"action": "decommission",
		})
	}
	return nil
}

func reapTombstone(ctx context.Context, row sqlc.GitopsRegisteredCluster) error {
	cluster, err := gitopsDeps.Queries.GetClusterByID(ctx, row.ClusterID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load cluster: %w", err)
	}
	clusterName := cluster.Name
	if clusterName == "" {
		clusterName = row.RepoPath
	}
	if err := enqueueDecommission(ctx, row.ClusterID, clusterName); err != nil {
		return err
	}
	if err := gitopsDeps.Queries.DeleteGitOpsRegisteredCluster(ctx, row.ClusterID); err != nil {
		return fmt.Errorf("clear tombstoned link: %w", err)
	}
	emitAudit(ctx, "gitops.cluster.reaped", row.ClusterID.String(), clusterName, map[string]any{
		"source_id": row.SourceID.String(),
		"path":      row.RepoPath,
	})
	return nil
}

func enqueueDecommission(ctx context.Context, clusterID uuid.UUID, clusterName string) error {
	if clusterID == uuid.Nil {
		return nil
	}
	decom, err := gitopsDeps.Queries.CreateClusterDecommission(ctx, sqlc.CreateClusterDecommissionParams{
		ClusterID:     clusterID,
		RequestedByID: pgtype.UUID{},
		ClusterName:   clusterName,
	})
	if err != nil {
		return fmt.Errorf("create cluster_decommission: %w", err)
	}
	task, err := NewClusterDecommissionTask(decom.ID)
	if err != nil {
		return fmt.Errorf("build task: %w", err)
	}
	if gitopsDeps.TaskOutbox != nil {
		if _, err := EnqueueTaskOutbox(ctx, gitopsDeps.TaskOutbox, task, TaskOutboxOptions{
			DedupeKey: fmt.Sprintf("cluster_decommission:%s", decom.ID.String()),
			// "tunnel" queue: managed-side cleanup needs the server-pod hub.
			QueueName:           ClusterTemplateApplyQueueName,
			MaxRetry:            3,
			MaxDeliveryAttempts: 20,
		}); err == nil {
			return nil
		} else {
			// Don't silently fall through: the outbox error was being swallowed
			// and the fallback enqueued onto the DEFAULT queue, which has no
			// cluster-decommission handler — so the task dead-lettered while the
			// tracking row already existed, orphaning managed-side resources.
			runtimeLogger().WarnContext(ctx, "gitops decommission outbox enqueue failed; falling back to direct enqueue on the tunnel queue",
				"cluster_id", clusterID.String(), "decommission_id", decom.ID.String(), "error", err)
		}
	}
	if gitopsDeps.Enqueuer == nil {
		return nil
	}
	// Stamp the SAME (tunnel) queue the outbox would have used — the default
	// queue has no TypeClusterDecommission handler.
	if _, err := gitopsDeps.Enqueuer.Enqueue(task, asynq.Queue(ClusterTemplateApplyQueueName)); err != nil {
		return fmt.Errorf("enqueue decommission: %w", err)
	}
	return nil
}

// ParsedDoc is the in-memory bundle the walker hands to the apply phase.
type ParsedDoc struct {
	Doc gitops.ClusterRegistration
	SHA string
}

// walkSource clones (or fetches) the repo, walks YAML, and parses each
// file. Returns the HEAD sha + parsed docs.
func walkSource(ctx context.Context, src sqlc.GitopsRegistrationSource) (string, []ParsedDoc, error) {
	dir := cloneDir(src.ID)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", nil, fmt.Errorf("mkdir clone parent: %w", err)
	}
	repo, err := openOrClone(ctx, src, dir)
	if err != nil {
		return "", nil, err
	}
	if err := fetchAndCheckout(ctx, src, repo); err != nil {
		return "", nil, err
	}
	head, err := repo.Head()
	if err != nil {
		return "", nil, fmt.Errorf("resolve HEAD: %w", err)
	}
	headSHA := head.Hash().String()

	prefix := strings.TrimPrefix(src.PathPrefix, "/")
	walkRoot := dir
	if prefix != "" {
		walkRoot = filepath.Join(dir, prefix)
	}
	// A non-existent / unreadable / non-directory walkRoot must be a hard
	// sync error — NOT silently interpreted as "all docs deleted" (H10).
	// A typo'd path_prefix or renamed dir would otherwise yield zero docs
	// and mass-decommission every cluster owned by this source.
	if fi, statErr := os.Stat(walkRoot); statErr != nil {
		return "", nil, fmt.Errorf("walk root %q unreadable (check path_prefix %q): %w", walkRoot, src.PathPrefix, statErr)
	} else if !fi.IsDir() {
		return "", nil, fmt.Errorf("walk root %q is not a directory (check path_prefix %q)", walkRoot, src.PathPrefix)
	}
	var docs []ParsedDoc
	walkErr := filepath.WalkDir(walkRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", p, err)
		}
		if d.IsDir() {
			// Skip .git always
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !isYAMLFile(p) {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			rel = p
		}
		content, readErr := os.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", rel, readErr)
		}
		doc, parseErr := gitops.Parse(content, rel)
		if parseErr != nil {
			if gitops.IsSkippable(parseErr) {
				gitopsDeps.Log.DebugContext(ctx, "gitops skipping non-registration file",
					"source", src.Name, "path", rel, "reason", parseErr.Error())
				return nil
			}
			return fmt.Errorf("parse %s: %w", rel, parseErr)
		}
		docs = append(docs, ParsedDoc{Doc: doc, SHA: gitops.SHA(content)})
		return nil
	})
	if walkErr != nil {
		return "", nil, walkErr
	}
	return headSHA, docs, nil
}

func isYAMLFile(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".yaml" || ext == ".yml"
}

func cloneDir(sourceID uuid.UUID) string {
	root := gitopsDeps.CloneRoot
	if root == "" {
		root = GitOpsCloneRoot
	}
	return filepath.Join(root, sourceID.String())
}

func openOrClone(ctx context.Context, src sqlc.GitopsRegistrationSource, dir string) (*git.Repository, error) {
	if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && info.IsDir() {
		repo, err := git.PlainOpen(dir)
		if err != nil {
			return nil, fmt.Errorf("open cached clone %s: %w", dir, err)
		}
		// Re-point the origin URL in case it was edited on the source row.
		if err := setRemoteURL(repo, src.RepoUrl); err != nil {
			return nil, err
		}
		return repo, nil
	}
	auth, err := buildGitAuth(src)
	if err != nil {
		return nil, err
	}
	repo, err := git.PlainCloneContext(ctx, dir, false, &git.CloneOptions{
		URL:           src.RepoUrl,
		Auth:          auth,
		ReferenceName: plumbing.NewBranchReferenceName(branchOrDefault(src.Branch)),
		SingleBranch:  true,
		Depth:         1,
	})
	if err != nil {
		// Wipe partial state so the next tick retries cleanly.
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("clone %s: %w", src.RepoUrl, err)
	}
	return repo, nil
}

func setRemoteURL(repo *git.Repository, url string) error {
	cfg, err := repo.Config()
	if err != nil {
		return fmt.Errorf("repo config: %w", err)
	}
	remote, ok := cfg.Remotes["origin"]
	if !ok {
		// Create origin so the next fetch knows where to go.
		cfg.Remotes["origin"] = &gitconfig.RemoteConfig{Name: "origin", URLs: []string{url}}
		return repo.SetConfig(cfg)
	}
	if len(remote.URLs) > 0 && remote.URLs[0] == url {
		return nil
	}
	remote.URLs = []string{url}
	return repo.SetConfig(cfg)
}

func fetchAndCheckout(ctx context.Context, src sqlc.GitopsRegistrationSource, repo *git.Repository) error {
	auth, err := buildGitAuth(src)
	if err != nil {
		return err
	}
	branch := branchOrDefault(src.Branch)
	fetchErr := repo.FetchContext(ctx, &git.FetchOptions{
		Auth:     auth,
		RefSpecs: []gitconfig.RefSpec{gitconfig.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch))},
		Depth:    1,
		Force:    true,
	})
	if fetchErr != nil && !errors.Is(fetchErr, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("fetch: %w", fetchErr)
	}
	// Resolve the remote ref AFTER fetch so we always check out the
	// latest snapshot. Re-pointing the local branch ref to the remote
	// hash before checkout is how we converge a cached clone — if we
	// merely "git checkout main", the local main may still point at the
	// pre-fetch commit.
	remoteRef, refErr := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if refErr != nil {
		// First-time clone path: PlainCloneContext already set HEAD.
		wt, err := repo.Worktree()
		if err != nil {
			return fmt.Errorf("worktree: %w", err)
		}
		return wt.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewBranchReferenceName(branch),
			Force:  true,
		})
	}
	// Force the local branch ref to the freshly-fetched remote hash so a
	// subsequent file deletion in the upstream tree converges.
	localRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName(branch), remoteRef.Hash())
	if err := repo.Storer.SetReference(localRef); err != nil {
		return fmt.Errorf("set local branch ref: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	return wt.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branch),
		Force:  true,
	})
}

func branchOrDefault(b string) string {
	if b == "" {
		return "main"
	}
	return b
}

// buildGitAuth resolves the auth_encrypted blob into a go-git auth method.
// The blob is a Fernet ciphertext in production (written by the handler's
// SetEncryptor path); it is decrypted here before being handed to go-git
// as the https_token password or the ssh_key PEM. When no decryptor is
// wired — or the value is already plaintext (dev / pre-encryption rows) —
// the stored value is used verbatim.
func buildGitAuth(src sqlc.GitopsRegistrationSource) (transport.AuthMethod, error) {
	switch src.AuthMode {
	case "", "none":
		return nil, nil
	case "https_token":
		if src.AuthEncrypted == "" {
			return nil, fmt.Errorf("source %q is https_token but auth_encrypted is empty", src.Name)
		}
		return &githttp.BasicAuth{Username: "astronomer-gitops", Password: decryptGitAuth(src.AuthEncrypted)}, nil
	case "ssh_key":
		if src.AuthEncrypted == "" {
			return nil, fmt.Errorf("source %q is ssh_key but auth_encrypted is empty", src.Name)
		}
		signer, err := gitssh.NewPublicKeys("git", []byte(decryptGitAuth(src.AuthEncrypted)), "")
		if err != nil {
			return nil, fmt.Errorf("parse ssh key: %w", err)
		}
		return signer, nil
	default:
		return nil, fmt.Errorf("unknown auth_mode %q", src.AuthMode)
	}
}

// decryptGitAuth returns the plaintext git credential for a source. When a
// Fernet decryptor is wired the stored auth_encrypted blob is unwrapped;
// when none is configured or the blob is already plaintext (dev,
// pre-encryption rows) the raw value is returned unchanged. Mirrors
// handler/argocd.go decryptInstanceToken.
func decryptGitAuth(blob string) string {
	if gitopsDeps.Decryptor == nil || blob == "" {
		return blob
	}
	if plaintext, err := gitopsDeps.Decryptor.Decrypt(blob); err == nil {
		return plaintext
	}
	return blob
}

func updateSourceGauges(ctx context.Context, src sqlc.GitopsRegistrationSource) {
	total, err := gitopsDeps.Queries.CountGitOpsRegisteredClustersBySource(ctx, src.ID)
	if err == nil {
		gitopsClustersManaged.WithLabelValues(observability.MetricValues(src.Name)...).Set(float64(total))
	}
	tombstoned, err := gitopsDeps.Queries.CountGitOpsTombstonedBySource(ctx, src.ID)
	if err == nil {
		gitopsTombstonedClusters.WithLabelValues(observability.MetricValues(src.Name)...).Set(float64(tombstoned))
	}
}

// emitAudit writes a row to the audit log. Nil-safe on Queries.
func emitAudit(ctx context.Context, action, target, name string, payload map[string]any) {
	if gitopsDeps.Queries == nil {
		return
	}
	body, _ := json.Marshal(payload)
	if body == nil {
		body = []byte("{}")
	}
	err := gitopsDeps.Queries.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
		Source:       "gitops-worker",
		UserID:       pgtype.UUID{},
		Action:       action,
		ResourceType: "gitops",
		ResourceID:   target,
		ResourceName: name,
		Detail:       body,
	})
	if err != nil {
		gitopsDeps.Log.DebugContext(ctx, "audit insert failed (non-fatal)", "action", action, "error", err)
	}
}

// PreviewResult is the dry-run output the handler /preview/ endpoint
// returns. The same shape is computable from a hermetic in-memory repo
// for testing (apply.Result + tombstone/missing/restore deltas).
type PreviewResult struct {
	HeadSHA        string          `json:"head_sha"`
	SourceID       string          `json:"source_id"`
	SourceName     string          `json:"source_name"`
	Applies        []gitops.Result `json:"applies"`
	WouldMiss      []string        `json:"would_miss"`
	WouldRestore   []string        `json:"would_restore"`
	OnDeletePolicy string          `json:"on_delete_policy"`
}

// PreviewSource runs SyncSource in dry-run mode. NO DB writes, NO
// enqueues. Returns the structured diff.
func PreviewSource(ctx context.Context, sourceID uuid.UUID) (PreviewResult, error) {
	if gitopsDeps.Queries == nil {
		return PreviewResult{}, fmt.Errorf("gitops runtime not configured")
	}
	src, err := gitopsDeps.Queries.GetGitOpsSource(ctx, sourceID)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("load source: %w", err)
	}
	headSHA, parsedDocs, walkErr := walkSource(ctx, src)
	if walkErr != nil {
		return PreviewResult{}, walkErr
	}

	previousLinks, err := gitopsDeps.Queries.ListGitOpsRegisteredClustersBySource(ctx, src.ID)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("list previously-registered: %w", err)
	}
	previousByPath := map[string]sqlc.GitopsRegisteredCluster{}
	for _, link := range previousLinks {
		previousByPath[link.RepoPath] = link
	}

	res := PreviewResult{
		HeadSHA:        headSHA,
		SourceID:       src.ID.String(),
		SourceName:     src.Name,
		OnDeletePolicy: src.OnDelete,
	}
	seen := map[string]struct{}{}
	for _, parsed := range parsedDocs {
		seen[parsed.Doc.RepoPath] = struct{}{}
		applied, err := gitops.Apply(ctx, gitopsDeps.Queries, gitops.ApplyInput{
			Doc:        parsed.Doc,
			SourceID:   src.ID,
			ContentSHA: parsed.SHA,
			Dry:        true,
		})
		if err != nil {
			return PreviewResult{}, fmt.Errorf("dry-run apply %s: %w", parsed.Doc.RepoPath, err)
		}
		if prev, ok := previousByPath[parsed.Doc.RepoPath]; ok && prev.Status == "tombstoned" {
			applied.RestoredActive = true
			res.WouldRestore = append(res.WouldRestore, parsed.Doc.RepoPath)
		}
		res.Applies = append(res.Applies, applied)
	}
	for path := range previousByPath {
		if _, ok := seen[path]; !ok {
			res.WouldMiss = append(res.WouldMiss, path)
		}
	}
	return res, nil
}
