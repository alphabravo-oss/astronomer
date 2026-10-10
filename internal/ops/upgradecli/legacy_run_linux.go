//go:build linux

package upgradecli

import (
	"context"
	"errors"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// LegacyRunResult separates the acknowledged upgrade result, public publication
// and resource-close errors. ExitCode is the outer CLI result; Result remains
// the exact acknowledged Machine result. A failed report must not erase the original child
// failure or invent successful application. No private command bytes are exposed.
type LegacyRunResult struct {
	Result           upgrade.Result
	ExitCode         int
	Help             bool
	Report           PublishedReport
	PublicationError error
	CloseError       error
}

// RunLegacyUpgrade connects legacy argv to the concrete factory and durable
// machine, then publishes only the invocation's own completed sanitized report.
// Explicit operator bindings and an already-owned public directory remain the
// caller's responsibility. There is no ambient environment or command lookup.
// Successful early help performs no preparation, backup, report or command.
// External/replacement/resume support is unchanged from the real factory.
// The shell is not switched to this adapter before executable parity is proven.
func RunLegacyUpgrade(ctx context.Context, args []string, options InvocationOptions, evidenceDirectory string) (result LegacyRunResult, returned error) {
	invocation, help, err := PrepareLegacyInvocation(ctx, args, options)
	if err != nil {
		result.Result = upgrade.Result{ExitCode: 1, Result: "failed", FailedStage: upgrade.Verify}
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) && coded.ExitCode() > 0 && coded.ExitCode() < 256 {
			result.Result.ExitCode = coded.ExitCode()
		}
		if errors.Is(err, context.Canceled) {
			result.Result.ExitCode = 130
		}
		if errors.Is(err, context.DeadlineExceeded) {
			result.Result.ExitCode = 124
		}
		result.ExitCode = result.Result.ExitCode
		return result, err
	}
	if help {
		result.Help = true
		result.ExitCode = 0
		return result, nil
	}
	defer func() {
		result.CloseError = invocation.Close()
		if result.CloseError != nil && result.ExitCode == 0 {
			result.ExitCode = 1
		}
		returned = errors.Join(returned, result.CloseError)
	}()
	result.Result = invocation.Run(ctx)
	result.ExitCode = result.Result.ExitCode
	// The old wrapper retains evidence after a failed/cancelled mutation. Never
	// reuse its cancelled work context, relax private-byte validation or fabricate
	// a missing backup digest. Publication is still finite and independently fails.
	publication, stop := context.WithTimeout(context.WithoutCancel(ctx), invocation.plan.CleanupTimeout)
	defer stop()
	result.Report, result.PublicationError = invocation.PublishSanitizedReport(publication, evidenceDirectory)
	if result.PublicationError != nil && result.ExitCode == 0 {
		result.ExitCode = 1
	}
	return result, result.PublicationError
}
