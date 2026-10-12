package upgrade

import (
	"errors"
	"strconv"
)

// RollbackArguments retains the original anchored revision, hooks and bounded
// legacy wait. A v4 rollback explicitly keeps v3 client-side update semantics;
// zero/omitted revision must never silently select a different history entry.
func (in HelmInputs) RollbackArguments(revision uint64) ([]string, error) {
	if in.Plan.Mode != Execute || revision == 0 || revision > uint64(^uint(0)>>1) {
		return nil, errors.New("exact representable original rollback revision required")
	}
	if _, err := in.arguments(); err != nil {
		return nil, err
	}
	major, err := in.Tool.major()
	if err != nil {
		return nil, err
	}
	args := []string{"--kube-context", in.Plan.Identity.Context, "rollback", in.Plan.Identity.Release, strconv.FormatUint(revision, 10), "--namespace", in.Plan.Identity.Namespace}
	if major == 4 {
		args = append(args, "--server-side=false", "--wait=legacy")
	} else {
		args = append(args, "--wait")
	}
	return append(args, "--cleanup-on-fail", "--timeout", in.Plan.Timeout.String()), nil
}
