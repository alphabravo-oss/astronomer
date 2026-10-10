//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

const workloadIdentityTemplate = `{{range .items}}{{.metadata.name}}{{"\t"}}{{.metadata.uid}}{{"\t"}}{{.metadata.namespace}}{{"\n"}}{{end}}`

// TargetWorkloads joins the remaining legacy post-upgrade checks: retained
// rendered manifest pins, every selected deployment's rollout, actual bounded
// /readyz and exact target schema/readiness/revision observations. Point-in-time
// UID observations are not an atomic cluster lock or proof of every replica.
type TargetWorkloads struct {
	observation *HelmStateObservation
	readyz      *ReadyzObservation
	database    *TargetDatabaseObservation
}

func NewTargetWorkloads(observation *HelmStateObservation, readyz *ReadyzObservation) (*TargetWorkloads, error) {
	if observation == nil || observation.release == nil || observation.release.fence == nil || observation.release.plan.Validate() != nil || observation.readiness == nil || observation.readiness.fence != observation.release.fence || observation.source == nil || observation.source.fence != observation.release.fence || readyz == nil || readyz.release != observation.release {
		return nil, errors.New("one release with actual bundled schema and health composition required")
	}
	return &TargetWorkloads{observation: observation, readyz: readyz}, nil
}

// NewTargetWorkloadsWithDatabase adds only the initially co-owned database
// storage observation. Capture occurs in the Driver Backup stage, before any
// mutation. The original constructor continues to use the initial Pod fence.
func NewTargetWorkloadsWithDatabase(observation *HelmStateObservation, readyz *ReadyzObservation, database *TargetDatabaseObservation) (*TargetWorkloads, error) {
	target, err := NewTargetWorkloads(observation, readyz)
	if err != nil {
		return nil, err
	}
	if database == nil || database.source != observation.source {
		return nil, errors.New("initial target database observer owner differs")
	}
	target.database = database
	return target, nil
}

func (o *TargetWorkloads) Check(ctx context.Context) error {
	if o == nil || ctx == nil || o.observation == nil || o.observation.release == nil || o.readyz == nil {
		return errors.New("explicit complete target observation context required")
	}
	if err := o.observation.confirmTarget(ctx, o.database); err != nil {
		return err
	}
	r := o.observation.release
	manifest, _, err := releasecontract.Load(r.manifestFile, r.plan.Target)
	if err != nil {
		return errors.New("initial target manifest contract changed")
	}
	raw, _, err := r.execute(ctx, r.tool, []string{"--kubeconfig", r.fence.options.Kubeconfig, "--kube-context", r.plan.Identity.Context, "get", "manifest", r.plan.Identity.Release, "--namespace", r.plan.Identity.Namespace})
	if err != nil {
		return err
	}
	if err := targetManifestPins(raw, manifest); err != nil {
		return err
	}
	first, err := o.workloads(ctx)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(first))
	for name := range first {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		deployment := first[name]
		if err := r.fence.Fence(ctx, r.plan.Identity, r.plan.Replace); err != nil {
			return err
		}
		args := []string{"--kubeconfig", r.fence.options.Kubeconfig, "--context", r.plan.Identity.Context, "rollout", "status", "--namespace", r.plan.Identity.Namespace, "deployment/" + deployment.name, "--timeout", r.plan.Timeout.String()}
		if _, err := r.fence.execute(ctx, r.fence.kubectl, args); err != nil {
			return err
		}
	}
	if err := o.readyz.Check(ctx); err != nil {
		return err
	}
	last, err := o.workloads(ctx)
	if err != nil {
		return err
	}
	if len(first) != len(last) {
		return errors.New("selected target workload set changed")
	}
	for name, previous := range first {
		if last[name] != previous {
			return errors.New("selected target workload identity changed")
		}
	}
	if err := r.manifestBinding(); err != nil {
		return err
	}
	return o.observation.confirmTarget(ctx, o.database)
}

type targetWorkload struct {
	name string
	uid  string
}

func (o *TargetWorkloads) workloads(ctx context.Context) (map[string]targetWorkload, error) {
	r := o.observation.release
	id := r.plan.Identity
	args := []string{"--kubeconfig", r.fence.options.Kubeconfig, "--context", id.Context, "get", "deployments", "--namespace", id.Namespace, "--selector", "app.kubernetes.io/instance=" + id.Release + ",app.kubernetes.io/part-of=astronomer", "--output", "go-template=" + workloadIdentityTemplate}
	raw, err := r.fence.execute(ctx, r.fence.kubectl, args)
	if err != nil {
		return nil, err
	}
	return targetWorkloadIdentities(raw, id.Namespace)
}

func targetWorkloadIdentities(raw []byte, namespace string) (map[string]targetWorkload, error) {
	rows, err := readinessRows(raw)
	if err != nil || len(rows) == 0 {
		return nil, errors.New("bounded nonempty target deployment inventory required")
	}
	result := make(map[string]targetWorkload, len(rows))
	for _, row := range rows {
		fields := strings.Split(row, "\t")
		if len(fields) != 3 || !kubeName.MatchString(fields[0]) || fields[1] == "" || strings.ContainsAny(fields[1], "\t\r\n ") || fields[2] != namespace {
			return nil, errors.New("target deployment identity differs")
		}
		if _, exists := result[fields[0]]; exists {
			return nil, errors.New("target deployment identity duplicated")
		}
		result[fields[0]] = targetWorkload{name: fields[0], uid: fields[1]}
	}
	return result, nil
}

func targetManifestPins(raw []byte, manifest releasecontract.Manifest) error {
	if len(raw) == 0 || len(raw) > 16<<20 {
		return errors.New("target rendered manifest exceeds finite capture")
	}
	wanted := map[string]string{}
	for _, image := range manifest.Astronomer.Images {
		if _, duplicate := wanted[image.Name]; duplicate {
			return errors.New("target first-party image role duplicated")
		}
		wanted[image.Name] = image.Reference
	}
	text := string(raw)
	for _, role := range []string{"server", "worker", "migrate", "frontend", "shell"} {
		if wanted[role] == "" || !strings.Contains(text, wanted[role]) {
			return errors.New("target rendered manifest lacks exact first-party image pin")
		}
	}
	if wanted["agent"] == "" || !strings.Contains(text, "AGENT_IMAGE_REPOSITORY: \""+wanted["agent"]+"\"") || !strings.Contains(text, "AGENT_IMAGE_TAG: \""+manifest.Release.Version+"\"") {
		return errors.New("target rendered manifest lacks managed agent image configuration")
	}
	// This deliberately retains legacy string-presence semantics. It does not
	// claim an exhaustive Kubernetes object/schema or actual running-image proof.
	return nil
}
