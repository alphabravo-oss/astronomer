//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strconv"
)

// targetDatabaseCommand uses the same exact credential-free remote expansion
// and retained temporary name/marker as the original restore adapter. It adds
// post-upgrade storage observation without weakening the original source Pod
// fence. Pre/post checks are point-in-time; an independent privileged operator
// can race them. Mutating SQL separately checks the retained OID/role/marker.
func (o *TargetDatabaseObservation) targetDatabaseCommand(ctx context.Context, script string, positionals ...string) ([]byte, error) {
	if _, err := o.Observe(ctx); err != nil {
		return nil, err
	}
	f := o.source.fence
	args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", f.options.Identity.Context, "exec", "--namespace", f.options.Identity.Namespace, f.options.DatabasePod, "--container=postgres", "--", "sh", "-ec", script}
	if len(positionals) > 0 {
		args = append(args, "sh")
		args = append(args, positionals...)
	}
	raw, err := f.execute(ctx, f.kubectl, args)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := o.Observe(ctx); err != nil {
		return nil, err
	}
	return raw, nil
}

// cleanupWithTarget is available only to the co-owned complete Driver. Public
// TemporaryDatabase.Cleanup/Reconcile retain the original backup Pod UID. No
// backup/checkpoint/command original is removed; unknown SQL completion retains
// the existing handle. This neither replaces the source DB nor reverses schema.
func (v *TemporaryDatabase) cleanupWithTarget(ctx context.Context, target *TargetDatabaseObservation) error {
	if ctx == nil || v == nil || v.backup == nil || target == nil || v.backup.source != target.source || !v.creationAttempted {
		return errors.New("co-owned attempted temporary restore and target context required")
	}
	b := v.backup.source
	read := func() (uint32, bool, error) {
		raw, err := target.targetDatabaseCommand(ctx, temporaryIdentityScript, b.options.RestoreDatabase, v.marker)
		if err != nil {
			return 0, false, err
		}
		return parseTemporaryIdentity(raw)
	}
	oid, exists, err := read()
	if err != nil || !exists {
		return err
	}
	if v.oid != 0 && v.oid != oid {
		return errors.New("temporary database was recreated before target cleanup")
	}
	v.oid = oid
	if _, err := target.targetDatabaseCommand(ctx, temporaryDropScript, b.options.RestoreDatabase, v.marker, strconv.FormatUint(uint64(oid), 10)); err != nil {
		return err
	}
	if _, exists, err := read(); err != nil {
		return err
	} else if exists {
		return errors.New("temporary database remains after target cleanup")
	}
	return ctx.Err()
}

func (b *ReleaseBackup) cleanupWithTarget(ctx context.Context, target *TargetDatabaseObservation) error {
	if b == nil || ctx == nil || target == nil || target.source != b.source {
		return errors.New("co-owned target backup cleanup required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cnpgRestore != nil {
		return b.cnpgRestore.Cleanup(ctx)
	}
	if b.temporary == nil {
		return ctx.Err()
	}
	return b.temporary.cleanupWithTarget(ctx, target)
}
