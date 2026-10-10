//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// A private lease names only the exact disposable scope the operator intends.
// It is not sufficient authority: fresh namespace/Pod UID+owner, current-user DB
// ownership, exact original OID, operation-marked restored OID and original
// schema are checked independently before the destructive joined command.
func ReadDisposableReplacementLease(ctx context.Context, path string, initial InitialInstallation) (*upgrade.DisposableLease, error) {
	if ctx == nil || !filepath.IsAbs(path) || initial.ExternalDatabase || initial.CloudNativePG || initial.DatabasePodUID == "" {
		return nil, errors.New("explicit private bundled replacement lease required")
	}
	input, err := bindMetadataArtifact(ctx, path, 8<<10)
	if err != nil {
		return nil, err
	}
	raw, err := readCapture(filepath.Dir(path), filepath.Base(path), 8<<10, "sha256:"+formatHash(input.hash))
	if err != nil {
		return nil, err
	}
	var lease upgrade.DisposableLease
	if jsoncontract.Decode(raw, &lease) != nil || lease.NamespaceUID != initial.Identity.NamespaceUID || lease.DatabasePodUID != initial.DatabasePodUID || lease.DatabaseName != initial.Database || !databaseName.MatchString(lease.RestoreDatabaseName) || !strings.HasPrefix(lease.RestoreDatabaseName, "astronomer_upgrade_verify_") || lease.DatabaseName == lease.RestoreDatabaseName || !kubeName.MatchString(lease.Owner) {
		return nil, errors.New("private disposable lease differs from observed original scope")
	}
	current, err := bindMetadataArtifact(ctx, path, 8<<10)
	if err != nil || !sameArtifact(input.identity, current.identity) || current.hash != input.hash {
		return nil, errors.New("private replacement lease changed during admission")
	}
	copy := lease
	return &copy, ctx.Err()
}

const replacementCatalogScript = `psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname=postgres -v live="$1" -v restored="$2" -v live_marker="$3" -v restore_marker="$4" -AtF '|' <<'SQL'
SELECT datname, oid, datdba = (SELECT oid FROM pg_roles WHERE rolname = current_user), COALESCE(shobj_description(oid,'pg_database'),'') = CASE WHEN datname = :'live' THEN :'live_marker' ELSE :'restore_marker' END FROM pg_database WHERE datname IN (:'live',:'restored') ORDER BY datname;
SQL`
const replacementExecuteScript = `psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname=postgres -v live="$1" -v restored="$2" -v live_marker="$3" -v restore_marker="$4" -v live_oid="$5" -v restore_oid="$6" <<'SQL'
SELECT pg_advisory_lock(hashtextextended('astronomer-owned-replacement:' || :'live',0));
SELECT datname AS bound_live FROM pg_database WHERE datname=:'live' AND oid::text=:'live_oid' AND datdba=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND COALESCE(shobj_description(oid,'pg_database'),'')=:'live_marker' \gset
SELECT datname AS bound_restore FROM pg_database WHERE datname=:'restored' AND oid::text=:'restore_oid' AND datdba=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND COALESCE(shobj_description(oid,'pg_database'),'')=:'restore_marker' \gset
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=:'bound_live' AND pid<>pg_backend_pid();
DROP DATABASE :"bound_live";
ALTER DATABASE :"bound_restore" RENAME TO :"bound_live";
SQL`

// DROP and RENAME cannot be one PostgreSQL transaction. The repeated OID checks
// and advisory lock protect this owned runner's scope, not an atomic lock against
// every unrelated privileged SQL client. Partial/unknown completion is assessed
// using exact retained OIDs and never reported as rollback or automatically replayed.
const replacementDeploymentsTemplate = `{{range .items}}{{if or (eq (index .metadata.labels "app.kubernetes.io/component") "server") (eq (index .metadata.labels "app.kubernetes.io/component") "worker")}}{{.metadata.name}}{{"\t"}}{{.metadata.uid}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/component"}}{{"\t"}}{{.spec.replicas}}{{"\t"}}{{.metadata.resourceVersion}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\n"}}{{end}}{{end}}`

type replacementDeployment struct {
	name, uid, component, resourceVersion string
	replicas                              uint32
}
type replacementDatabase struct {
	name          string
	oid           uint32
	owned, marked bool
}
type DisposableReplacement struct {
	backup                                      *ReleaseBackup
	lease                                       upgrade.DisposableLease
	liveOID, restoreOID                         uint32
	deployments                                 []replacementDeployment
	initialPDBs, quiescedPDBs                   []ownedQuiescedPDB
	quiescedDeployments                         []replacementDeployment
	captured, quiescing, sqlAttempted, replaced bool
}

func NewDisposableReplacement(backup *ReleaseBackup, lease *upgrade.DisposableLease) (*DisposableReplacement, error) {
	if backup == nil || backup.source == nil || backup.source.cnpg != nil || backup.external != nil || backup.recovery == nil || backup.recovery.observation == nil || lease == nil {
		return nil, errors.New("co-owned bundled backup and explicit disposable lease required")
	}
	source := backup.source
	id := source.fence.options.Identity
	if lease.NamespaceUID != id.NamespaceUID || lease.DatabasePodUID != source.options.DatabasePodUID || lease.DatabaseName != source.options.Database || lease.RestoreDatabaseName != source.options.RestoreDatabase || lease.Owner != source.options.Owner {
		return nil, errors.New("disposable replacement and original backup bindings differ")
	}
	return &DisposableReplacement{backup: backup, lease: *lease}, nil
}
func (v *DisposableReplacement) fence(ctx context.Context) error {
	if v == nil || v.backup == nil || ctx == nil {
		return errors.New("explicit replacement owner/context required")
	}
	return v.backup.source.fence.Fence(ctx, v.backup.source.fence.options.Identity, &v.lease)
}
func (v *DisposableReplacement) catalog(ctx context.Context) (map[string]replacementDatabase, error) {
	if err := v.fence(ctx); err != nil {
		return nil, err
	}
	source := v.backup.source
	args := []string{"--kubeconfig", source.fence.options.Kubeconfig, "--context", source.fence.options.Identity.Context, "exec", "--namespace", source.fence.options.Identity.Namespace, source.fence.options.DatabasePod, "--container=postgres", "--", "sh", "-ec", replacementCatalogScript, "sh", v.lease.DatabaseName, v.lease.RestoreDatabaseName, "astronomer-disposable:" + v.lease.Owner, v.backup.temporary.marker}
	raw, err := source.fence.execute(ctx, source.fence.kubectl, args)
	if err != nil {
		return nil, err
	}
	if err := v.fence(ctx); err != nil {
		return nil, err
	}
	return parseReplacementCatalog(raw, v.lease)
}
func parseReplacementCatalog(raw []byte, lease upgrade.DisposableLease) (map[string]replacementDatabase, error) {
	rows, err := readinessRows(raw)
	if err != nil || len(rows) > 2 {
		return nil, errors.New("bounded replacement catalog differs")
	}
	result := map[string]replacementDatabase{}
	for _, row := range rows {
		fields := strings.Split(row, "|")
		if len(fields) != 4 || (fields[0] != lease.DatabaseName && fields[0] != lease.RestoreDatabaseName) {
			return nil, errors.New("replacement DB identity differs")
		}
		oid, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil || oid == 0 || strconv.FormatUint(oid, 10) != fields[1] || (fields[2] != "t" && fields[2] != "f") || (fields[3] != "t" && fields[3] != "f") {
			return nil, errors.New("replacement DB OID/owner/marker differs")
		}
		if _, duplicate := result[fields[0]]; duplicate {
			return nil, errors.New("replacement DB identity duplicated")
		}
		result[fields[0]] = replacementDatabase{fields[0], uint32(oid), fields[2] == "t", fields[3] == "t"}
	}
	return result, nil
}
func (v *DisposableReplacement) workloads(ctx context.Context) ([]replacementDeployment, error) {
	if err := v.fence(ctx); err != nil {
		return nil, err
	}
	f := v.backup.source.fence
	id := f.options.Identity
	args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", id.Context, "get", "deployments", "--namespace", id.Namespace, "--selector", "app.kubernetes.io/instance=" + id.Release + ",app.kubernetes.io/name=astronomer", "--output", "go-template=" + replacementDeploymentsTemplate}
	raw, err := f.execute(ctx, f.kubectl, args)
	if err != nil {
		return nil, err
	}
	if err := v.fence(ctx); err != nil {
		return nil, err
	}
	rows, err := readinessRows(raw)
	if err != nil || len(rows) != 2 {
		return nil, errors.New("replacement requires exactly server and worker deployment bindings")
	}
	result := make([]replacementDeployment, 0, 2)
	names, roles := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		fields := strings.Split(row, "\t")
		if len(fields) != 7 || !kubeName.MatchString(fields[0]) || fields[1] == "" || strings.ContainsAny(fields[1], "\t\r\n ") || fields[2] != id.Namespace || (fields[3] != "server" && fields[3] != "worker") || fields[5] == "" || len(fields[5]) > 128 || strings.ContainsAny(fields[5], "\t\r\n ") || fields[6] != "live" || names[fields[0]] || roles[fields[3]] {
			return nil, errors.New("replacement deployment identity differs")
		}
		count, err := strconv.ParseUint(fields[4], 10, 31)
		if err != nil || strconv.FormatUint(count, 10) != fields[4] {
			return nil, errors.New("replacement replica count is invalid")
		}
		names[fields[0]], roles[fields[3]] = true, true
		result = append(result, replacementDeployment{fields[0], fields[1], fields[3], fields[5], uint32(count)})
	}
	return result, ctx.Err()
}
func (v *DisposableReplacement) Capture(ctx context.Context) error {
	if v.captured || v.sqlAttempted || !v.backup.complete || v.backup.temporary == nil || !v.backup.temporary.restored {
		return errors.New("one acknowledged complete replacement backup required")
	}
	if err := v.backup.VerifyBeforeMutation(ctx); err != nil {
		return err
	}
	catalog, err := v.catalog(ctx)
	if err != nil {
		return err
	}
	live, liveOK := catalog[v.lease.DatabaseName]
	restore, restoreOK := catalog[v.lease.RestoreDatabaseName]
	if !liveOK || !restoreOK || !live.owned || !live.marked || !restore.owned || !restore.marked || restore.oid != v.backup.temporary.oid || live.oid == restore.oid {
		return errors.New("independent disposable live and operation-restored DB OIDs required")
	}
	deployments, err := v.workloads(ctx)
	if err != nil {
		return err
	}
	v.liveOID, v.restoreOID, v.deployments = live.oid, restore.oid, deployments
	private := []byte("original_oid=" + strconv.FormatUint(uint64(live.oid), 10) + "\nrestored_oid=" + strconv.FormatUint(uint64(restore.oid), 10) + "\n")
	if _, err := writeBackupBytes(ctx, v.backup.commands(), "replacement-original-oids.txt", private, 1<<10); err != nil {
		return err
	}
	if err := v.capturePDBOwners(ctx); err != nil {
		return err
	}
	v.captured = true
	return ctx.Err()
}
func (v *DisposableReplacement) scale(ctx context.Context, previous replacementDeployment, count uint32) error {
	fresh, err := v.workloads(ctx)
	if err != nil {
		return err
	}
	found := false
	version := ""
	for _, current := range fresh {
		if current.name == previous.name {
			found = current.uid == previous.uid && current.component == previous.component
			version = current.resourceVersion
		}
	}
	if !found {
		return errors.New("replacement deployment recreated before scale")
	}
	f := v.backup.source.fence
	id := f.options.Identity
	args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", id.Context, "scale", "--namespace", id.Namespace, "deployment/" + previous.name, "--replicas=" + strconv.FormatUint(uint64(count), 10), "--resource-version=" + version}
	if _, err := f.execute(ctx, f.kubectl, args); err != nil {
		return err
	}
	return v.fence(ctx)
}
func (v *DisposableReplacement) Replace(ctx context.Context) (upgrade.Outcome, error) {
	known := upgrade.Outcome{Known: true}
	if v == nil || ctx == nil || !v.captured || v.sqlAttempted || v.replaced {
		return known, errors.New("single owned replacement attempt required")
	}
	if err := v.backup.VerifyBeforeMutation(ctx); err != nil {
		return known, err
	}
	v.quiescing = true
	for _, deployment := range v.deployments {
		if err := v.scale(ctx, deployment, 0); err != nil {
			return known, err
		}
	}
	f := v.backup.source.fence
	id := f.options.Identity
	for _, deployment := range v.deployments {
		args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", id.Context, "rollout", "status", "--namespace", id.Namespace, "deployment/" + deployment.name, "--timeout", v.backup.recovery.observation.release.plan.Timeout.String()}
		if _, err := f.execute(ctx, f.kubectl, args); err != nil {
			return known, err
		}
	}
	fresh, err := v.workloads(ctx)
	if err != nil {
		return known, err
	}
	for _, original := range v.deployments {
		found := false
		for _, current := range fresh {
			if current.name == original.name && current.uid == original.uid && current.component == original.component && current.replicas == 0 {
				found = true
			}
		}
		if !found {
			return known, errors.New("quiesced workload identity changed before DROP")
		}
	}
	rows, err := v.catalog(ctx)
	if err != nil {
		return known, err
	}
	live, restore := rows[v.lease.DatabaseName], rows[v.lease.RestoreDatabaseName]
	if live.oid != v.liveOID || !live.owned || !live.marked || restore.oid != v.restoreOID || !restore.owned || !restore.marked {
		return known, errors.New("captured replacement DB ownership changed before DROP")
	}
	if err := v.fence(ctx); err != nil {
		return known, err
	}
	v.sqlAttempted = true
	args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", id.Context, "exec", "--namespace", id.Namespace, f.options.DatabasePod, "--container=postgres", "--", "sh", "-ec", replacementExecuteScript, "sh", v.lease.DatabaseName, v.lease.RestoreDatabaseName, "astronomer-disposable:" + v.lease.Owner, v.backup.temporary.marker, strconv.FormatUint(uint64(v.liveOID), 10), strconv.FormatUint(uint64(v.restoreOID), 10)}
	if _, err := f.execute(ctx, f.kubectl, args); err != nil {
		return upgrade.Outcome{}, err
	}
	// Even a successful child does not establish which OID survived a partial
	// operation or subsequent recreation. Only fresh retained-OID assessment does.
	outcome, err := v.Reconcile(ctx)
	if err != nil || !outcome.Replaced {
		return upgrade.Outcome{}, errors.Join(errors.New("replacement joined result requires retained OID assessment"), err, ctx.Err())
	}
	if err := v.captureQuiesced(ctx); err != nil {
		return outcome, err
	}
	return outcome, ctx.Err()
}
func (v *DisposableReplacement) Reconcile(ctx context.Context) (upgrade.Outcome, error) {
	if v == nil || ctx == nil || !v.captured {
		return upgrade.Outcome{}, errors.New("captured replacement assessment required")
	}
	// After rename the live DB legitimately retains the restore marker. Request
	// that exact marker only when the original live name's OID could have changed.
	rows, err := v.catalog(ctx)
	if err != nil {
		return upgrade.Outcome{}, err
	}
	live, liveOK := rows[v.lease.DatabaseName]
	restore, restoreOK := rows[v.lease.RestoreDatabaseName]
	if liveOK && restoreOK && live.oid == v.liveOID && live.owned && live.marked && restore.oid == v.restoreOID && restore.owned && restore.marked {
		return upgrade.Outcome{Known: true}, nil
	}
	if liveOK && !restoreOK && live.oid == v.restoreOID && live.owned {
		f := v.backup.source.fence
		args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", f.options.Identity.Context, "exec", "--namespace", f.options.Identity.Namespace, f.options.DatabasePod, "--container=postgres", "--", "sh", "-ec", replacementCatalogScript, "sh", v.lease.DatabaseName, v.lease.RestoreDatabaseName, v.backup.temporary.marker, v.backup.temporary.marker}
		raw, err := f.execute(ctx, f.kubectl, args)
		if err != nil {
			return upgrade.Outcome{}, err
		}
		checked, err := parseReplacementCatalog(raw, v.lease)
		if err != nil || len(checked) != 1 || !checked[v.lease.DatabaseName].owned || !checked[v.lease.DatabaseName].marked || checked[v.lease.DatabaseName].oid != v.restoreOID {
			return upgrade.Outcome{}, errors.New("renamed replacement DB ownership differs")
		}
		if err := v.fence(ctx); err != nil {
			return upgrade.Outcome{}, err
		}
		v.replaced = true
		return upgrade.Outcome{Known: true, Replaced: true}, ctx.Err()
	}
	return upgrade.Outcome{}, errors.New("replacement outcome remains unknown; preserve all recovery handles")
}
func (v *DisposableReplacement) RestoreReplicas(ctx context.Context) error {
	if v == nil || ctx == nil {
		return errors.New("explicit replica restoration owner/context required")
	}
	if !v.quiescing {
		return ctx.Err()
	}
	state, err := v.backup.recovery.observation.Read(ctx)
	if err != nil {
		return err
	}
	// A deployed target's Helm values own replica counts. Restore old counts only
	// after an independently observed old release (including an atomic rollback).
	r := v.backup.recovery.observation.release
	if state.Status != "deployed" {
		return errors.New("unknown release state cannot restore replicas")
	}
	if releaseVersion(state, r.plan.Target) {
		return ctx.Err()
	}
	if !releaseVersion(state, r.plan.Previous) {
		return errors.New("foreign release state cannot restore replicas")
	}
	assessed, err := v.Reconcile(ctx)
	if err != nil || !assessed.Known {
		return errors.New("unknown database replacement cannot authorize replica recovery")
	}
	for _, deployment := range v.deployments {
		if err := v.scale(ctx, deployment, deployment.replicas); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func sameReleasePlan(a, b upgrade.Plan) bool {
	if (a.Replace == nil) != (b.Replace == nil) {
		return false
	}
	if a.Replace != nil && *a.Replace != *b.Replace {
		return false
	}
	a.Replace, b.Replace = nil, nil
	return a == b
}

// formatHash renders only a private file identity already calculated in Go; it
// never logs artifact bytes or supplies independent signature authority.
func formatHash(hash [32]byte) string {
	const alphabet = "0123456789abcdef"
	raw := make([]byte, 64)
	for i, value := range hash {
		raw[i*2] = alphabet[value>>4]
		raw[i*2+1] = alphabet[value&15]
	}
	return string(raw)
}
