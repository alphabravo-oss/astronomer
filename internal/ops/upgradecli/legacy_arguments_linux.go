//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// LegacyUpgradeArguments maps only the retained shell argv. It grants neither
// cluster identity nor credentials, a replacement lease or release authenticity.
// Default invocation performs backup and server dry run, then exits: the old
// shell has no interactive prompt. --dry-run-only takes precedence over --yes.
type LegacyUpgradeArguments struct {
	Target string
	Mode   upgrade.Mode
	Help   bool
}

type UpgradeUsageError struct{}

func (UpgradeUsageError) Error() string {
	return "usage: upgrade-release [--yes] [--dry-run-only] <exact stable version>"
}
func (UpgradeUsageError) ExitCode() int { return 2 }

var legacyStableTarget = regexp.MustCompile(`^v[1-9][0-9]*\.[0-9]+\.[0-9]+$`)

func ParseLegacyUpgradeArguments(args []string) (LegacyUpgradeArguments, error) {
	result := LegacyUpgradeArguments{Mode: upgrade.Preview}
	approved, dryOnly := false, false
	for _, arg := range args {
		switch arg {
		case "--yes":
			approved = true
		case "--dry-run-only":
			dryOnly = true
		case "-h", "--help":
			return LegacyUpgradeArguments{Help: true, Mode: upgrade.Preview}, nil
		default:
			if strings.HasPrefix(arg, "-") || result.Target != "" {
				return LegacyUpgradeArguments{}, UpgradeUsageError{}
			}
			// Even an empty positional consumes the shell's loop but does not become
			// a valid version. Preserve final validation rather than granting authority.
			result.Target = arg
		}
	}
	if !strings.HasPrefix(result.Target, "v") {
		result.Target = "v" + result.Target
	}
	if !legacyStableTarget.MatchString(result.Target) {
		return LegacyUpgradeArguments{}, UpgradeUsageError{}
	}
	if approved && !dryOnly {
		result.Mode = upgrade.Execute
	}
	return result, nil
}

func bindLegacyUpgradePlan(parsed LegacyUpgradeArguments, observed upgrade.Plan) (upgrade.Plan, error) {
	if parsed.Help || observed.Target != parsed.Target {
		return upgrade.Plan{}, errors.New("legacy target differs from initially authenticated invocation target")
	}
	if observed.Replace != nil && parsed.Mode != upgrade.Execute {
		return upgrade.Plan{}, errors.New("replacement requires explicit --yes without --dry-run-only")
	}
	observed.Mode = parsed.Mode
	return observed, nil
}

// PrepareLegacyInvocation connects the real argument decision to the concrete
// signed Driver factory. Every other option remains an explicit independently
// observed operator binding. Help returns before tool/network/cluster admission.
// No ambient environment, arbitrary argv command or prompt is introduced.
// The caller owns Run, Close and public evidence publication on the returned
// invocation; this does not yet replace the historical shell entrypoint.
func PrepareLegacyInvocation(ctx context.Context, args []string, options InvocationOptions) (*Invocation, bool, error) {
	parsed, err := ParseLegacyUpgradeArguments(args)
	if err != nil {
		return nil, false, err
	}
	if parsed.Help {
		return nil, true, nil
	}
	plan, err := bindLegacyUpgradePlan(parsed, options.Plan)
	if err != nil {
		return nil, false, err
	}
	options.Plan = plan
	invocation, err := PrepareInvocation(ctx, options)
	return invocation, false, err
}
