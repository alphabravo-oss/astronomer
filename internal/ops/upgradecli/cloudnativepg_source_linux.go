//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// These projections contain metadata only. They never read Secret.data or use
// a Cluster's observed image as new signing authority. The independently signed
// target remains responsible for runtime compatibility.
const cnpgClusterTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/instance"}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{.spec.instances}}{{"\t"}}{{.status.readyInstances}}{{"\t"}}{{.status.currentPrimary}}{{"\t"}}{{.status.targetPrimary}}{{"\t"}}{{.spec.imageName}}`
const cnpgPodsTemplate = `{{range .items}}{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{index .metadata.labels "cnpg.io/cluster"}}{{"\t"}}{{range .metadata.ownerReferences}}{{if .controller}}{{.uid}}{{end}}{{end}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{.status.phase}}{{"\t"}}{{$ready := false}}{{range .status.conditions}}{{if and (eq .type "Ready") (eq .status "True")}}{{$ready = true}}{{end}}{{end}}{{if $ready}}ready{{else}}notready{{end}}{{"\t"}}{{range .spec.volumes}}{{if .persistentVolumeClaim}}{{.persistentVolumeClaim.claimName}}{{","}}{{end}}{{end}}{{"\n"}}{{end}}`
const cnpgPVCTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{range .metadata.ownerReferences}}{{if .controller}}{{.uid}}{{end}}{{end}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{.status.phase}}{{"\t"}}{{.spec.volumeName}}`
const cnpgPVTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{.status.phase}}{{"\t"}}{{.spec.claimRef.uid}}{{"\t"}}{{.spec.claimRef.namespace}}{{"\t"}}{{.spec.claimRef.name}}`
const cnpgServiceTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{range .metadata.ownerReferences}}{{if .controller}}{{.uid}}{{end}}{{end}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{index .spec.selector "cnpg.io/cluster"}}{{"\t"}}{{index .spec.selector "cnpg.io/instanceRole"}}`
const cnpgEndpointsTemplate = `{{range .subsets}}{{range .addresses}}{{.targetRef.uid}}{{"\t"}}{{.targetRef.name}}{{"\t"}}{{.targetRef.namespace}}{{"\t"}}{{.targetRef.kind}}{{"\n"}}{{end}}{{range .notReadyAddresses}}notready{{"\n"}}{{end}}{{end}}`
const cnpgSecretTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{range .metadata.ownerReferences}}{{if .controller}}{{.uid}}{{end}}{{end}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{.metadata.resourceVersion}}`
const cnpgStateScript = `psql -X -v ON_ERROR_STOP=1 --username=postgres --dbname="$1" -AtF "|" -c "SELECT current_database(), current_setting('server_version_num')::bigint / 10000, 1, COALESCE(max(version_id) FILTER (WHERE is_applied),0), COALESCE(bool_or(NOT is_applied),false) FROM goose_db_version"`
const cnpgDumpScript = `pg_dump --format=custom --username=postgres --dbname="$1"`

type CloudNativePGOptions struct {
	Cluster, Database, ApplicationSecret       string
	ExpectedMajor, MinimumSchema, TargetSchema uint32
	ArtifactPolicy                             ArtifactPolicy
}

type cnpgPodBinding struct {
	UID    string
	Claims []string
}
type cnpgStorageBinding struct{ PVCUID, Volume, PVUID string }
type cnpgSourceSnapshot struct {
	ClusterUID, Image, Primary, PrimaryUID, ServiceUID, SecretUID, SecretVersion string
	Instances                                                                    uint32
	Pods                                                                         map[string]cnpgPodBinding
	Storage                                                                      map[string]cnpgStorageBinding
}

// CloudNativePGSource owns read-only database observation and backup on one
// existing release Cluster. An ordinary primary handoff among the initially
// captured member Pod UIDs is admitted only under unchanged Cluster/PVC/PV,
// Service and application-Secret bindings. A recreated source Pod is refused;
// target-after-Apply ownership needs its separate adapter. No DROP, RESTORE,
// Cluster patch, Barman change or Flux reconciliation is performed here.
type CloudNativePGSource struct {
	mu      sync.Mutex
	fence   *OwnershipFence
	options CloudNativePGOptions
	initial cnpgSourceSnapshot
	read    func(context.Context, Tool, []string) ([]byte, error)
	capture func(context.Context, Tool, []string, ArtifactPolicy) (*process.ArtifactCapture, process.Result, error)
}

type CloudNativePGBackup struct {
	source              *CloudNativePGSource
	artifact            process.ArtifactCapture
	schema              DatabaseSchemaState
	primary, primaryUID string
}

func NewCloudNativePGSource(ctx context.Context, fence *OwnershipFence, options CloudNativePGOptions) (*CloudNativePGSource, error) {
	if ctx == nil || fence == nil || fence.commands == nil || !kubeName.MatchString(options.Cluster) || options.ApplicationSecret != options.Cluster+"-app" || !databaseName.MatchString(options.Database) || options.ExpectedMajor == 0 || options.MinimumSchema == 0 || options.TargetSchema < options.MinimumSchema || options.ArtifactPolicy.MaximumBytes <= 0 {
		return nil, errors.New("explicit observed CNPG Cluster, generated application Secret, database and capture bounds required")
	}
	source := &CloudNativePGSource{fence: fence, options: options, read: fence.execute, capture: fence.commands.CaptureArtifact}
	initial, err := source.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	source.initial = initial
	if _, err = source.state(ctx, initial); err != nil {
		return nil, err
	}
	return source, ctx.Err()
}

func cnpgFields(raw []byte, count int) ([]string, error) {
	if len(raw) == 0 || len(raw) > 4096 || strings.ContainsAny(string(raw), "\x00\r\n") {
		return nil, errors.New("CNPG metadata projection shape differs")
	}
	fields := strings.Split(string(raw), "\t")
	if len(fields) != count {
		return nil, errors.New("CNPG metadata field count differs")
	}
	for _, field := range fields {
		if field == "" || field == "<no value>" {
			return nil, errors.New("CNPG metadata field missing")
		}
	}
	return fields, nil
}

func (s *CloudNativePGSource) get(ctx context.Context, resource, name, template string) ([]byte, error) {
	id := s.fence.options.Identity
	return s.read(ctx, s.fence.kubectl, []string{"--kubeconfig", s.fence.options.Kubeconfig, "--context", id.Context, "get", resource, name, "--namespace", id.Namespace, "--output", "go-template=" + template})
}

func (s *CloudNativePGSource) snapshot(ctx context.Context) (cnpgSourceSnapshot, error) {
	var empty cnpgSourceSnapshot
	if s == nil || ctx == nil {
		return empty, errors.New("explicit CNPG observation context required")
	}
	id := s.fence.options.Identity
	if err := s.fence.Fence(ctx, id, nil); err != nil {
		return empty, err
	}
	raw, err := s.get(ctx, "clusters.postgresql.cnpg.io", s.options.Cluster, cnpgClusterTemplate)
	if err != nil {
		return empty, err
	}
	f, err := cnpgFields(raw, 10)
	if err != nil {
		return empty, err
	}
	instances, err := strconv.ParseUint(f[5], 10, 32)
	if err != nil || instances == 0 || instances > readinessRowsLimit || strconv.FormatUint(instances, 10) != f[5] || f[6] != f[5] || f[1] != s.options.Cluster || f[2] != id.Namespace || f[3] != id.Release || f[4] != "live" || f[7] != f[8] || !kubeName.MatchString(f[7]) {
		return empty, errors.New("CNPG Cluster owner, readiness or primary handoff incomplete")
	}
	clusterProjection := append([]byte(nil), raw...)
	out := cnpgSourceSnapshot{ClusterUID: f[0], Image: f[9], Primary: f[7], Instances: uint32(instances), Pods: map[string]cnpgPodBinding{}, Storage: map[string]cnpgStorageBinding{}}
	raw, err = s.read(ctx, s.fence.kubectl, []string{"--kubeconfig", s.fence.options.Kubeconfig, "--context", id.Context, "get", "pods", "--namespace", id.Namespace, "--selector", "cnpg.io/cluster=" + s.options.Cluster, "--output", "go-template=" + cnpgPodsTemplate})
	if err != nil {
		return empty, err
	}
	rows, err := readinessRows(raw)
	if err != nil {
		return empty, err
	}
	for _, row := range rows {
		p, err := cnpgFields([]byte(row), 9)
		if err != nil {
			return empty, err
		}
		if !kubeName.MatchString(p[1]) || p[2] != id.Namespace || p[3] != s.options.Cluster || p[4] != out.ClusterUID || p[5] != "live" || p[6] != "Running" || p[7] != "ready" {
			return empty, errors.New("CNPG member Pod ownership or readiness differs")
		}
		if _, ok := out.Pods[p[1]]; ok {
			return empty, errors.New("duplicate CNPG member Pod")
		}
		if !strings.HasSuffix(p[8], ",") {
			return empty, errors.New("CNPG member storage projection differs")
		}
		claims := strings.Split(strings.TrimSuffix(p[8], ","), ",")
		slices.Sort(claims)
		for i, name := range claims {
			if !kubeName.MatchString(name) || (i > 0 && name == claims[i-1]) {
				return empty, errors.New("CNPG member persistent volume name differs")
			}
			if _, ok := out.Storage[name]; ok {
				return empty, errors.New("CNPG members share persistent volume claim")
			}
			pvcRaw, err := s.get(ctx, "persistentvolumeclaims", name, cnpgPVCTemplate)
			if err != nil {
				return empty, err
			}
			v, err := cnpgFields(pvcRaw, 7)
			if err != nil {
				return empty, err
			}
			if v[1] != name || v[2] != id.Namespace || v[3] != out.ClusterUID || v[4] != "live" || v[5] != "Bound" || !kubeName.MatchString(v[6]) {
				return empty, errors.New("CNPG persistent claim binding differs")
			}
			pvRaw, err := s.get(ctx, "persistentvolumes", v[6], cnpgPVTemplate)
			if err != nil {
				return empty, err
			}
			pv, err := cnpgFields(pvRaw, 7)
			if err != nil {
				return empty, err
			}
			if !equalFields([]byte(strings.Join(pv[1:], "\t")), v[6], "live", "Bound", v[0], id.Namespace, name) {
				return empty, errors.New("CNPG persistent volume claimRef differs")
			}
			out.Storage[name] = cnpgStorageBinding{PVCUID: v[0], Volume: v[6], PVUID: pv[0]}
		}
		out.Pods[p[1]] = cnpgPodBinding{UID: p[0], Claims: claims}
	}
	if len(out.Pods) != int(out.Instances) {
		return empty, errors.New("CNPG ready instance inventory differs")
	}
	primary, ok := out.Pods[out.Primary]
	if !ok {
		return empty, errors.New("CNPG primary not a captured ready member")
	}
	out.PrimaryUID = primary.UID
	service := s.options.Cluster + "-rw"
	raw, err = s.get(ctx, "services", service, cnpgServiceTemplate)
	if err != nil {
		return empty, err
	}
	v, err := cnpgFields(raw, 7)
	if err != nil {
		return empty, err
	}
	if !equalFields([]byte(strings.Join(v[1:], "\t")), service, id.Namespace, out.ClusterUID, "live", s.options.Cluster, "primary") {
		return empty, errors.New("CNPG read-write Service owner or selector differs")
	}
	out.ServiceUID = v[0]
	raw, err = s.get(ctx, "endpoints", service, cnpgEndpointsTemplate)
	if err != nil {
		return empty, err
	}
	endpointRows, err := readinessRows(raw)
	if err != nil || len(endpointRows) != 1 || !equalFields([]byte(endpointRows[0]), out.PrimaryUID, out.Primary, id.Namespace, "Pod") {
		return empty, errors.New("CNPG read-write endpoint not the ready current primary")
	}
	raw, err = s.get(ctx, "secrets", s.options.ApplicationSecret, cnpgSecretTemplate)
	if err != nil {
		return empty, err
	}
	v, err = cnpgFields(raw, 6)
	if err != nil {
		return empty, err
	}
	if !equalFields([]byte(strings.Join(v[1:5], "\t")), s.options.ApplicationSecret, id.Namespace, out.ClusterUID, "live") {
		return empty, errors.New("CNPG application Secret owner differs")
	}
	out.SecretUID = v[0]
	out.SecretVersion = v[5]
	finalCluster, err := s.get(ctx, "clusters.postgresql.cnpg.io", s.options.Cluster, cnpgClusterTemplate)
	if err != nil {
		return empty, err
	}
	if string(finalCluster) != string(clusterProjection) {
		return empty, errors.New("CNPG primary or Cluster changed while observing dependent resources")
	}
	if err = s.fence.Fence(ctx, id, nil); err != nil {
		return empty, err
	}
	return out, ctx.Err()
}

func sameCNPGSource(a, b cnpgSourceSnapshot) bool {
	if a.ClusterUID != b.ClusterUID || a.Image != b.Image || a.Instances != b.Instances || a.ServiceUID != b.ServiceUID || a.SecretUID != b.SecretUID || a.SecretVersion != b.SecretVersion || len(a.Pods) != len(b.Pods) || len(a.Storage) != len(b.Storage) {
		return false
	}
	for name, p := range a.Pods {
		q, ok := b.Pods[name]
		if !ok || p.UID != q.UID || !slices.Equal(p.Claims, q.Claims) {
			return false
		}
	}
	for name, p := range a.Storage {
		if q, ok := b.Storage[name]; !ok || p != q {
			return false
		}
	}
	return true
}
func (s *CloudNativePGSource) observe(ctx context.Context) (cnpgSourceSnapshot, error) {
	now, err := s.snapshot(ctx)
	if err != nil {
		return now, err
	}
	if !sameCNPGSource(s.initial, now) {
		return cnpgSourceSnapshot{}, errors.New("captured CNPG Cluster, source member or persistent identity changed")
	}
	return now, nil
}
func (s *CloudNativePGSource) arguments(pod, script string) []string {
	id := s.fence.options.Identity
	return []string{"--kubeconfig", s.fence.options.Kubeconfig, "--context", id.Context, "exec", "--namespace", id.Namespace, pod, "--", "sh", "-ec", script, "sh", s.options.Database}
}
func (s *CloudNativePGSource) state(ctx context.Context, view cnpgSourceSnapshot) (DatabaseSchemaState, error) {
	raw, err := s.read(ctx, s.fence.kubectl, s.arguments(view.Primary, cnpgStateScript))
	if err != nil {
		return DatabaseSchemaState{}, err
	}
	state, err := parseDatabaseState(raw, s.options.Database, s.options.MinimumSchema, s.options.TargetSchema)
	if err != nil {
		return state, err
	}
	if state.Major != s.options.ExpectedMajor {
		return DatabaseSchemaState{}, errors.New("CNPG source major differs from bound compatibility")
	}
	return state, ctx.Err()
}
func (s *CloudNativePGSource) Dump(ctx context.Context) (*CloudNativePGBackup, process.Result, error) {
	var result process.Result
	if s == nil || ctx == nil {
		return nil, result, errors.New("explicit bound CNPG dump required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	view, err := s.observe(ctx)
	if err != nil {
		return nil, result, err
	}
	state, err := s.state(ctx, view)
	if err != nil {
		return nil, result, err
	}
	artifact, result, err := s.capture(ctx, s.fence.kubectl, s.arguments(view.Primary, cnpgDumpScript), s.options.ArtifactPolicy)
	if err != nil {
		return nil, result, err
	}
	if artifact == nil || !artifact.Acknowledged {
		return nil, result, errors.New("CNPG dump retained bytes not acknowledged")
	}
	after, err := s.observe(ctx)
	if err != nil {
		return nil, result, err
	}
	if after.Primary != view.Primary || after.PrimaryUID != view.PrimaryUID {
		return nil, result, errors.New("CNPG primary changed while dump was in flight; private capture retained")
	}
	current, err := s.state(ctx, after)
	if err != nil {
		return nil, result, err
	}
	if current != state {
		return nil, result, errors.New("CNPG source schema changed while dumping")
	}
	return &CloudNativePGBackup{source: s, artifact: *artifact, schema: state, primary: view.Primary, primaryUID: view.PrimaryUID}, result, ctx.Err()
}
