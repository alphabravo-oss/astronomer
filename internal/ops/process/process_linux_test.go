//go:build linux

package process

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This actual child accepts synthetic fixtures only; values never enter the
// runner's public error. It may fork into its inherited owned process group.
func TestOwnedProcessHelper(t *testing.T) {
	mode := os.Getenv("OPS_PROCESS_HELPER")
	switch mode {
	case "exit":
		os.Exit(43)
	case "environment":
		payload, _ := io.ReadAll(os.Stdin)
		_, _ = os.Stdout.Write([]byte(os.Getenv("OPS_PROCESS_SECRET") + ":" + string(payload) + ":" + os.Getenv("OPS_PROCESS_AMBIENT")))
	case "overflow":
		_, _ = os.Stdout.Write([]byte(strings.Repeat("x", 8192)))
	case "descendant":
		child := exec.Command(os.Args[0], "-test.run=^TestOwnedProcessHelper$")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		child.Env = []string{"OPS_PROCESS_HELPER=wait", "OPS_PROCESS_PID=" + os.Getenv("OPS_PROCESS_PID")}
		if child.Start() != nil {
			os.Exit(2)
		}
		// Exit only after the descendant reached its wait; the process runner must
		// retain the exited leader's identity while retiring that surviving child.
		deadline := time.Now().Add(time.Second)
		for {
			if _, err := os.Stat(os.Getenv("OPS_PROCESS_PID")); err == nil {
				os.Exit(0)
			}
			if time.Now().After(deadline) {
				os.Exit(3)
			}
			time.Sleep(time.Millisecond)
		}
	case "wait":
		if path := os.Getenv("OPS_PROCESS_PID"); path != "" {
			if os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600) != nil {
				os.Exit(2)
			}
		}
		for {
			_, _ = os.Stdout.Write([]byte("synthetic pipe tick\n"))
			time.Sleep(10 * time.Millisecond)
		}
	}
}
func processFixture(t *testing.T, mode string) Spec {
	t.Helper()
	root := t.TempDir()
	if os.Chmod(root, 0o700) != nil {
		t.Fatal("protect fixture")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("test executable unavailable")
	}
	return Spec{Executable: executable, Args: []string{"-test.run=^TestOwnedProcessHelper$"}, Environment: []Environment{{Name: "OPS_PROCESS_HELPER", Literal: mode}}, Directory: root, CaptureDirectory: root, ID: "fixture", Timeout: 2 * time.Second, TerminationGrace: 100 * time.Millisecond, MaximumCaptureBytes: 4096}
}
func TestOwnedProcessPreservesChildExitAndPrivateCapture(t *testing.T) {
	spec := processFixture(t, "exit")
	result, err := Run(context.Background(), spec)
	var child *Error
	if !result.LeaderReaped || result.Kind != ChildFailed || result.ExitCode != 43 || result.ChildExitCode != 43 || !errors.As(err, &child) || child.ExitCode() != 43 || result.StdoutSHA256 == "" || result.StderrSHA256 == "" {
		t.Fatalf("exit/capture contract differs: %+v %v", result, err)
	}
	for _, name := range []string{"fixture-000.stdout", "fixture-000.stderr"} {
		stat, e := os.Lstat(filepath.Join(spec.CaptureDirectory, name))
		if e != nil || stat.Mode().Perm() != 0o600 {
			t.Fatal("capture not private")
		}
	}
	// No subsequent run overwrites retained evidence.
	again, e := Run(context.Background(), spec)
	if e == nil || again.Started {
		t.Fatal("reused capture admitted a child")
	}
}
func TestOwnedProcessUsesExplicitPrivateInputsWithoutAmbientEnvironment(t *testing.T) {
	t.Setenv("OPS_PROCESS_AMBIENT", "synthetic ambient secret")
	spec := processFixture(t, "environment")
	secret := filepath.Join(spec.Directory, "secret")
	stdin := filepath.Join(spec.Directory, "stdin")
	for path, value := range map[string]string{secret: "synthetic bound secret\n", stdin: "synthetic private stdin"} {
		if os.WriteFile(path, []byte(value), 0o600) != nil {
			t.Fatal("private input unavailable")
		}
	}
	spec.Environment = append(spec.Environment, Environment{Name: "OPS_PROCESS_SECRET", SecretFile: secret})
	spec.StdinFile = stdin
	result, err := Run(context.Background(), spec)
	if err != nil || result.Kind != Success {
		t.Fatalf("private child failed: %+v %v", result, err)
	}
	bytes, e := os.ReadFile(filepath.Join(spec.CaptureDirectory, "fixture-000.stdout"))
	if e != nil || !strings.Contains(string(bytes), "synthetic bound secret:synthetic private stdin:") || strings.Contains(string(bytes), "synthetic ambient secret") {
		t.Fatal("private input or ambient environment contract differs")
	}
}
func TestOwnedProcessRejectsPrivateAliasBeforeChild(t *testing.T) {
	for _, kind := range []string{"symlink_input", "public_input", "fifo_input", "symlink_directory", "public_directory"} {
		t.Run(kind, func(t *testing.T) {
			spec := processFixture(t, "wait")
			private := filepath.Join(spec.Directory, "private")
			if os.WriteFile(private, []byte("synthetic secret"), 0o600) != nil {
				t.Fatal("input unavailable")
			}
			switch kind {
			case "symlink_input":
				alias := private + "-alias"
				if os.Symlink(private, alias) != nil {
					t.Fatal("alias unavailable")
				}
				spec.StdinFile = alias
			case "fifo_input":
				if os.Remove(private) != nil || syscall.Mkfifo(private, 0o600) != nil {
					t.Fatal("fifo fixture unavailable")
				}
				spec.StdinFile = private
			case "public_input":
				if os.Chmod(private, 0o644) != nil {
					t.Fatal("mode unavailable")
				}
				spec.StdinFile = private
			case "symlink_directory":
				alias := spec.Directory + "-alias"
				if os.Symlink(spec.Directory, alias) != nil {
					t.Fatal("alias unavailable")
				}
				t.Cleanup(func() { _ = os.Remove(alias) })
				spec.CaptureDirectory = alias
			case "public_directory":
				if os.Chmod(spec.Directory, 0o755) != nil {
					t.Fatal("mode unavailable")
				}
			}
			result, err := Run(context.Background(), spec)
			if err == nil || result.Started || strings.Contains(err.Error(), "synthetic secret") {
				t.Fatal("private preflight admitted child or disclosed input")
			}
		})
	}
}
func TestOwnedProcessBoundsCaptureAndRetiresSurvivingDescendant(t *testing.T) {
	t.Run("capture_budget", func(t *testing.T) {
		spec := processFixture(t, "overflow")
		result, err := Run(context.Background(), spec)
		if err == nil || result.Kind != CaptureFailed {
			t.Fatalf("capture overflow not rejected: %+v", result)
		}
		stat, e := os.Stat(filepath.Join(spec.CaptureDirectory, "fixture-000.stdout"))
		if e != nil || stat.Size() > spec.MaximumCaptureBytes {
			t.Fatal("private capture exceeded bound")
		}
	})
	t.Run("surviving_child", func(t *testing.T) {
		spec := processFixture(t, "descendant")
		path := filepath.Join(spec.Directory, "pid")
		spec.Environment = append(spec.Environment, Environment{Name: "OPS_PROCESS_PID", Literal: path})
		result, err := Run(context.Background(), spec)
		if err != nil || result.Kind != Success {
			t.Fatalf("controlled parent failed: %+v %v", result, err)
		}
		bytes, e := os.ReadFile(path)
		pid, parse := strconv.Atoi(string(bytes))
		if e != nil || parse != nil {
			t.Fatal("descendant did not start")
		}
		assertCaptureStable(t, spec)
		deadline := time.Now().Add(time.Second)
		for {
			// An unreaped orphan zombie has stopped executing and cannot mutate fixture
			// resources. Its eventual reaping belongs to the host's init process.
			stat, readErr := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
			if errors.Is(readErr, os.ErrNotExist) || (readErr == nil && strings.Contains(string(stat), ") Z ")) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("owned descendant remains running")
			}
			time.Sleep(time.Millisecond)
		}
	})
}
func TestOwnedProcessPreservesCancellationAndDeadline(t *testing.T) {
	for _, kind := range []string{"cancel", "deadline", "preexpired"} {
		t.Run(kind, func(t *testing.T) {
			spec := processFixture(t, "wait")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected := Cancelled
			exit := 130
			if kind == "cancel" {
				go func() { time.Sleep(100 * time.Millisecond); cancel() }()
			} else {
				expected = TimedOut
				exit = 124
				spec.Timeout = 100 * time.Millisecond
			}
			if kind == "preexpired" {
				var stop context.CancelFunc
				ctx, stop = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer stop()
			}
			result, err := Run(ctx, spec)
			if result.Kind != expected || result.ExitCode != exit || err == nil {
				t.Fatalf("cancellation contract differs: %+v %v", result, err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost")
			}
			if kind != "cancel" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("deadline identity lost")
			}
			if kind == "preexpired" && result.Started {
				t.Fatal("preexpired child admitted")
			}
			if result.Started {
				if !result.LeaderReaped {
					t.Fatal("cancellation did not join child/pipe writers")
				}
				assertCaptureStable(t, spec)
			}
		})
	}
}
func TestOwnedProcessRejectsMissingDependencyAndDuplicateEnvironment(t *testing.T) {
	spec := processFixture(t, "wait")
	spec.Executable = filepath.Join(spec.Directory, "not-installed")
	result, err := Run(context.Background(), spec)
	if err == nil || result.Started || result.Kind != StartFailed {
		t.Fatal("missing dependency admitted")
	}
	spec = processFixture(t, "wait")
	spec.Environment = append(spec.Environment, spec.Environment[0])
	result, err = Run(context.Background(), spec)
	if err == nil || result.Started {
		t.Fatal("duplicate binding admitted")
	}
}

func TestOwnedProcessRejectsNilContextAndOversizedSecret(t *testing.T) {
	spec := processFixture(t, "wait")
	result, err := Run(nil, spec)
	if err == nil || result.Started {
		t.Fatal("nil context admitted")
	}
	path := filepath.Join(spec.Directory, "oversized-secret")
	if os.WriteFile(path, []byte(strings.Repeat("x", (64<<10)+1)), 0o600) != nil {
		t.Fatal("private secret fixture unavailable")
	}
	spec.Environment = append(spec.Environment, Environment{Name: "OPS_PROCESS_SECRET", SecretFile: path})
	result, err = Run(context.Background(), spec)
	if err == nil || result.Started {
		t.Fatal("oversized private binding admitted")
	}
}

func assertCaptureStable(t *testing.T, spec Spec) {
	t.Helper()
	path := filepath.Join(spec.CaptureDirectory, "fixture-000.stdout")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private capture unavailable")
	}
	time.Sleep(50 * time.Millisecond)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("pipe writer remained active after bounded return")
	}
}
