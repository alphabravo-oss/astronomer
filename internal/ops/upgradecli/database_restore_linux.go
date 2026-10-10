//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

const archiveListScript = `pg_restore --list`
const temporaryIdentityScript = `psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -v restore="$1" -v marker="$2" -AtF "|" <<'SQL'
SELECT oid, datdba = (SELECT oid FROM pg_roles WHERE rolname = current_user), COALESCE(shobj_description(oid,'pg_database'),'') = :'marker' FROM pg_database WHERE datname = :'restore';
SQL`
const temporaryCreateScript = `psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -v restore="$1" -v marker="$2" <<'SQL'
CREATE DATABASE :"restore";
COMMENT ON DATABASE :"restore" IS :'marker';
SQL`
const temporaryRestoreScript = `pg_restore --exit-on-error --no-owner --no-privileges --username="$POSTGRES_USER" --dbname="$1"`
const temporarySchemaScript = `psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$1" -AtF "|" -c "SELECT current_database(), current_setting('server_version_num')::bigint / 10000, count(*), COALESCE(max(version),0), COALESCE(bool_or(dirty),false) FROM schema_migrations"`
const temporaryDropScript = `psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -v restore="$1" -v marker="$2" -v expected_oid="$3" <<'SQL'
SELECT datname AS bound_name FROM pg_database WHERE datname = :'restore' AND oid::text = :'expected_oid' AND datdba = (SELECT oid FROM pg_roles WHERE rolname = current_user) AND COALESCE(shobj_description(oid,'pg_database'),'') = :'marker' \gset
DROP DATABASE :"bound_name";
SQL`

// TemporaryDatabase verifies one exact retained backup in a separate database.
// It never replaces the source database. CREATE+COMMENT are not atomic: a child
// failure may leave an unmarked database that must be assessed, never dropped
// merely because its name resembles a temporary name. Failed restore material
// and the original private backup remain retained until an explicit cleanup.
// This handle belongs to one joined Driver invocation; callers must serialize
// its methods. Reconcile does not infer rollback from cancellation.
type TemporaryDatabase struct {
	backup            *DatabaseBackup
	marker            string
	oid               uint32
	creationAttempted bool
	restored          bool
	archiveList       []byte
	input             func(context.Context, Tool, []string, *process.ArtifactCapture, int64) ([]byte, process.Result, error)
}

func NewTemporaryDatabase(backup *DatabaseBackup) (*TemporaryDatabase, error) {
	if backup == nil || backup.source == nil || backup.source.fence == nil || backup.source.fence.commands == nil || !backup.artifact.Acknowledged || backup.artifact.SizeBytes <= 0 {
		return nil, errors.New("acknowledged bound backup required for temporary restore")
	}
	owner := backup.source.options.Owner
	if owner == "" || len(owner) > 256 || strings.ContainsAny(owner, "\x00\t\r\n") {
		return nil, errors.New("bounded explicit operation owner required for temporary restore")
	}
	copy := *backup
	return &TemporaryDatabase{backup: &copy, marker: "astronomer-upgrade:" + owner, input: backup.source.fence.commands.ExecuteArtifactInput}, nil
}

func (v *TemporaryDatabase) sourceState(ctx context.Context) error {
	if v == nil || v.backup == nil || ctx == nil {
		return errors.New("explicit temporary restore binding and context required")
	}
	state, err := v.backup.source.observe(ctx)
	if err != nil {
		return err
	}
	if state != v.backup.schema {
		return errors.New("source schema differs from retained backup")
	}
	return ctx.Err()
}

func (v *TemporaryDatabase) arguments(stdin bool, script string, positionals ...string) []string {
	return v.backup.source.arguments(stdin, script, positionals...)
}

func (v *TemporaryDatabase) identity(ctx context.Context) (uint32, bool, error) {
	b := v.backup.source
	raw, err := b.fence.execute(ctx, b.fence.kubectl, v.arguments(false, temporaryIdentityScript, b.options.RestoreDatabase, v.marker))
	if err != nil {
		return 0, false, err
	}
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	return parseTemporaryIdentity(raw)
}

// Verify requires absence before any CREATE, validates the actual archive list,
// observes the acknowledged operation marker/OID, performs the real restore and
// checks the same clean schema row/major. The child stream integrity check alone
// is insufficient: each pg_restore operation must exit successfully. An error
// retains this handle for a later fresh owned reconciliation and backup recovery.
func (v *TemporaryDatabase) Verify(ctx context.Context) error {
	if err := v.sourceState(ctx); err != nil {
		return err
	}
	if v.creationAttempted {
		return errors.New("temporary restore already attempted; reconcile rather than replay")
	}
	b := v.backup.source
	listing, _, err := v.input(ctx, b.fence.kubectl, v.arguments(true, archiveListScript), &v.backup.artifact, b.options.ArtifactPolicy.MaximumBytes)
	if err != nil {
		return err
	}
	if len(listing) == 0 || int64(len(listing)) > b.fence.commands.options.MaximumCaptureBytes {
		return errors.New("readable archive listing missing or exceeds retained diagnostic budget")
	}
	v.archiveList = append([]byte(nil), listing...)
	if err := v.sourceState(ctx); err != nil {
		return err
	}
	if _, exists, err := v.identity(ctx); err != nil {
		return err
	} else if exists {
		return errors.New("temporary restore destination already exists")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	v.creationAttempted = true
	if _, err := b.fence.execute(ctx, b.fence.kubectl, v.arguments(false, temporaryCreateScript, b.options.RestoreDatabase, v.marker)); err != nil {
		return err
	}
	if err := v.requirePresent(ctx); err != nil {
		return err
	}
	if v.oid == 0 {
		return errors.New("temporary database creation did not establish owned identity")
	}
	if _, _, err := v.input(ctx, b.fence.kubectl, v.arguments(true, temporaryRestoreScript, b.options.RestoreDatabase), &v.backup.artifact, b.options.ArtifactPolicy.MaximumBytes); err != nil {
		return err
	}
	if err := v.requirePresent(ctx); err != nil {
		return err
	}
	raw, err := b.fence.execute(ctx, b.fence.kubectl, v.arguments(false, temporarySchemaScript, b.options.RestoreDatabase))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := parseDatabaseState(raw, b.options.RestoreDatabase, b.options.MinimumSchema, b.options.TargetSchema)
	if err != nil {
		return err
	}
	if state != v.backup.schema {
		return errors.New("temporary restore schema differs from retained backup")
	}
	if err := v.requirePresent(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := v.sourceState(ctx); err != nil {
		return err
	}
	v.restored = true
	return nil
}

// Reconcile binds an existing destination only after this invocation attempted
// creation and its exact operation marker and database role match. A changed
// OID is a recreated identity, never re-admitted. Missing/unmarked/foreign rows
// cannot authorize cleanup. OID/marker checks are observations, not a database
// lock against an independently privileged operator changing identity afterward.
func (v *TemporaryDatabase) Reconcile(ctx context.Context) (bool, error) {
	if v == nil || v.backup == nil || ctx == nil {
		return false, errors.New("explicit temporary restore binding and context required")
	}
	if _, err := v.backup.source.observe(ctx); err != nil {
		return false, err
	}
	if !v.creationAttempted {
		return false, errors.New("temporary database was not created by this invocation")
	}
	oid, exists, err := v.identity(ctx)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if v.oid != 0 && v.oid != oid {
		return false, errors.New("temporary database was recreated")
	}
	v.oid = oid
	return true, nil
}

func (v *TemporaryDatabase) requirePresent(ctx context.Context) error {
	exists, err := v.Reconcile(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("owned temporary database disappeared")
	}
	return nil
}

// Cleanup removes only the exact observed temporary OID, owner role and marker.
// The SQL rechecks those conditions immediately before DROP on the same psql
// connection, without FORCE or source database replacement. The backup capture
// and checkpoint are never deleted. A failed/unknown command preserves identity
// for reconciliation; cancellation does not establish that DROP rolled back.
func (v *TemporaryDatabase) Cleanup(ctx context.Context) error {
	exists, err := v.Reconcile(ctx)
	if err != nil || !exists {
		return err
	}
	b := v.backup.source
	if _, err := b.fence.execute(ctx, b.fence.kubectl, v.arguments(false, temporaryDropScript, b.options.RestoreDatabase, v.marker, strconv.FormatUint(uint64(v.oid), 10))); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, exists, err := v.identity(ctx); err != nil {
		return err
	} else if exists {
		return errors.New("temporary database remains after cleanup")
	}
	return nil
}

func parseTemporaryIdentity(raw []byte) (uint32, bool, error) {
	if len(raw) == 0 {
		return 0, false, nil
	}
	if len(raw) > 128 || strings.Count(string(raw), "\n") > 1 {
		return 0, false, errors.New("temporary database identity shape differs")
	}
	fields := strings.Split(strings.TrimSuffix(string(raw), "\n"), "|")
	if len(fields) != 3 || fields[1] != "t" || fields[2] != "t" {
		return 0, false, errors.New("temporary database operation marker or role differs")
	}
	oid, err := strconv.ParseUint(fields[0], 10, 32)
	if err != nil || oid == 0 || strconv.FormatUint(oid, 10) != fields[0] {
		return 0, false, errors.New("temporary database OID differs")
	}
	return uint32(oid), true, nil
}
