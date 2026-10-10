//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

const readinessRowsLimit = 10000

const readyNodesTemplate = `{{range .items}}{{$ready := false}}{{if not .spec.unschedulable}}{{range .status.conditions}}{{if and (eq .type "Ready") (eq .status "True")}}{{$ready = true}}{{end}}{{end}}{{end}}{{if $ready}}1{{else}}0{{end}}{{"\n"}}{{end}}`
const deploymentsReadyTemplate = `{{range .items}}{{.metadata.generation}}{{"\t"}}{{if .status.observedGeneration}}{{.status.observedGeneration}}{{else}}0{{end}}{{"\t"}}{{if .spec.replicas}}{{.spec.replicas}}{{else}}0{{end}}{{"\t"}}{{if .status.availableReplicas}}{{.status.availableReplicas}}{{else}}0{{end}}{{"\t"}}{{if .status.updatedReplicas}}{{.status.updatedReplicas}}{{else}}0{{end}}{{"\n"}}{{end}}`
const disruptionTemplate = `{{range .items}}{{if .status.disruptionsAllowed}}{{.status.disruptionsAllowed}}{{else}}0{{end}}{{"\n"}}{{end}}`

// Readiness retains the legacy node/deployment/PDB/Secret-read preconditions.
// It performs owned joined reads, never acquires authorization from readiness
// or replaces signer, chart, backup, schema or post-upgrade health verification.
type Readiness struct {
	fence             *OwnershipFence
	minimumReadyNodes uint32
}

func NewReadiness(fence *OwnershipFence, minimumReadyNodes uint32) (*Readiness, error) {
	if fence == nil || minimumReadyNodes == 0 || minimumReadyNodes > readinessRowsLimit {
		return nil, errors.New("explicit owned readiness composition and bounded node minimum required")
	}
	return &Readiness{fence: fence, minimumReadyNodes: minimumReadyNodes}, nil
}

func (r *Readiness) Check(ctx context.Context) error {
	if r == nil || ctx == nil {
		return errors.New("explicit readiness context and composition required")
	}
	id := r.fence.options.Identity
	if err := r.fence.Fence(ctx, id, nil); err != nil {
		return err
	}
	read := func(args ...string) ([]byte, error) {
		base := []string{"--kubeconfig", r.fence.options.Kubeconfig, "--context", id.Context}
		raw, err := r.fence.execute(ctx, r.fence.kubectl, append(base, args...))
		if err != nil {
			return nil, err
		}
		return raw, ctx.Err()
	}
	permission, err := read("auth", "can-i", "get", "secrets", "--namespace", id.Namespace)
	if err != nil {
		return err
	}
	if string(permission) != "yes\n" && string(permission) != "yes" {
		return errors.New("operator cannot read recovery Secrets")
	}
	nodes, err := read("get", "nodes", "--output", "go-template="+readyNodesTemplate)
	if err != nil {
		return err
	}
	if err := checkReadyNodes(nodes, r.minimumReadyNodes); err != nil {
		return err
	}
	selector := "app.kubernetes.io/instance=" + id.Release + ",app.kubernetes.io/part-of=astronomer"
	deployments, err := read("get", "deployments", "--namespace", id.Namespace, "--selector", selector, "--output", "go-template="+deploymentsReadyTemplate)
	if err != nil {
		return err
	}
	if err := checkReadyDeployments(deployments); err != nil {
		return err
	}
	pdbs, err := read("get", "poddisruptionbudgets", "--namespace", id.Namespace, "--selector", selector, "--output", "go-template="+disruptionTemplate)
	if err != nil {
		return err
	}
	if err := checkDisruptionAllowance(pdbs); err != nil {
		return err
	}
	// All observations must finish before acknowledging readiness. A recreated
	// owner or canceled final observation never qualifies the next mutation.
	return r.fence.Fence(ctx, id, nil)
}

func readinessRows(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// The command already bounds capture bytes. Check row count before Split
	// allocates to keep the source parser's item bound independent of a driver.
	if len(raw) > 1<<20 || raw[len(raw)-1] != '\n' || strings.Count(string(raw), "\n") > readinessRowsLimit {
		return nil, errors.New("readiness observation exceeds bounded row contract")
	}
	return strings.Split(string(raw[:len(raw)-1]), "\n"), nil
}

func checkReadyNodes(raw []byte, minimum uint32) error {
	rows, err := readinessRows(raw)
	if err != nil {
		return err
	}
	var ready uint32
	for _, row := range rows {
		switch row {
		case "1":
			ready++
		case "0":
		default:
			return errors.New("node readiness projection differs")
		}
	}
	if ready < minimum {
		return errors.New("insufficient schedulable Ready nodes")
	}
	return nil
}

func checkReadyDeployments(raw []byte) error {
	rows, err := readinessRows(raw)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return errors.New("no owned Astronomer deployments observed")
	}
	for _, row := range rows {
		fields := strings.Split(row, "\t")
		if len(fields) != 5 {
			return errors.New("deployment readiness projection differs")
		}
		var counts [5]uint64
		for i, field := range fields {
			value, parseErr := strconv.ParseUint(field, 10, 64)
			if parseErr != nil || strconv.FormatUint(value, 10) != field {
				return errors.New("deployment readiness counts differ")
			}
			counts[i] = value
		}
		if counts[0] == 0 || counts[0] > counts[1] || counts[3] < counts[2] || counts[4] < counts[2] {
			return errors.New("owned deployment is not fully observed updated and available")
		}
	}
	return nil
}

func checkDisruptionAllowance(raw []byte) error {
	rows, err := readinessRows(raw)
	if err != nil {
		return err
	}
	for _, row := range rows {
		value, parseErr := strconv.ParseUint(row, 10, 64)
		if parseErr != nil || strconv.FormatUint(value, 10) != row || value == 0 {
			return errors.New("owned PodDisruptionBudget permits no voluntary disruption")
		}
	}
	return nil
}
