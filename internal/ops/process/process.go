// Package process runs explicit argv with bounded, owned subprocesses. Raw
// output remains in exclusive private captures; public errors omit argv, env
// values and child diagnostics. This is not a universal secret scrubber.
package process

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

type ResultKind string

const (
	Success         ResultKind = "success"
	ChildFailed     ResultKind = "child_failed"
	StartFailed     ResultKind = "start_failed"
	Cancelled       ResultKind = "cancelled"
	TimedOut        ResultKind = "timed_out"
	CaptureFailed   ResultKind = "capture_failed"
	OwnershipFailed ResultKind = "ownership_cleanup_failed"
	Unsupported     ResultKind = "unsupported_ownership"
)

type Environment struct {
	Name       string
	Literal    string
	SecretFile string
}

// Spec contains no shell expansion. Secret values must be supplied through a
// protected file/stdin or secret-file environment binding, never an argument.
// Literal environment is for known public configuration; its safety cannot be
// inferred by regex for arbitrary caller-provided values.
type Spec struct {
	Executable          string
	Args                []string
	Environment         []Environment
	Directory           string
	CaptureDirectory    string
	ID                  string
	Sequence            uint16
	StdinFile           string
	Timeout             time.Duration
	TerminationGrace    time.Duration
	MaximumCaptureBytes int64
}

type Result struct {
	Kind          ResultKind
	ExitCode      int
	ChildExitCode int
	Started       bool
	LeaderReaped  bool
	StartedAt     time.Time
	CompletedAt   time.Time
	StdoutSHA256  string
	StderrSHA256  string
}

type Error struct {
	Result Result
	cause  error
}

func (e *Error) Error() string {
	return fmt.Sprintf("owned process %s (exit %d)", e.Result.Kind, e.Result.ExitCode)
}
func (e *Error) Unwrap() error { return e.cause }
func (e *Error) ExitCode() int { return e.Result.ExitCode }

var (
	idPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func validate(spec Spec) error {
	if !filepath.IsAbs(spec.Executable) || !filepath.IsAbs(spec.Directory) || !filepath.IsAbs(spec.CaptureDirectory) || !idPattern.MatchString(spec.ID) || spec.Sequence > 1000 || spec.Timeout <= 0 || spec.Timeout > time.Hour || spec.TerminationGrace <= 0 || spec.TerminationGrace > 5*time.Second || spec.MaximumCaptureBytes <= 0 || spec.MaximumCaptureBytes > 16<<20 {
		return errors.New("explicit absolute process paths and finite capture/lifecycle bounds required")
	}
	names := make(map[string]struct{}, len(spec.Environment))
	for _, env := range spec.Environment {
		if !envPattern.MatchString(env.Name) || (env.SecretFile != "" && (env.Literal != "" || !filepath.IsAbs(env.SecretFile))) {
			return errors.New("invalid environment binding")
		}
		if _, duplicate := names[env.Name]; duplicate {
			return errors.New("duplicate environment binding")
		}
		names[env.Name] = struct{}{}
	}
	if spec.StdinFile != "" && !filepath.IsAbs(spec.StdinFile) {
		return errors.New("private stdin reference must be absolute")
	}
	return nil
}

func Run(ctx context.Context, spec Spec) (Result, error) {
	if err := validate(spec); err != nil {
		r := Result{Kind: StartFailed, ExitCode: 1, ChildExitCode: -1}
		return r, &Error{Result: r, cause: err}
	}
	return runOwned(ctx, spec)
}
