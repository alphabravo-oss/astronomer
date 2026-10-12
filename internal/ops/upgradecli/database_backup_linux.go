//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// These constants preserve the remote POSTGRES_* expansion used by the legacy
// bundled-database adapter. No secret is expanded locally or placed in argv.
const databaseStateScript = `psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -AtF "|" -c "SELECT current_database(), current_setting('server_version_num')::bigint / 10000, count(*), COALESCE(max(version),0), COALESCE(bool_or(dirty),false) FROM schema_migrations"`
const databaseDumpScript = `pg_dump --format=custom --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"`

const databasePodTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/instance"}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/component"}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}`

var databaseName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type DatabaseBackupOptions struct {
	DecryptProofWebhookID string
	Database              string
	RestoreDatabase       string
	DatabasePodUID        string
	Owner                 string
	ExpectedMajor         uint32
	MinimumSchema         uint32
	TargetSchema          uint32
	ArtifactPolicy        ArtifactPolicy
}

type DatabaseSchemaState struct{ Major, Version uint32 }

// SourceDatabaseBackup binds one existing bundled database and pod UID. It does
// not authorize replacement. Target schema bounds must come from the verified
// exact target chart in Driver.Verify, not local latest-version defaults.
// Constructor admission and successful dumping alone do not certify a readable
// archive or a temporary restore; those later operations retain their own gates.
type SourceDatabaseBackup struct {
	cnpg           *CloudNativePGSource
	cnpgRestore    CloudNativePGRestoreOptions
	observeBound   func(context.Context) (DatabaseSchemaState, error)
	argumentsBound func(bool, string, ...string) []string
	fence          *OwnershipFence
	options        DatabaseBackupOptions
	capture        func(context.Context, Tool, []string, ArtifactPolicy) (*process.ArtifactCapture, process.Result, error)
}

// DatabaseBackup retains private recovery material and its initial schema
// binding. Its fields are private to this composition package, so an arbitrary
// public metadata object cannot become restore authority.
type DatabaseBackup struct {
	cnpg     *CloudNativePGBackup
	source   *SourceDatabaseBackup
	artifact process.ArtifactCapture
	schema   DatabaseSchemaState
}

func NewSourceDatabaseBackup(fence *OwnershipFence, options DatabaseBackupOptions) (*SourceDatabaseBackup, error) {
	if fence == nil || fence.commands == nil || fence.options.DatabasePod == "" || !databaseName.MatchString(options.Database) || !databaseName.MatchString(options.RestoreDatabase) || !strings.HasPrefix(options.RestoreDatabase, "astronomer_upgrade_verify_") || options.Database == options.RestoreDatabase || options.DatabasePodUID == "" || options.Owner == "" || strings.ContainsAny(options.DatabasePodUID+options.Owner, "\t\r\n") || options.ExpectedMajor == 0 || options.MinimumSchema == 0 || options.TargetSchema < options.MinimumSchema || options.ArtifactPolicy.MaximumBytes <= 0 {
		return nil, errors.New("explicit bundled database identity, schema, temporary name and capture policy required")
	}
	if options.DecryptProofWebhookID != "" {
		id, err := uuid.Parse(options.DecryptProofWebhookID)
		if err != nil || id.String() != options.DecryptProofWebhookID {
			return nil, errors.New("RC_DECRYPT_PROOF_WEBHOOK_ID requires canonical UUID")
		}
	}
	return &SourceDatabaseBackup{fence: fence, options: options, capture: fence.commands.CaptureArtifact}, nil
}

func (b *SourceDatabaseBackup) observe(ctx context.Context) (DatabaseSchemaState, error) {
	var empty DatabaseSchemaState
	if ctx == nil || b == nil {
		return empty, errors.New("explicit source database context and binding required")
	}
	if b.observeBound != nil {
		return b.observeBound(ctx)
	}
	if err := b.fence.Fence(ctx, b.fence.options.Identity, nil); err != nil {
		return empty, err
	}
	pod, err := b.fence.execute(ctx, b.fence.kubectl, []string{"--kubeconfig", b.fence.options.Kubeconfig, "--context", b.fence.options.Identity.Context, "get", "pod", b.fence.options.DatabasePod, "--namespace", b.fence.options.Identity.Namespace, "--output", "go-template=" + databasePodTemplate})
	if err != nil {
		return empty, err
	}
	if !equalFields(pod, b.options.DatabasePodUID, b.fence.options.DatabasePod, b.fence.options.Identity.Namespace, b.fence.options.Identity.Release, "postgres", "live") {
		return empty, errors.New("bound release database pod identity or lifecycle differs")
	}
	raw, err := b.fence.execute(ctx, b.fence.kubectl, b.arguments(false, databaseStateScript))
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	state, err := parseDatabaseState(raw, b.options.Database, b.options.MinimumSchema, b.options.TargetSchema)
	if err != nil {
		return empty, err
	}
	if state.Major != b.options.ExpectedMajor {
		return empty, errors.New("bound bundled database server major differs")
	}
	return state, nil
}

func (b *SourceDatabaseBackup) Dump(ctx context.Context) (*DatabaseBackup, process.Result, error) {
	var empty process.Result
	if b != nil && b.cnpg != nil {
		backup, result, err := b.cnpg.Dump(ctx)
		if err != nil {
			return nil, result, err
		}
		return &DatabaseBackup{source: b, artifact: backup.artifact, schema: backup.schema, cnpg: backup}, result, nil
	}
	state, err := b.observe(ctx)
	if err != nil {
		return nil, empty, err
	}
	artifact, result, err := b.capture(ctx, b.fence.kubectl, b.arguments(false, databaseDumpScript), b.options.ArtifactPolicy)
	if err != nil {
		return nil, result, err
	}
	if artifact == nil || !artifact.Acknowledged {
		return nil, result, errors.New("private database dump did not acknowledge retained bytes")
	}
	current, err := b.observe(ctx)
	if err != nil {
		return nil, result, err
	}
	if current != state {
		return nil, result, errors.New("source database schema changed during backup capture")
	}
	return &DatabaseBackup{source: b, artifact: *artifact, schema: state}, result, nil
}

func (b *SourceDatabaseBackup) arguments(stdin bool, script string, positionals ...string) []string {
	if b.argumentsBound != nil {
		return b.argumentsBound(stdin, script, positionals...)
	}
	args := []string{"--kubeconfig", b.fence.options.Kubeconfig, "--context", b.fence.options.Identity.Context, "exec", "--namespace", b.fence.options.Identity.Namespace}
	if stdin {
		args = append(args, "-i")
	}
	args = append(args, b.fence.options.DatabasePod, "--", "sh", "-ec", script)
	if len(positionals) > 0 {
		args = append(args, "sh")
		args = append(args, positionals...)
	}
	return args
}

func parseDatabaseState(raw []byte, name string, minimum, target uint32) (DatabaseSchemaState, error) {
	var empty DatabaseSchemaState
	if len(raw) > 256 || strings.Count(string(raw), "\n") > 1 {
		return empty, errors.New("bundled database schema observation shape differs")
	}
	line := strings.TrimSuffix(string(raw), "\n")
	fields := strings.Split(line, "|")
	if len(fields) != 5 || fields[0] != name || fields[2] != "1" || fields[4] != "f" {
		return empty, errors.New("bundled database requires one clean bound schema row")
	}
	major, err := strconv.ParseUint(fields[1], 10, 32)
	if err != nil || strconv.FormatUint(major, 10) != fields[1] || major == 0 {
		return empty, errors.New("bundled database server major observation differs")
	}
	version, err := strconv.ParseUint(fields[3], 10, 32)
	if err != nil || strconv.FormatUint(version, 10) != fields[3] || version < uint64(minimum) || version > uint64(target) {
		return empty, errors.New("bundled database schema outside verified target range")
	}
	return DatabaseSchemaState{Major: uint32(major), Version: uint32(version)}, nil
}
