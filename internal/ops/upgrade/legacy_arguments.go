package upgrade

import (
	"errors"
	"strings"
)

// LegacyArguments preserves upgrade-release.sh's existing argv interpretation.
// Preview still creates a verified backup and temporary restore: it is not a
// zero-write operation. Parsing never reads ambient environment or Kubernetes.
type LegacyArguments struct {
	Help   bool
	Target string
	Mode   Mode
}
type UsageError struct{ cause string }

func (e *UsageError) Error() string { return e.cause }
func (e *UsageError) ExitCode() int { return 2 }
func ParseLegacyArguments(arguments []string) (LegacyArguments, error) {
	parsed := LegacyArguments{Mode: Preview}
	approve, dryRun := false, false
	for _, argument := range arguments {
		switch argument {
		case "-h", "--help":
			parsed.Help = true
			return parsed, nil
		case "--yes":
			approve = true
		case "--dry-run-only":
			dryRun = true
		default:
			if strings.HasPrefix(argument, "-") {
				return parsed, &UsageError{"unknown upgrade option"}
			}
			if parsed.Target != "" {
				return parsed, &UsageError{"only one upgrade version may be supplied"}
			}
			parsed.Target = argument
		}
	}
	if parsed.Target == "" {
		return parsed, &UsageError{"exact stable target required"}
	}
	if !strings.HasPrefix(parsed.Target, "v") {
		parsed.Target = "v" + parsed.Target
	}
	if !stableRelease.MatchString(parsed.Target) {
		return parsed, &UsageError{"target must be an exact stable version"}
	}
	if approve && !dryRun {
		parsed.Mode = Execute
	}
	return parsed, nil
}

// ParseUsageExit preserves the shell's usage exit separately from operational
// and child failures. It does not turn malformed argv into an upgrade plan.
func ParseUsageExit(err error) int {
	if err == nil {
		return 0
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}
