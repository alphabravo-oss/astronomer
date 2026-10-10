//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strings"
)

// The initial source handle is private to the actual observed installation.
// Reconstructing an exported identity cannot create a CNPG backup authority.
func newCNPGDatabaseBackup(ctx context.Context, source *CloudNativePGSource, options DatabaseBackupOptions, restore CloudNativePGRestoreOptions) (*SourceDatabaseBackup, error) {
	if ctx == nil || source == nil || source.fence == nil || options.Database != source.options.Database || options.DatabasePodUID != source.initial.PrimaryUID || options.ExpectedMajor != source.options.ExpectedMajor || options.MinimumSchema != source.options.MinimumSchema || options.TargetSchema != source.options.TargetSchema || options.ArtifactPolicy.MaximumBytes != source.options.ArtifactPolicy.MaximumBytes || options.RestoreDatabase != restore.Database || options.Owner != restore.Owner || options.DecryptProofWebhookID != "" {
		return nil, errors.New("initially observed CNPG source, isolated restore and schema bounds differ")
	}
	if _, err := source.observe(ctx); err != nil {
		return nil, err
	}
	b := &SourceDatabaseBackup{fence: source.fence, options: options, cnpg: source, cnpgRestore: restore}
	b.observeBound = func(ctx context.Context) (DatabaseSchemaState, error) {
		view, err := source.observe(ctx)
		if err != nil {
			return DatabaseSchemaState{}, err
		}
		return source.state(ctx, view)
	}
	// This read-only source wrapper must never become a generic CREATE/DROP
	// destination. Concrete CNPG restore has a different independent Pod owner.
	b.argumentsBound = func(_ bool, script string, positionals ...string) []string {
		if script != databaseStateScript && script != databaseDumpScript {
			return nil
		}
		// Legacy observational callers are routed through the bound current member;
		// their real reads use observeBound/Dump, not ambient POSTGRES_* variables.
		pod := source.initial.Primary
		if script == databaseStateScript {
			return source.arguments(pod, cnpgStateScript)
		}
		return source.arguments(pod, cnpgDumpScript)
	}
	return b, ctx.Err()
}

// newCNPGTargetSnapshot admits a recreated member after Apply only when every
// original persistent identity and operator-owned Cluster/Service/app Secret
// remains. The original backup/source handle is never rewritten to that UID.
func newCNPGTargetSnapshot(ctx context.Context, s *CloudNativePGSource) (cnpgSourceSnapshot, error) {
	now, err := s.snapshot(ctx)
	if err != nil {
		return now, err
	}
	before := s.initial
	if before.ClusterUID != now.ClusterUID || before.Image != now.Image || before.Instances != now.Instances || before.ServiceUID != now.ServiceUID || before.SecretUID != now.SecretUID || before.SecretVersion != now.SecretVersion || len(before.Pods) != len(now.Pods) || len(before.Storage) != len(now.Storage) {
		return cnpgSourceSnapshot{}, errors.New("target CNPG operator or persistent owner differs")
	}
	for name, binding := range before.Storage {
		if current, ok := now.Storage[name]; !ok || current != binding {
			return cnpgSourceSnapshot{}, errors.New("target CNPG PVC/PV identity differs")
		}
	}
	// Names and claims remain bound. Only member Pod UIDs may change under the
	// same controller and unchanged ready endpoint/storage checks in snapshot.
	for name, old := range before.Pods {
		current, ok := now.Pods[name]
		if !ok || strings.Join(old.Claims, "\x00") != strings.Join(current.Claims, "\x00") {
			return cnpgSourceSnapshot{}, errors.New("target CNPG member claim identity differs")
		}
	}
	return now, ctx.Err()
}
func observeCNPGTarget(ctx context.Context, source *SourceDatabaseBackup) (DatabaseSchemaState, error) {
	if ctx == nil || source == nil || source.cnpg == nil {
		return DatabaseSchemaState{}, errors.New("owned target CNPG observation required")
	}
	first, err := newCNPGTargetSnapshot(ctx, source.cnpg)
	if err != nil {
		return DatabaseSchemaState{}, err
	}
	state, err := source.cnpg.state(ctx, first)
	if err != nil {
		return DatabaseSchemaState{}, err
	}
	last, err := newCNPGTargetSnapshot(ctx, source.cnpg)
	if err != nil {
		return DatabaseSchemaState{}, err
	}
	if !sameCNPGSource(first, last) || first.Primary != last.Primary || first.PrimaryUID != last.PrimaryUID {
		return DatabaseSchemaState{}, errors.New("target CNPG member/primary changed during SQL observation")
	}
	return state, ctx.Err()
}
