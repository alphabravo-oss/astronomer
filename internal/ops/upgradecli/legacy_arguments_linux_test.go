//go:build linux

package upgradecli

import (
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

func TestLegacyUpgradeArgumentsPreservePreviewAndExplicitApproval(t *testing.T) {
	cases := []struct {
		name string
		args []string
		mode upgrade.Mode
		help bool
	}{
		{"default", []string{"1.2.3"}, upgrade.Preview, false},
		{"prefixed", []string{"v1.2.3"}, upgrade.Preview, false},
		{"yes-before", []string{"--yes", "1.2.3"}, upgrade.Execute, false},
		{"yes-after", []string{"v1.2.3", "--yes"}, upgrade.Execute, false},
		{"duplicate-yes", []string{"--yes", "--yes", "v1.2.3"}, upgrade.Execute, false},
		{"dry-only", []string{"--dry-run-only", "v1.2.3"}, upgrade.Preview, false},
		{"yes-and-dry", []string{"--yes", "v1.2.3", "--dry-run-only"}, upgrade.Preview, false},
		{"dry-and-yes", []string{"--dry-run-only", "--yes", "v1.2.3"}, upgrade.Preview, false},
		{"help-short", []string{"-h"}, upgrade.Preview, true},
		{"help-stops-validation", []string{"not-a-version", "--help", "--unknown"}, upgrade.Preview, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLegacyUpgradeArguments(tc.args)
			if err != nil || got.Help != tc.help || got.Mode != tc.mode || (!tc.help && got.Target != "v1.2.3") {
				t.Fatal("legacy argv decision differs", got, err)
			}
		})
	}
	for _, args := range [][]string{nil, {""}, {"--yes"}, {"--unknown", "--help"}, {"--"}, {"v1.2.3", "v1.2.4"}, {"--yes=true", "v1.2.3"}, {"v2.0.0-rc.1"}, {"v1.2.3-rc.1"}, {"v1.2.3+build"}, {"v1.2"}, {"1.2.3;echo bad"}} {
		if _, err := ParseLegacyUpgradeArguments(args); err == nil {
			t.Fatal("invalid shell argv accepted")
		} else {
			var usage UpgradeUsageError
			if !errors.As(err, &usage) || usage.ExitCode() != 2 {
				t.Fatal("legacy usage exit differs")
			}
		}
	}
}

func TestLegacyUpgradePlanBindsOnlyModeAndRetainsObservedAuthority(t *testing.T) {
	observed := helmReleaseFixture(t).plan
	before := observed
	for _, args := range [][]string{{"--yes", observed.Target}, {observed.Target}, {"--yes", "--dry-run-only", observed.Target}} {
		parsed, err := ParseLegacyUpgradeArguments(args)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := bindLegacyUpgradePlan(parsed, observed)
		if err != nil {
			t.Fatal(err)
		}
		expected := before
		expected.Mode = parsed.Mode
		if bound != expected || observed != before {
			t.Fatal("argv changed independent authority or caller plan")
		}
	}
	parsed, _ := ParseLegacyUpgradeArguments([]string{"v1.999.999"})
	if _, err := bindLegacyUpgradePlan(parsed, observed); err == nil {
		t.Fatal("argv granted a different unauthenticated target")
	}
	lease := &upgrade.DisposableLease{Owner: "owned-fixture"}
	observed.Replace = lease
	parsed, _ = ParseLegacyUpgradeArguments([]string{"--yes", "--dry-run-only", observed.Target})
	if _, err := bindLegacyUpgradePlan(parsed, observed); err == nil {
		t.Fatal("dry-run granted replacement authority")
	}
	parsed, _ = ParseLegacyUpgradeArguments([]string{"--yes", observed.Target})
	bound, err := bindLegacyUpgradePlan(parsed, observed)
	if err != nil || bound.Replace != lease {
		t.Fatal("explicit replacement lease was inferred or replaced", err)
	}
}

func TestPrepareLegacyInvocationRejectsUsageAndTargetBeforeConcreteAdmission(t *testing.T) {
	if invocation, help, err := PrepareLegacyInvocation(nil, []string{"--help"}, InvocationOptions{}); err != nil || !help || invocation != nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("help reached concrete factory")
	}
	if invocation, help, err := PrepareLegacyInvocation(nil, []string{"--unknown"}, InvocationOptions{}); err == nil || help || invocation != nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("usage reached concrete factory")
	}
	if invocation, help, err := PrepareLegacyInvocation(nil, []string{"--yes", "v1.2.3"}, InvocationOptions{Plan: upgrade.Plan{Target: "v1.2.4"}}); err == nil || help || invocation != nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("unauthenticated argument target reached concrete factory")
	}
}

func TestMajorReleaseUpgradeArguments(t *testing.T) {
	got, err := ParseLegacyUpgradeArguments([]string{"--yes", "v2.0.0"})
	if err != nil || got.Target != "v2.0.0" || got.Mode != upgrade.Execute {
		t.Fatalf("major-release upgrade rejected: %+v %v", got, err)
	}
}
