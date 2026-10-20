// Package migrations embeds the canonical goose migration set and applies it
// under a PostgreSQL session advisory lock (Technology Selection Guide ch.02).
//
// Migrations are forward-only: every file carries a single `-- +goose Up`
// block, there is no Down section, and the server applies pending migrations
// at boot before it accepts traffic. Two replicas starting at once serialize
// on the advisory lock, so exactly one of them applies each migration.
//
// Naming is goose's sequential form, NNNNN_<slug>.sql, zero-padded so the
// lexical order of the embedded files equals their numeric version order
// (sqlc reads the same directory as its schema source).
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strconv"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed *.sql
var files embed.FS

// AdvisoryLockID is the pg_advisory_lock key held for the whole migration
// run. It is the key the previous standalone migrator used, so an operator
// who still has that tooling around cannot run concurrently with a booting
// server.
const AdvisoryLockID int64 = 781930472366215

// VersionTable is the goose bookkeeping table.
const VersionTable = "public.goose_db_version"

var versionRe = regexp.MustCompile(`^(\d+)_[a-z0-9_]+\.sql$`)

// FS returns the embedded migration set.
func FS() fs.FS { return files }

// Latest returns the highest embedded migration version. It panics on a
// malformed file name because that is a build defect, not a runtime state.
func Latest() int64 {
	versions := Versions()
	if len(versions) == 0 {
		panic("migrations: embedded set is empty")
	}
	return versions[len(versions)-1]
}

// Versions lists every embedded migration version in ascending order.
func Versions() []int64 {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		panic(fmt.Sprintf("migrations: read embedded set: %v", err))
	}
	out := make([]int64, 0, len(entries))
	for _, e := range entries {
		m := versionRe.FindStringSubmatch(e.Name())
		if m == nil {
			panic(fmt.Sprintf("migrations: %q does not follow NNNNN_<slug>.sql", e.Name()))
		}
		v, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			panic(fmt.Sprintf("migrations: %q: %v", e.Name(), err))
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Result summarises one Up run.
type Result struct {
	// Applied is the number of migrations this process applied. Zero means
	// another replica (or an earlier run) had already brought the schema to
	// Latest.
	Applied int
	// Version is the applied schema version after the run.
	Version int64
}

// Up applies every pending migration. It blocks on the advisory lock while
// another session is migrating and returns once the schema is at Latest. The
// lock is released whether the run succeeds or fails; a failed migration is
// rolled back (transactional migrations) and reported as an error so the
// caller exits non-zero instead of serving a half-applied schema.
func Up(ctx context.Context, db *sql.DB, logger *slog.Logger) (Result, error) {
	if logger == nil {
		logger = slog.Default()
	}
	provider, err := newProvider(db, logger)
	if err != nil {
		return Result{}, err
	}
	defer provider.Close()

	results, err := provider.Up(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("migrations: apply: %w", err)
	}
	for _, r := range results {
		logger.Info("database migration applied",
			"event", "database_migration_applied",
			"version", r.Source.Version,
			"file", r.Source.Path,
			"duration_ms", r.Duration.Milliseconds(),
		)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("migrations: read version: %w", err)
	}
	return Result{Applied: len(results), Version: version}, nil
}

// Version reports the applied schema version (0 when the version table does
// not exist yet).
func Version(ctx context.Context, db *sql.DB) (int64, error) {
	provider, err := newProvider(db, slog.Default())
	if err != nil {
		return 0, err
	}
	defer provider.Close()
	v, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("migrations: read version: %w", err)
	}
	return v, nil
}

func newProvider(db *sql.DB, logger *slog.Logger) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker(
		lock.WithLockID(AdvisoryLockID),
		// Poll every 2s for up to 5 minutes: a sibling replica applying a long
		// backfill must not make this replica give up and crash-loop.
		lock.WithLockTimeout(2, 150),
		lock.WithUnlockTimeout(2, 15),
	)
	if err != nil {
		return nil, fmt.Errorf("migrations: session locker: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files,
		goose.WithSessionLocker(locker),
		goose.WithTableName(VersionTable),
		goose.WithSlog(logger),
	)
	if err != nil {
		return nil, fmt.Errorf("migrations: provider: %w", err)
	}
	return provider, nil
}
