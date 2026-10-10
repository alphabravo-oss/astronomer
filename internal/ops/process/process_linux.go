//go:build linux

package process

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var errCaptureBudget = errors.New("private capture byte budget exceeded")

type limitedCapture struct {
	file     *os.File
	maximum  int64
	size     int64
	exceeded bool
	mu       sync.Mutex
}

func (w *limitedCapture) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.maximum - w.size
	if int64(len(payload)) > remaining {
		written, err := w.file.Write(payload[:remaining])
		w.size += int64(written)
		w.exceeded = true
		if err != nil {
			return written, err
		}
		return written, errCaptureBudget
	}
	written, err := w.file.Write(payload)
	w.size += int64(written)
	return written, err
}
func privateInput(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 {
		return nil, errors.New("private regular0600 input required")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("open private input")
	}
	file := os.NewFile(uintptr(fd), "private-input")
	actual, err := file.Stat()
	if err != nil || !actual.Mode().IsRegular() || actual.Mode().Perm() != 0o600 || !os.SameFile(before, actual) || actual.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		_ = file.Close()
		return nil, errors.New("private input owner or identity differs")
	}
	return file, nil
}
func privateCaptures(spec Spec) (*os.Root, *os.File, *os.File, error) {
	parent, err := os.Lstat(spec.CaptureDirectory)
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0o700 || parent.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, nil, nil, errors.New("owned private0700 capture directory required")
	}
	resolved, err := filepath.EvalSymlinks(spec.CaptureDirectory)
	if err != nil || resolved != spec.CaptureDirectory {
		return nil, nil, nil, errors.New("capture directory alias rejected")
	}
	root, err := os.OpenRoot(spec.CaptureDirectory)
	if err != nil {
		return nil, nil, nil, errors.New("open private capture scope")
	}
	opened, statErr := root.Stat(".")
	if statErr != nil || !os.SameFile(parent, opened) {
		_ = root.Close()
		return nil, nil, nil, errors.New("capture directory identity changed")
	}
	name := fmt.Sprintf("%s-%03d", spec.ID, spec.Sequence)
	stdout, err := root.OpenFile(name+".stdout", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = root.Close()
		return nil, nil, nil, errors.New("exclusive private stdout required")
	}
	stderr, err := root.OpenFile(name+".stderr", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = stdout.Close()
		_ = root.Close()
		return nil, nil, nil, errors.New("exclusive private stderr required")
	}
	return root, stdout, stderr, nil
}
func captureDigest(root *os.Root, file *os.File, name string) (string, error) {
	actual, err := file.Stat()
	if err != nil {
		return "", err
	}
	named, err := root.Lstat(name)
	if err != nil || !os.SameFile(actual, named) || !named.Mode().IsRegular() || named.Mode().Perm() != 0o600 {
		return "", errors.New("private capture identity changed")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
func runOwned(ctx context.Context, spec Spec) (result Result, runErr error) {
	result = Result{Kind: StartFailed, ExitCode: 1, ChildExitCode: -1}
	root, stdout, stderr, err := privateCaptures(spec)
	if err != nil {
		return result, &Error{Result: result, cause: err}
	}
	defer func() {
		if result.Started && !result.LeaderReaped {
			_ = stdout.Close()
			_ = stderr.Close()
			_ = root.Close()
			result.CompletedAt = time.Now().UTC()
			runErr = &Error{Result: result, cause: runErr}
			return // incomplete captures must not be presented as stable output hashes
		}
		name := fmt.Sprintf("%s-%03d", spec.ID, spec.Sequence)
		var outErr, diagErr error
		result.StdoutSHA256, outErr = captureDigest(root, stdout, name+".stdout")
		result.StderrSHA256, diagErr = captureDigest(root, stderr, name+".stderr")
		captureErr := errors.Join(outErr, diagErr, stdout.Close(), stderr.Close(), root.Close())
		if captureErr != nil {
			if result.Kind == Success {
				result.Kind, result.ExitCode = CaptureFailed, 1
			}
			runErr = errors.Join(runErr, captureErr)
		}
		result.CompletedAt = time.Now().UTC()
		if runErr != nil {
			runErr = &Error{Result: result, cause: runErr}
		}
	}()
	command, captures, closeInput, err := prepareCommand(ctx, spec, stdout, stderr)
	defer closeInput()
	if err != nil {
		return result, err
	}
	out, diagnostic := captures[0], captures[1]
	bounded, stop := context.WithTimeout(ctx, spec.Timeout)
	defer stop()
	if err = bounded.Err(); err != nil {
		result.Kind, result.ExitCode = cancellationResult(err)
		return result, err
	}
	result.StartedAt = time.Now().UTC()
	if err = command.Start(); err != nil {
		return result, err
	}
	result.Started = true
	err = awaitOwned(bounded, command, spec, &result)
	if result.LeaderReaped && command.ProcessState != nil {
		result.ChildExitCode = command.ProcessState.ExitCode()
	}
	if result.Kind != Cancelled && result.Kind != TimedOut && result.Kind != OwnershipFailed {
		result.Kind, result.ExitCode = Success, 0
		if err != nil {
			result.Kind, result.ExitCode = ChildFailed, result.ChildExitCode
			if result.ExitCode <= 0 {
				result.ExitCode = 1
			}
		}
		if out.exceeded || diagnostic.exceeded {
			result.Kind, result.ExitCode = CaptureFailed, 1
			err = errors.Join(err, errCaptureBudget)
		}
	}
	return result, err
}

func cancellationResult(err error) (ResultKind, int) {
	if errors.Is(err, context.DeadlineExceeded) {
		return TimedOut, 124
	}
	return Cancelled, 130
}
func waitLeader(ctx context.Context, fd int) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		events := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		count, err := unix.Poll(events, 25)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if count > 0 {
			if events[0].Revents&unix.POLLIN != 0 {
				return nil
			}
			return errors.New("kernel child identity poll failed")
		}
	}
}

func prepareCommand(ctx context.Context, spec Spec, stdout, stderr *os.File) (*exec.Cmd, []*limitedCapture, func(), error) {
	closeInput := func() {}
	command := exec.Command(spec.Executable, spec.Args...)
	command.Dir = spec.Directory
	command.Env = make([]string, 0, len(spec.Environment)) // no ambient credential inheritance
	for _, binding := range spec.Environment {
		value := binding.Literal
		if binding.SecretFile != "" {
			file, openErr := privateInput(binding.SecretFile)
			if openErr != nil {
				return nil, nil, closeInput, openErr
			}
			bytes, readErr := readPrivateEnvironment(ctx, file)
			_ = file.Close()
			if readErr != nil || len(bytes) > 64<<10 {
				return nil, nil, closeInput, errors.New("bounded private environment read failed")
			}
			value = strings.TrimSuffix(strings.TrimSuffix(string(bytes), "\n"), "\r")
		}
		command.Env = append(command.Env, binding.Name+"="+value)
	}
	if spec.StdinFile != "" {
		input, openErr := privateInput(spec.StdinFile)
		if openErr != nil {
			return nil, nil, closeInput, openErr
		}
		command.Stdin = input
		closeInput = func() { _ = input.Close() }
	}
	out := &limitedCapture{file: stdout, maximum: spec.MaximumCaptureBytes}
	diagnostic := &limitedCapture{file: stderr, maximum: spec.MaximumCaptureBytes}
	command.Stdout, command.Stderr = out, diagnostic
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = spec.TerminationGrace
	return command, []*limitedCapture{out, diagnostic}, closeInput, nil
}

func awaitOwned(bounded context.Context, command *exec.Cmd, spec Spec, result *Result) error {
	// Open a kernel reference before waiting. The leader is deliberately not
	// reaped until group cleanup: its zombie retains the group ID, preventing a
	// recycled PID/group from being targeted after a fast parent exit.
	leader, openErr := unix.PidfdOpen(command.Process.Pid, 0)
	if openErr != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		waitErr, reaped := waitCommand(command, spec.TerminationGrace)
		result.LeaderReaped = reaped
		if !reaped {
			result.Kind, result.ExitCode = OwnershipFailed, 1
		}
		return errors.Join(errors.New("kernel child identity unavailable"), openErr, waitErr)
	}
	defer func() { _ = unix.Close(leader) }()
	exitErr := waitLeader(bounded, leader)
	if exitErr != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		grace, graceStop := context.WithTimeout(context.WithoutCancel(bounded), spec.TerminationGrace)
		_ = waitLeader(grace, leader)
		graceStop()
	}
	// Only this still-unreaped leader's group is addressed. Trusted command
	// adapters must not escape their process group; daemonization is unsupported.
	killErr := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(killErr, syscall.ESRCH) {
		killErr = nil
	}
	// WaitDelay bounds pipe draining; this outer wait also bounds an abnormal
	// kernel wait after KILL. An unreaped child is explicit unresolved ownership,
	// never proof that a mutation can be replayed or recovery material removed.
	waitErr, reaped := waitCommand(command, spec.TerminationGrace)
	result.LeaderReaped = reaped
	if !reaped {
		result.Kind, result.ExitCode = OwnershipFailed, 1
		if exitErr != nil {
			_, result.ExitCode = cancellationResult(exitErr)
		}
		return errors.Join(exitErr, killErr, waitErr)
	}
	err := errors.Join(waitErr, killErr)
	if exitErr != nil {
		if bounded.Err() != nil {
			result.Kind, result.ExitCode = cancellationResult(exitErr)
		} else {
			result.Kind, result.ExitCode = ChildFailed, 1
		}
		err = errors.Join(exitErr, err)
	}
	return err
}

func waitCommand(command *exec.Cmd, grace time.Duration) (error, bool) {
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	timer := time.NewTimer(2 * grace)
	defer timer.Stop()
	select {
	case err := <-done:
		return err, true
	case <-timer.C:
		return errors.New("owned child wait did not close within bound"), false
	}
}

// Context checks surround bounded reads of a revalidated regular file. This
// excludes blocking FIFO/device inputs; it cannot promise arbitrary kernel I/O
// is interruptible in a failed filesystem.
func readPrivateEnvironment(ctx context.Context, file *os.File) ([]byte, error) {
	result := make([]byte, 0, 4096)
	buffer := make([]byte, 4096)
	for len(result) <= 64<<10 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count, err := file.Read(buffer)
		result = append(result, buffer[:count]...)
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, errors.New("private environment exceeds byte budget")
}
