//go:build linux

package upgradecli

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

const cnpgVerifierStartScript = `umask 077; test "$(id -u)" = 26; mkdir -p "$PGDATA"; initdb --auth=trust --username=postgres --pgdata="$PGDATA"; exec postgres --pgdata="$PGDATA" -c listen_addresses= -c unix_socket_directories=/var/lib/postgresql/upgrade-verifier`

const cnpgRestorePodTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{index .metadata.labels "astronomer.dev/upgrade-verifier"}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{if .metadata.ownerReferences}}{{len .metadata.ownerReferences}}{{else}}0{{end}}{{"\t"}}{{len .spec.containers}}{{"\t"}}{{range .spec.containers}}{{.image}}{{end}}{{"\t"}}{{if .status.phase}}{{.status.phase}}{{else}}Pending{{end}}{{"\t"}}{{range .spec.volumes}}{{.name}}{{"="}}{{if .emptyDir}}emptydir={{.emptyDir.sizeLimit}}{{else}}foreign{{end}}{{end}}{{"\t"}}{{.spec.automountServiceAccountToken}}{{"\t"}}{{if .spec.initContainers}}{{len .spec.initContainers}}{{else}}0{{end}}{{"\t"}}{{if .spec.ephemeralContainers}}{{len .spec.ephemeralContainers}}{{else}}0{{end}}{{"\t"}}{{range .spec.containers}}{{range .command}}{{.}}{{"|"}}{{end}}{{";"}}{{range .args}}{{.}}{{"|"}}{{end}}{{end}}{{"\t"}}{{range .spec.containers}}{{range .env}}{{.name}}{{"="}}{{.value}}{{if .valueFrom}}foreign{{end}}{{"|"}}{{end}}{{end}}{{"\t"}}{{.spec.securityContext.runAsUser}}{{"\t"}}{{.spec.securityContext.fsGroup}}{{"\t"}}{{.spec.securityContext.runAsNonRoot}}{{"\t"}}{{range .spec.containers}}{{index .resources.requests "ephemeral-storage"}}{{end}}{{"\t"}}{{range .spec.containers}}{{index .resources.limits "ephemeral-storage"}}{{end}}{{"\t"}}{{range .spec.containers}}{{range .volumeMounts}}{{.name}}{{"="}}{{.mountPath}}{{"="}}{{if .readOnly}}readonly{{else}}readwrite{{end}}{{"|"}}{{end}}{{end}}{{"\t"}}{{.spec.restartPolicy}}{{"\t"}}{{.spec.securityContext.runAsGroup}}{{"\t"}}{{.spec.securityContext.seccompProfile.type}}{{"\t"}}{{range .spec.containers}}{{.securityContext.allowPrivilegeEscalation}}{{end}}{{"\t"}}{{range .spec.containers}}{{range .securityContext.capabilities.drop}}{{.}}{{"|"}}{{end}}{{end}}{{"\t"}}{{range .spec.containers}}{{.securityContext.readOnlyRootFilesystem}}{{end}}`

type CloudNativePGRestoreOptions struct {
	Pod, Owner, Database string
	MaximumStorageBytes  int64
	WaitSeconds          uint32
}

// CloudNativePGRestore owns a fresh, isolated Pod running the exact digest
// configured in the authenticated target chart for the same observed major.
// This chart content binding is not an independent operand image signature. Its
// emptyDir and scheduler request have an explicit storage limit. Restricted-PSS
// fields are explicit; the root filesystem is read-only and TMPDIR/socket/data
// writes stay on the independently owned emptyDir. Actual image/runtime support
// still requires a live restore rehearsal. It never uses
// any CNPG PVC, Secret, Service, Cluster edit or source database CREATE/DROP.
// A scheduler/restore/storage failure retains the original dump and assessment;
// this source adapter alone is not an actual Kubernetes/PostgreSQL rehearsal.
type CloudNativePGRestore struct {
	mu                       sync.Mutex
	chart                    *TargetChartContent
	backup                   *CloudNativePGBackup
	options                  CloudNativePGRestoreOptions
	image                    string
	uid                      string
	creationAttempted        bool
	deleteAttempted          bool
	temporary                *TemporaryDatabase
	createInput, deleteInput *process.ArtifactCapture
	execute                  func(context.Context, Tool, []string) ([]byte, error)
	input                    func(context.Context, Tool, []string, *process.ArtifactCapture, int64) ([]byte, process.Result, error)
}

func NewCloudNativePGRestore(ctx context.Context, chart *TargetChartContent, backup *CloudNativePGBackup, options CloudNativePGRestoreOptions) (*CloudNativePGRestore, error) {
	if ctx == nil || chart == nil || chart.metadata == nil || backup == nil || backup.source == nil || !backup.artifact.Acknowledged || backup.artifact.SizeBytes <= 0 || !kubeName.MatchString(options.Pod) || !strings.HasPrefix(options.Pod, "astronomer-upgrade-verify-") || !kubeName.MatchString(options.Owner) || !databaseName.MatchString(options.Database) || !strings.HasPrefix(options.Database, "astronomer_upgrade_verify_") || options.Database == backup.source.options.Database || options.MaximumStorageBytes <= 0 || options.WaitSeconds == 0 {
		return nil, errors.New("bound CNPG backup and explicit isolated restore ownership/storage/wait required")
	}
	image, major, err := chart.CNPGImage(ctx)
	if err != nil || major != backup.schema.Major {
		return nil, errors.New("CNPG restore requires the same authenticated chart operand major")
	}
	source := backup.source
	copy := *backup
	return &CloudNativePGRestore{chart: chart, backup: &copy, options: options, image: image, execute: source.fence.execute, input: source.fence.commands.ExecuteArtifactInput}, ctx.Err()
}

func (r *CloudNativePGRestore) arguments(args ...string) []string {
	f := r.backup.source.fence
	id := f.options.Identity
	return append([]string{"--kubeconfig", f.options.Kubeconfig, "--context", id.Context}, args...)
}
func (r *CloudNativePGRestore) sourceState(ctx context.Context) error {
	if r == nil || ctx == nil {
		return errors.New("bound isolated restore context required")
	}
	view, err := r.backup.source.observe(ctx)
	if err != nil {
		return err
	}
	state, err := r.backup.source.state(ctx, view)
	if err != nil {
		return err
	}
	if state != r.backup.schema {
		return errors.New("CNPG source schema differs from retained dump")
	}
	return ctx.Err()
}
func (r *CloudNativePGRestore) observePod(ctx context.Context) (bool, error) {
	if ctx == nil || r == nil {
		return false, errors.New("explicit isolated verifier Pod observation required")
	}
	f := r.backup.source.fence
	if err := f.Fence(ctx, f.options.Identity, nil); err != nil {
		return false, err
	}
	raw, err := r.execute(ctx, f.kubectl, r.arguments("get", "pod", r.options.Pod, "--namespace", f.options.Identity.Namespace, "--ignore-not-found", "--output", "go-template="+cnpgRestorePodTemplate))
	if err != nil {
		return false, err
	}
	if len(raw) == 0 {
		return false, ctx.Err()
	}
	fields, err := cnpgFields(raw, 27)
	if err != nil {
		return false, err
	}
	if fields[1] != r.options.Pod || fields[2] != f.options.Identity.Namespace || fields[3] != r.options.Owner || (fields[4] != "live" && (!r.deleteAttempted || fields[4] != "deleted")) || fields[5] != "0" || fields[6] != "1" || fields[7] != r.image || fields[9] != "verify=emptydir="+resource.NewQuantity(r.options.MaximumStorageBytes, resource.BinarySI).String() || fields[10] != "false" || fields[11] != "0" || fields[12] != "0" || fields[13] != "/bin/sh|-ec|;"+cnpgVerifierStartScript+"|" || fields[14] != "POSTGRES_DB=postgres|POSTGRES_USER=postgres|PGHOST=/var/lib/postgresql/upgrade-verifier|PGDATA=/var/lib/postgresql/upgrade-verifier/data|TMPDIR=/var/lib/postgresql/upgrade-verifier|" {
		return false, errors.New("isolated restore Pod ownership/image/volume differs")
	}
	if fields[15] != "26" || fields[16] != "26" || fields[17] != "true" || fields[18] != resource.NewQuantity(r.options.MaximumStorageBytes, resource.BinarySI).String() || fields[19] != fields[18] || fields[20] != "verify=/var/lib/postgresql=readwrite|" || fields[21] != "Never" || fields[22] != "26" || fields[23] != "RuntimeDefault" || fields[24] != "false" || fields[25] != "ALL|" || fields[26] != "true" {
		return false, errors.New("isolated verifier nonroot/storage resources differ")
	}
	if !r.creationAttempted {
		return false, errors.New("isolated verifier Pod already exists before owned creation")
	}
	if r.uid != "" && fields[0] != r.uid {
		return false, errors.New("isolated verifier Pod was recreated")
	}
	r.uid = fields[0]
	if err = f.Fence(ctx, f.options.Identity, nil); err != nil {
		return false, err
	}
	return true, ctx.Err()
}
func (r *CloudNativePGRestore) requirePod(ctx context.Context) error {
	present, err := r.observePod(ctx)
	if err != nil {
		return err
	}
	if !present {
		return errors.New("owned isolated verifier Pod disappeared")
	}
	return nil
}
func (r *CloudNativePGRestore) podManifest() ([]byte, error) {
	storage := resource.NewQuantity(r.options.MaximumStorageBytes, resource.BinarySI)
	pod := corev1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Name: r.options.Pod, Namespace: r.backup.source.fence.options.Identity.Namespace, Labels: map[string]string{"astronomer.dev/upgrade-verifier": r.options.Owner}}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, AutomountServiceAccountToken: boolPointer(false), SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: boolPointer(true), RunAsUser: int64Pointer(26), RunAsGroup: int64Pointer(26), FSGroup: int64Pointer(26), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}, Containers: []corev1.Container{{Name: "postgres", SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: boolPointer(false), ReadOnlyRootFilesystem: boolPointer(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}, Image: r.image, ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{"/bin/sh", "-ec"}, Args: []string{cnpgVerifierStartScript}, Env: []corev1.EnvVar{{Name: "POSTGRES_DB", Value: "postgres"}, {Name: "POSTGRES_USER", Value: "postgres"}, {Name: "PGHOST", Value: "/var/lib/postgresql/upgrade-verifier"}, {Name: "PGDATA", Value: "/var/lib/postgresql/upgrade-verifier/data"}, {Name: "TMPDIR", Value: "/var/lib/postgresql/upgrade-verifier"}}, ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"pg_isready", "--host=/var/lib/postgresql/upgrade-verifier", "--username=postgres", "--dbname=postgres"}}}, PeriodSeconds: 1, TimeoutSeconds: 1}, VolumeMounts: []corev1.VolumeMount{{Name: "verify", MountPath: "/var/lib/postgresql"}}, Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceEphemeralStorage: *storage}, Limits: corev1.ResourceList{corev1.ResourceEphemeralStorage: *storage}}}}, Volumes: []corev1.Volume{{Name: "verify", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: storage}}}}}}
	return json.Marshal(pod)
}
func boolPointer(value bool) *bool    { return &value }
func int64Pointer(value int64) *int64 { return &value }

// Verify is one-shot. Creation is recorded before the joined API call; an
// unknown child result requires a fresh UID/owner/image/volume reconciliation.
// pg_restore --list, actual restore and clean schema/major are delegated to the
// existing concrete TemporaryDatabase adapter on this owned Pod only.
func (r *CloudNativePGRestore) Verify(ctx context.Context) error {
	if r == nil || ctx == nil {
		return errors.New("explicit isolated CNPG restore required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.creationAttempted {
		return errors.New("isolated restore attempted; reconcile rather than replay")
	}
	if err := r.sourceState(ctx); err != nil {
		return err
	}
	if present, err := r.observePod(ctx); err != nil || present {
		return errors.New("isolated verifier destination must be absent")
	}
	manifest, err := r.podManifest()
	if err != nil {
		return err
	}
	commands := r.backup.source.fence.commands
	retained, err := writeBackupBytes(ctx, commands, "cnpg-restore-pod.json", manifest, 1<<20)
	if err != nil {
		return err
	}
	r.createInput = &retained
	if err = ctx.Err(); err != nil {
		return err
	}
	r.creationAttempted = true
	if _, _, err = r.input(ctx, r.backup.source.fence.kubectl, r.arguments("create", "--filename", "-"), r.createInput, 1<<20); err != nil {
		return err
	}
	if err = r.requirePod(ctx); err != nil {
		return err
	}
	if _, err = r.execute(ctx, r.backup.source.fence.kubectl, r.arguments("wait", "--for=condition=Ready", "pod/"+r.options.Pod, "--namespace", r.backup.source.fence.options.Identity.Namespace, "--timeout="+strconv.FormatUint(uint64(r.options.WaitSeconds), 10)+"s")); err != nil {
		return err
	}
	if err = r.requirePod(ctx); err != nil {
		return err
	}
	if err = r.sourceState(ctx); err != nil {
		return err
	}
	// The copied generic source observes the real CNPG original, but every SQL
	// and stdin command is fenced to the independent new Pod. None can execute
	// CREATE, restore or DROP on a source CNPG member.
	original := r.backup.source
	cloneFence := *original.fence
	cloneFence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if err := r.requirePod(ctx); err != nil {
			return nil, err
		}
		return original.fence.execute(ctx, tool, args)
	}
	restoreSource := &SourceDatabaseBackup{fence: &cloneFence, options: DatabaseBackupOptions{Database: original.options.Database, RestoreDatabase: r.options.Database, DatabasePodUID: r.uid, Owner: r.options.Owner, ExpectedMajor: r.backup.schema.Major, MinimumSchema: original.options.MinimumSchema, TargetSchema: original.options.TargetSchema, ArtifactPolicy: original.options.ArtifactPolicy}}
	restoreSource.observeBound = func(ctx context.Context) (DatabaseSchemaState, error) {
		if err := r.sourceState(ctx); err != nil {
			return DatabaseSchemaState{}, err
		}
		return r.backup.schema, nil
	}
	restoreSource.argumentsBound = func(stdin bool, script string, positionals ...string) []string {
		args := r.arguments("exec", "--namespace", original.fence.options.Identity.Namespace)
		if stdin {
			args = append(args, "-i")
		}
		args = append(args, r.options.Pod, "--", "sh", "-ec", script)
		if len(positionals) > 0 {
			args = append(args, "sh")
			args = append(args, positionals...)
		}
		return args
	}
	generic := &DatabaseBackup{source: restoreSource, artifact: r.backup.artifact, schema: r.backup.schema}
	temporary, err := NewTemporaryDatabase(generic)
	if err != nil {
		return err
	}
	r.temporary = temporary
	temporary.input = func(ctx context.Context, tool Tool, args []string, artifact *process.ArtifactCapture, maximum int64) ([]byte, process.Result, error) {
		if err := r.requirePod(ctx); err != nil {
			return nil, process.Result{}, err
		}
		raw, result, err := r.input(ctx, tool, args, artifact, maximum)
		if err != nil {
			return raw, result, err
		}
		return raw, result, r.requirePod(ctx)
	}
	if err = temporary.Verify(ctx); err != nil {
		return err
	}
	if err = r.requirePod(ctx); err != nil {
		return err
	}
	return r.sourceState(ctx)
}

// Cleanup targets only this attempted Pod with a server-side UID precondition.
// A canceled request, failed delete or recreated Pod is retained for assessment.
// No source CNPG resource or private recovery capture is deleted.
func (r *CloudNativePGRestore) Cleanup(ctx context.Context) error {
	if r == nil || ctx == nil {
		return errors.New("explicit owned verifier cleanup required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.creationAttempted {
		return nil
	}
	present, err := r.observePod(ctx)
	if err != nil || !present {
		return err
	}
	if r.deleteAttempted {
		return r.waitAbsent(ctx)
	}
	// Retire the entire independently owned emptyDir Pod. No SQL DROP is
	// needed here, and a source primary rollover after Apply cannot turn this
	// cleanup into source-CNPG mutation. Original archive/captures remain private.
	uid := types.UID(r.uid)
	body, err := json.Marshal(metav1.DeleteOptions{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "DeleteOptions"}, Preconditions: &metav1.Preconditions{UID: &uid}})
	if err != nil {
		return err
	}
	if r.deleteInput == nil {
		retained, err := writeBackupBytes(ctx, r.backup.source.fence.commands, "cnpg-restore-delete.json", body, 1<<20)
		if err != nil {
			return err
		}
		r.deleteInput = &retained
	}
	if err = r.requirePod(ctx); err != nil {
		return err
	}
	path := "/api/v1/namespaces/" + r.backup.source.fence.options.Identity.Namespace + "/pods/" + r.options.Pod
	r.deleteAttempted = true
	if _, _, err = r.input(ctx, r.backup.source.fence.kubectl, r.arguments("delete", "--raw", path, "--filename", "-"), r.deleteInput, 1<<20); err != nil {
		return err
	}
	return r.waitAbsent(ctx)
}

func (r *CloudNativePGRestore) waitAbsent(ctx context.Context) error {
	f := r.backup.source.fence
	if _, err := r.execute(ctx, f.kubectl, r.arguments("wait", "--for=delete", "pod/"+r.options.Pod, "--namespace", f.options.Identity.Namespace, "--timeout="+strconv.FormatUint(uint64(r.options.WaitSeconds), 10)+"s")); err != nil {
		return err
	}
	raw, err := r.execute(ctx, f.kubectl, r.arguments("get", "pod", r.options.Pod, "--namespace", f.options.Identity.Namespace, "--ignore-not-found", "--output", "go-template="+cnpgRestorePodTemplate))
	if err != nil {
		return err
	}
	if len(raw) != 0 {
		return errors.New("owned verifier Pod not absent after UID-bound deletion; no foreign cleanup authority")
	}
	if err = f.Fence(ctx, f.options.Identity, nil); err != nil {
		return err
	}
	return ctx.Err()
}
