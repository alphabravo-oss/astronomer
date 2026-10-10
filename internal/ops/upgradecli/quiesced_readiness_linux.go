//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// Every selected PDB is still observed. The richer projection permits only an
// exact initially captured server/worker selector, without match expressions or
// extra selector keys, under the same immutable PDB UID and captured post-scale
// resource version. Other dependencies retain the ordinary positive allowance.
const quiescedPDBTemplate = `{{range .items}}{{.metadata.name}}{{"\t"}}{{.metadata.uid}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{.metadata.resourceVersion}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}{{"\t"}}{{if .status.disruptionsAllowed}}{{.status.disruptionsAllowed}}{{else}}0{{end}}{{"\t"}}{{if .spec.selector.matchLabels}}{{len .spec.selector.matchLabels}}{{else}}0{{end}}{{"\t"}}{{if .spec.selector.matchExpressions}}{{len .spec.selector.matchExpressions}}{{else}}0{{end}}{{"\t"}}{{index .spec.selector.matchLabels "app.kubernetes.io/name"}}{{"\t"}}{{index .spec.selector.matchLabels "app.kubernetes.io/instance"}}{{"\t"}}{{index .spec.selector.matchLabels "app.kubernetes.io/component"}}{{"\n"}}{{end}}`

type ownedQuiescedPDB struct{ name, uid, version, component string }
type observedQuiescedPDB struct {
	ownedQuiescedPDB
	allowance     uint64
	exactSelector bool
}

func (v *DisposableReplacement) pdbs(ctx context.Context) ([]observedQuiescedPDB, error) {
	if err := v.fence(ctx); err != nil {
		return nil, err
	}
	f := v.backup.source.fence
	id := f.options.Identity
	args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", id.Context, "get", "poddisruptionbudgets", "--namespace", id.Namespace, "--selector", "app.kubernetes.io/instance=" + id.Release + ",app.kubernetes.io/part-of=astronomer", "--output", "go-template=" + quiescedPDBTemplate}
	raw, err := f.execute(ctx, f.kubectl, args)
	if err != nil {
		return nil, err
	}
	if err := v.fence(ctx); err != nil {
		return nil, err
	}
	return parseQuiescedPDBRows(raw, id.Namespace, id.Release)
}
func parseQuiescedPDBRows(raw []byte, namespace, release string) ([]observedQuiescedPDB, error) {
	rows, err := readinessRows(raw)
	if err != nil {
		return nil, err
	}
	result := make([]observedQuiescedPDB, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		fields := strings.Split(row, "\t")
		if len(fields) != 11 || !kubeName.MatchString(fields[0]) || fields[1] == "" || fields[2] != namespace || fields[3] == "" || strings.ContainsAny(fields[1]+fields[3], " \r\n\x00") || fields[4] != "live" || seen[fields[0]] {
			return nil, errors.New("selected PDB immutable identity/lifecycle differs")
		}
		allowance, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil || strconv.FormatUint(allowance, 10) != fields[5] {
			return nil, errors.New("PDB allowance projection invalid")
		}
		labels, e1 := strconv.ParseUint(fields[6], 10, 32)
		expressions, e2 := strconv.ParseUint(fields[7], 10, 32)
		if e1 != nil || e2 != nil || strconv.FormatUint(labels, 10) != fields[6] || strconv.FormatUint(expressions, 10) != fields[7] {
			return nil, errors.New("PDB selector count invalid")
		}
		exact := labels == 3 && expressions == 0 && fields[8] == "astronomer" && fields[9] == release && (fields[10] == "server" || fields[10] == "worker")
		result = append(result, observedQuiescedPDB{ownedQuiescedPDB: ownedQuiescedPDB{fields[0], fields[1], fields[3], fields[10]}, allowance: allowance, exactSelector: exact})
		seen[fields[0]] = true
	}
	return result, nil
}
func (v *DisposableReplacement) capturePDBOwners(ctx context.Context) error {
	rows, err := v.pdbs(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.exactSelector {
			v.initialPDBs = append(v.initialPDBs, row.ownedQuiescedPDB)
		}
	}
	return ctx.Err()
}
func (v *DisposableReplacement) captureQuiesced(ctx context.Context) error {
	rows, err := v.workloads(ctx)
	if err != nil {
		return err
	}
	if len(rows) != len(v.deployments) {
		return errors.New("quiesced workload inventory changed")
	}
	for _, old := range v.deployments {
		found := false
		for _, now := range rows {
			if now.name == old.name && now.uid == old.uid && now.component == old.component && now.replicas == 0 {
				found = true
			}
		}
		if !found {
			return errors.New("initial workload not freshly quiesced")
		}
	}
	pdbs, err := v.pdbs(ctx)
	if err != nil {
		return err
	}
	bound := make([]ownedQuiescedPDB, 0, len(v.initialPDBs))
	for _, old := range v.initialPDBs {
		found := false
		for _, now := range pdbs {
			if now.name == old.name && now.uid == old.uid && now.component == old.component && now.exactSelector {
				found = true
				bound = append(bound, now.ownedQuiescedPDB)
			}
		}
		if !found {
			return errors.New("owned PDB changed during quiescence")
		}
	}
	v.quiescedDeployments = rows
	v.quiescedPDBs = bound
	return ctx.Err()
}
func (v *DisposableReplacement) verifyQuiesced(ctx context.Context) error {
	if v == nil || !v.replaced || len(v.quiescedDeployments) != 2 {
		return errors.New("freshly assessed replacement and captured zero workloads required")
	}
	rows, err := v.workloads(ctx)
	if err != nil {
		return err
	}
	if len(rows) != len(v.quiescedDeployments) {
		return errors.New("quiesced workload set changed")
	}
	for _, bound := range v.quiescedDeployments {
		found := false
		for _, now := range rows {
			if now == bound && now.replicas == 0 {
				found = true
			}
		}
		if !found {
			return errors.New("quiesced workload UID/resource version changed")
		}
	}
	return ctx.Err()
}
func (v *DisposableReplacement) checkQuiescedPDBs(ctx context.Context, raw []byte) error {
	// Readiness passes the exact rich projection to the same parser; the second
	// fresh owned observation must equal it, refusing races across gate reads.
	rows, err := v.pdbs(ctx)
	if err != nil {
		return err
	}
	// Comparing parser results rather than public bytes avoids granting selector
	// authority from a caller-supplied projection. The actual read is repeated.
	first, err := parseQuiescedPDBRows(raw, v.backup.source.fence.options.Identity.Namespace, v.backup.source.fence.options.Identity.Release)
	if err != nil {
		return err
	}
	if len(first) != len(rows) {
		return errors.New("selected PDB set changed during readiness")
	}
	for _, bound := range v.quiescedPDBs {
		found := false
		for _, row := range rows {
			if row.exactSelector && row.ownedQuiescedPDB == bound {
				found = true
			}
		}
		if !found {
			return errors.New("captured quiesced PDB UID/resource version changed")
		}
	}
	for i, row := range rows {
		if row != first[i] {
			return errors.New("PDB UID/resource version changed during readiness")
		}
		if row.allowance > 0 {
			continue
		}
		permitted := false
		for _, bound := range v.quiescedPDBs {
			if row.exactSelector && row.ownedQuiescedPDB == bound {
				permitted = true
			}
		}
		if !permitted {
			return errors.New("unowned dependency PDB permits no disruption")
		}
	}
	if err := v.verifyQuiesced(ctx); err != nil {
		return err
	}
	return ctx.Err()
}
