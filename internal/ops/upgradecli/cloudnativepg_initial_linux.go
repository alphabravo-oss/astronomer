//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

const initialCNPGClustersTemplate = `{{range .items}}{{.metadata.name}}{{"\t"}}{{.metadata.uid}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/instance"}}{{"\t"}}{{index .metadata.labels "app.kubernetes.io/component"}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\n"}}{{end}}`

func (o *InitialObserver) observeCNPG(ctx context.Context, id upgrade.Identity, metadata initialHelmMetadata, valuesHash [32]byte) (InitialInstallation, error) {
	var empty InitialInstallation
	if o.selection.ReplaceDatabase || o.selection.ExternalBackupConfirmed || o.selection.ExternalSchemaVersion != "" || o.selection.DecryptProofWebhookID != "" {
		return empty, errors.New("CNPG cannot admit external assertions or bundled-only replacement/decrypt controls")
	}
	args := []string{"--kubeconfig", o.kubeconfig, "--kube-context", id.Context, "get", "values", id.Release, "--namespace", id.Namespace, "--all", "--output", "json"}
	values, err := o.read(ctx, o.helm, args)
	if err != nil {
		return empty, err
	}
	if sha256.Sum256(values) != valuesHash {
		return empty, errors.New("installed CNPG Helm values changed")
	}
	database, configuredCluster, err := initialCNPGDatabaseValues(values)
	if err != nil {
		return empty, err
	}
	raw, err := o.kube(ctx, "get", "clusters.postgresql.cnpg.io", "--namespace", id.Namespace, "--selector", "app.kubernetes.io/instance="+id.Release+",app.kubernetes.io/component=postgres", "--output", "go-template="+initialCNPGClustersTemplate)
	if err != nil {
		return empty, err
	}
	rows, err := readinessRows(raw)
	if err != nil || len(rows) != 1 {
		return empty, errors.New("exactly one release-owned CNPG Cluster required")
	}
	fields := strings.Split(rows[0], "\t")
	if len(fields) != 6 || !kubeName.MatchString(fields[0]) || fields[1] == "" || fields[2] != id.Namespace || fields[3] != id.Release || fields[4] != "postgres" || fields[5] != "live" || (configuredCluster != "" && configuredCluster != fields[0]) {
		return empty, errors.New("initial installed CNPG Cluster differs")
	}
	fence, err := NewOwnershipFence(o.commands, o.kubectl, FenceOptions{Identity: id, Kubeconfig: o.kubeconfig, Revision: metadata.Revision})
	if err != nil {
		return empty, err
	}
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) { return o.read(ctx, tool, args) }
	options := CloudNativePGOptions{Cluster: fields[0], Database: database, ApplicationSecret: fields[0] + "-app", MinimumSchema: 1, TargetSchema: ^uint32(0), ArtifactPolicy: ArtifactPolicy{MaximumBytes: 1}}
	source := &CloudNativePGSource{fence: fence, options: options, read: fence.execute, capture: o.commands.CaptureArtifact}
	view, err := source.snapshot(ctx)
	if err != nil {
		return empty, err
	}
	if view.ClusterUID != fields[1] {
		return empty, errors.New("initial CNPG Cluster UID changed")
	}
	raw, err = source.read(ctx, fence.kubectl, source.arguments(view.Primary, cnpgStateScript))
	if err != nil {
		return empty, err
	}
	state, err := parseDatabaseState(raw, database, 1, ^uint32(0))
	if err != nil {
		return empty, err
	}
	source.options.ExpectedMajor = state.Major
	source.initial = view
	final, err := source.observe(ctx)
	if err != nil {
		return empty, err
	}
	if final.Primary != view.Primary || final.PrimaryUID != view.PrimaryUID {
		return empty, errors.New("CNPG primary changed during initial schema observation")
	}
	current, err := source.state(ctx, final)
	if err != nil || current != state {
		return empty, errors.New("CNPG initial schema changed")
	}
	if err := recheckHelmDatabaseBackend(ctx, o.read, o.helm, o.kubeconfig, id, "cloudNativePG", valuesHash); err != nil {
		return empty, err
	}
	if err := fence.Fence(ctx, id, nil); err != nil {
		return empty, err
	}
	return InitialInstallation{Identity: id, Revision: metadata.Revision, CurrentVersion: "v" + metadata.Version, DatabasePod: view.Primary, DatabasePodUID: view.PrimaryUID, Database: database, DatabaseState: state, CloudNativePG: true, DatabaseValuesSHA256: valuesHash, cnpg: source}, ctx.Err()
}

func initialCNPGDatabaseValues(raw []byte) (string, string, error) {
	if mode, err := parseHelmDatabaseBackend(raw); err != nil || mode != "cloudNativePG" {
		return "", "", errors.New("installed CNPG backend values required")
	}
	var values, postgres, cnpg map[string]json.RawMessage
	if jsoncontract.Decode(raw, &values) != nil || jsoncontract.Decode(values["postgres"], &postgres) != nil || jsoncontract.Decode(postgres["cloudNativePG"], &cnpg) != nil {
		return "", "", errors.New("installed CNPG values invalid")
	}
	var external map[string]json.RawMessage
	if value, ok := postgres["external"]; ok {
		if jsoncontract.Decode(value, &external) != nil {
			return "", "", errors.New("installed external selector invalid")
		}
		if value, ok := external["dsnSecretRef"]; ok {
			var secret map[string]json.RawMessage
			if jsoncontract.Decode(value, &secret) != nil {
				return "", "", errors.New("installed external Secret selector invalid")
			}
			if value, ok := secret["name"]; ok {
				var name string
				if jsoncontract.Decode(value, &name) != nil || name != "" {
					return "", "", errors.New("CNPG with an external DSN Secret override requires separate observed backend support")
				}
			}
		}
	}
	var database, cluster string
	if jsoncontract.Decode(postgres["database"], &database) != nil || !databaseName.MatchString(database) {
		return "", "", errors.New("observed installed CNPG database name required")
	}
	if value, ok := cnpg["clusterName"]; ok && jsoncontract.Decode(value, &cluster) != nil {
		return "", "", errors.New("installed CNPG cluster selection invalid")
	}
	if cluster != "" && !kubeName.MatchString(cluster) {
		return "", "", errors.New("installed CNPG cluster name invalid")
	}
	return database, cluster, nil
}
