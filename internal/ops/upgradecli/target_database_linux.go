//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
)

// These projections contain only immutable object identities, owner references,
// readiness and the exact chart data-volume claim. They do not read Secrets.
const targetDatabasePodTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/instance"}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/component"}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/part-of"}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/name"}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{.status.phase}}{{"\t"}}{{$ready := false}}{{range .status.conditions}}{{if and (eq .type "Ready") (eq .status "True")}}{{$ready = true}}{{end}}{{end}}{{$ready}}{{"\t"}}{{range .metadata.ownerReferences}}{{if .controller}}{{.apiVersion}}{{"\t"}}{{.kind}}{{"\t"}}{{.name}}{{"\t"}}{{.uid}}{{end}}{{end}}{{"\t"}}{{range .spec.volumes}}{{if eq .name "data"}}{{.persistentVolumeClaim.claimName}}{{end}}{{end}}{{"\t"}}{{range .spec.containers}}{{if eq .name "postgres"}}{{.name}}{{"\t"}}{{range .volumeMounts}}{{if eq .name "data"}}{{.mountPath}}{{end}}{{end}}{{end}}{{end}}`
const targetDatabasePodsTemplate = `{{range .items}}` + targetDatabasePodTemplate + `{{"\n"}}{{end}}`
const targetDatabaseControllerTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/instance"}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/component"}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/name"}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{.spec.replicas}}{{"\t"}}{{.metadata.generation}}{{"\t"}}{{.status.observedGeneration}}{{"\t"}}{{.status.currentRevision}}{{"\t"}}{{.status.updateRevision}}{{"\t"}}{{.status.readyReplicas}}{{"\t"}}{{index .spec.selector.matchLabels "app.kubernetes.io/name"}}{{"\t"}}{{index .spec.selector.matchLabels "app.kubernetes.io/instance"}}{{"\t"}}{{index .spec.selector.matchLabels "app.kubernetes.io/component"}}`
const targetDatabaseClaimTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{.status.phase}}{{"\t"}}{{.spec.volumeName}}`
const targetDatabaseVolumeTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{.status.phase}}{{"\t"}}{{.spec.claimRef.uid}}{{"\t"}}{{.spec.claimRef.name}}{{"\t"}}{{.spec.claimRef.namespace}}`

type databaseStorageAnchor struct {
	controllerName, controllerUID, applicationName string
	claimName, claimUID, volumeName, volumeUID     string
}

type observedDatabasePod struct{ uid, name, applicationName, controllerName, controllerUID, claimName string }

// TargetDatabaseObservation permits a replacement Pod only under the initially
// captured single-replica StatefulSet and its same Bound PVC/PV claim. It never
// relaxes SourceDatabaseBackup's initial Pod UID, grants DROP authorization or
// claims CloudNativePG/failover/major-upgrade qualification. Repeated reads are
// point-in-time fences, not a Kubernetes or PostgreSQL lock.
type TargetDatabaseObservation struct {
	source    *SourceDatabaseBackup
	mu        sync.Mutex
	anchor    *databaseStorageAnchor
	capturing bool
}

func NewTargetDatabaseObservation(source *SourceDatabaseBackup) (*TargetDatabaseObservation, error) {
	if source == nil || source.fence == nil || source.fence.commands == nil || source.options.DatabasePodUID == "" || source.fence.options.DatabasePod == "" {
		return nil, errors.New("initial bundled source database ownership required")
	}
	return &TargetDatabaseObservation{source: source}, nil
}

// Capture must run before mutation while the original source Pod UID remains
// present. A second capture is rejected; replacement storage cannot be admitted
// as an initial owner. Failed capture leaves the observation unbound.
func (o *TargetDatabaseObservation) Capture(ctx context.Context) error {
	if o == nil || ctx == nil {
		return errors.New("explicit database capture context required")
	}
	o.mu.Lock()
	if o.anchor != nil || o.capturing {
		o.mu.Unlock()
		return errors.New("initial database storage already captured or being captured")
	}
	o.capturing = true
	o.mu.Unlock()
	defer func() { o.mu.Lock(); o.capturing = false; o.mu.Unlock() }()
	if _, err := o.source.observe(ctx); err != nil {
		return err
	}
	pod, err := o.readPod(ctx, o.source.fence.options.DatabasePod)
	if err != nil {
		return err
	}
	if pod.uid != o.source.options.DatabasePodUID {
		return errors.New("initial database Pod differs")
	}
	anchor, err := o.storage(ctx, pod)
	if err != nil {
		return err
	}
	last, err := o.readPod(ctx, pod.name)
	if err != nil {
		return err
	}
	if last != pod {
		return errors.New("initial database Pod changed during storage capture")
	}
	if _, err := o.source.observe(ctx); err != nil {
		return err
	}
	final, err := o.storage(ctx, last)
	if err != nil {
		return err
	}
	if final != anchor {
		return errors.New("initial database storage changed during capture")
	}
	if err := o.source.fence.Fence(ctx, o.source.fence.options.Identity, nil); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	o.anchor = &anchor
	o.mu.Unlock()
	return nil
}

// Observe requires a clean schema in the authenticated source-to-target range.
// The full target adapter separately requires Version == TargetSchema.
func (o *TargetDatabaseObservation) Observe(ctx context.Context) (DatabaseSchemaState, error) {
	var empty DatabaseSchemaState
	if o == nil || ctx == nil {
		return empty, errors.New("explicit target database observation required")
	}
	o.mu.Lock()
	if o.anchor == nil {
		o.mu.Unlock()
		return empty, errors.New("initial database storage capture missing")
	}
	bound := *o.anchor
	o.mu.Unlock()
	f := o.source.fence
	if err := f.Fence(ctx, f.options.Identity, nil); err != nil {
		return empty, err
	}
	first, err := o.currentPod(ctx, bound)
	if err != nil {
		return empty, err
	}
	anchor, err := o.storage(ctx, first)
	if err != nil {
		return empty, err
	}
	if anchor != bound {
		return empty, errors.New("target database controller or storage owner differs")
	}
	args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", f.options.Identity.Context, "exec", "--namespace", f.options.Identity.Namespace, first.name, "--container=postgres", "--", "sh", "-ec", databaseStateScript}
	raw, err := f.execute(ctx, f.kubectl, args)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	state, err := parseDatabaseState(raw, o.source.options.Database, o.source.options.MinimumSchema, o.source.options.TargetSchema)
	if err != nil {
		return empty, err
	}
	if state.Major != o.source.options.ExpectedMajor {
		return empty, errors.New("target database major differs from the bound source")
	}
	last, err := o.currentPod(ctx, bound)
	if err != nil {
		return empty, err
	}
	if first != last {
		return empty, errors.New("target database Pod changed during schema observation")
	}
	final, err := o.storage(ctx, last)
	if err != nil {
		return empty, err
	}
	if final != anchor {
		return empty, errors.New("target storage changed during schema observation")
	}
	if err := f.Fence(ctx, f.options.Identity, nil); err != nil {
		return empty, err
	}
	return state, ctx.Err()
}

func (o *TargetDatabaseObservation) read(ctx context.Context, kind, name, template string, namespaced bool) ([]byte, error) {
	f := o.source.fence
	args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", f.options.Identity.Context, "get", kind, name}
	if namespaced {
		args = append(args, "--namespace", f.options.Identity.Namespace)
	}
	args = append(args, "--output", "go-template="+template)
	raw, err := f.execute(ctx, f.kubectl, args)
	if err != nil {
		return nil, err
	}
	return raw, ctx.Err()
}

func (o *TargetDatabaseObservation) readPod(ctx context.Context, name string) (observedDatabasePod, error) {
	raw, err := o.read(ctx, "pod", name, targetDatabasePodTemplate, true)
	if err != nil {
		return observedDatabasePod{}, err
	}
	return o.parsePod(raw)
}

func (o *TargetDatabaseObservation) currentPod(ctx context.Context, bound databaseStorageAnchor) (observedDatabasePod, error) {
	f := o.source.fence
	selector := "app.kubernetes.io/name=" + bound.applicationName + ",app.kubernetes.io/instance=" + f.options.Identity.Release + ",app.kubernetes.io/component=postgres"
	args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", f.options.Identity.Context, "get", "pods", "--namespace", f.options.Identity.Namespace, "--selector", selector, "--output", "go-template=" + targetDatabasePodsTemplate}
	raw, err := f.execute(ctx, f.kubectl, args)
	if err != nil {
		return observedDatabasePod{}, err
	}
	if err := ctx.Err(); err != nil {
		return observedDatabasePod{}, err
	}
	if len(raw) == 0 || len(raw) > 4096 || strings.Count(string(raw), "\n") != 1 || raw[len(raw)-1] != '\n' {
		return observedDatabasePod{}, errors.New("one bounded target database Pod required")
	}
	pod, err := o.parsePod(raw[:len(raw)-1])
	if err != nil {
		return observedDatabasePod{}, err
	}
	if pod.name != o.source.fence.options.DatabasePod || pod.applicationName != bound.applicationName || pod.controllerName != bound.controllerName || pod.controllerUID != bound.controllerUID || pod.claimName != bound.claimName {
		return observedDatabasePod{}, errors.New("target database Pod owner differs")
	}
	return pod, nil
}

func databaseProjection(raw []byte, count int) ([]string, error) {
	if len(raw) == 0 || len(raw) > 4096 || strings.ContainsAny(string(raw), "\r\n") {
		return nil, errors.New("bounded database identity projection required")
	}
	fields := strings.Split(string(raw), "\t")
	if len(fields) != count {
		return nil, errors.New("database identity projection shape differs")
	}
	for _, field := range fields {
		if field == "" || strings.ContainsAny(field, " \r\n") {
			return nil, errors.New("database identity field differs")
		}
	}
	return fields, nil
}

func (o *TargetDatabaseObservation) parsePod(raw []byte) (observedDatabasePod, error) {
	var empty observedDatabasePod
	p, err := databaseProjection(raw, 17)
	if err != nil {
		return empty, err
	}
	id := o.source.fence.options.Identity
	if !kubeName.MatchString(p[1]) || p[2] != id.Namespace || p[3] != id.Release || p[4] != "postgres" || p[5] != "astronomer" || !kubeName.MatchString(p[6]) || p[7] != "live" || p[8] != "Running" || p[9] != "true" || p[10] != "apps/v1" || p[11] != "StatefulSet" || !kubeName.MatchString(p[12]) || p[1] != p[12]+"-0" || p[14] != "data-"+p[1] || p[15] != "postgres" || p[16] != "/var/lib/postgresql/data" {
		return empty, errors.New("bound chart database Pod shape or lifecycle differs")
	}
	return observedDatabasePod{uid: p[0], name: p[1], applicationName: p[6], controllerName: p[12], controllerUID: p[13], claimName: p[14]}, nil
}

func (o *TargetDatabaseObservation) storage(ctx context.Context, pod observedDatabasePod) (databaseStorageAnchor, error) {
	var empty databaseStorageAnchor
	id := o.source.fence.options.Identity
	raw, err := o.read(ctx, "statefulset", pod.controllerName, targetDatabaseControllerTemplate, true)
	if err != nil {
		return empty, err
	}
	c, err := databaseProjection(raw, 16)
	if err != nil {
		return empty, err
	}
	generation, e1 := strconv.ParseUint(c[8], 10, 64)
	observed, e2 := strconv.ParseUint(c[9], 10, 64)
	if c[0] != pod.controllerUID || c[1] != pod.controllerName || c[2] != id.Namespace || c[3] != id.Release || c[4] != "postgres" || c[5] != pod.applicationName || c[6] != "live" || c[7] != "1" || e1 != nil || e2 != nil || generation == 0 || observed < generation || strconv.FormatUint(generation, 10) != c[8] || strconv.FormatUint(observed, 10) != c[9] || c[10] != c[11] || c[12] != "1" || c[13] != pod.applicationName || c[14] != id.Release || c[15] != "postgres" {
		return empty, errors.New("database StatefulSet ownership or readiness differs")
	}
	raw, err = o.read(ctx, "persistentvolumeclaim", pod.claimName, targetDatabaseClaimTemplate, true)
	if err != nil {
		return empty, err
	}
	claim, err := databaseProjection(raw, 6)
	if err != nil {
		return empty, err
	}
	if claim[1] != pod.claimName || claim[2] != id.Namespace || claim[3] != "live" || claim[4] != "Bound" || !kubeName.MatchString(claim[5]) {
		return empty, errors.New("bound database data claim differs")
	}
	raw, err = o.read(ctx, "persistentvolume", claim[5], targetDatabaseVolumeTemplate, false)
	if err != nil {
		return empty, err
	}
	volume, err := databaseProjection(raw, 7)
	if err != nil {
		return empty, err
	}
	if volume[1] != claim[5] || volume[2] != "live" || volume[3] != "Bound" || volume[4] != claim[0] || volume[5] != claim[1] || volume[6] != claim[2] {
		return empty, errors.New("database volume claim reference differs")
	}
	return databaseStorageAnchor{controllerName: pod.controllerName, controllerUID: pod.controllerUID, applicationName: pod.applicationName, claimName: pod.claimName, claimUID: claim[0], volumeName: volume[1], volumeUID: volume[0]}, nil
}
