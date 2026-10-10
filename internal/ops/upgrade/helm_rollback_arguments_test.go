package upgrade

import (
	"reflect"
	"testing"
)

func TestHelmRollbackUsesExactAnchorAndExplicitQualifiedWait(t *testing.T) {
	for _, version := range []string{"v3.21.4", "v4.3.0"} {
		t.Run(version, func(t *testing.T) {
			input := helmFixture(t, false)
			input.Tool.Version = version
			args, err := input.RollbackArguments(17)
			if err != nil {
				t.Fatal(err)
			}
			expected := []string{"--kube-context", input.Plan.Identity.Context, "rollback", input.Plan.Identity.Release, "17", "--namespace", input.Plan.Identity.Namespace}
			if version == "v4.3.0" {
				expected = append(expected, "--server-side=false", "--wait=legacy")
			} else {
				expected = append(expected, "--wait")
			}
			expected = append(expected, "--cleanup-on-fail", "--timeout", input.Plan.Timeout.String())
			if !reflect.DeepEqual(args, expected) {
				t.Fatal("rollback changed original revision, hooks or legacy update/wait semantics")
			}
		})
	}
}

func TestHelmRollbackRejectsUnboundOrImplicitRevision(t *testing.T) {
	input := helmFixture(t, false)
	for _, revision := range []uint64{0, ^uint64(0)} {
		if _, err := input.RollbackArguments(revision); err == nil {
			t.Fatal("implicit or unrepresentable history revision admitted")
		}
	}
	input.Plan.Mode = Preview
	if _, err := input.RollbackArguments(1); err == nil {
		t.Fatal("preview constructed rollback mutation")
	}
}
