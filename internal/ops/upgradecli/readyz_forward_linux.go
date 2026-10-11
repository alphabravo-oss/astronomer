//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

const readyzServiceTemplate = `{{range .items}}{{$service := .}}{{range .spec.ports}}{{if eq .port 8000}}{{$service.metadata.name}}{{"\t"}}{{$service.metadata.uid}}{{"\t"}}{{$service.metadata.namespace}}{{"\n"}}{{end}}{{end}}{{end}}`

// ReadyzObservation retains the legacy server-Service /readyz check. It owns a
// private joined kubectl process bound to the initially captured tool/context.
// It never uses a caller health URL, ambient proxy, public listener or detached
// goroutine. Its loopback client is limited to that exact child-created tunnel.
// Calls must be serialized by the complete release Driver.
type ReadyzObservation struct {
	release *HelmRelease
	port    uint16
}

func NewReadyzObservation(release *HelmRelease) (*ReadyzObservation, error) {
	return NewReadyzObservationWithPort(release, 0)
}

// A zero port preserves the existing ephemeral adapter; the legacy executable
// injects its independently captured HEALTH_PORT. Occupied ports fail, never
// silently change the requested endpoint.
func NewReadyzObservationWithPort(release *HelmRelease, port uint16) (*ReadyzObservation, error) {
	if release == nil || release.fence == nil || release.fence.commands == nil || release.plan.Validate() != nil {
		return nil, errors.New("one bound release command owner required")
	}
	return &ReadyzObservation{release: release, port: port}, nil
}

func (o *ReadyzObservation) Check(ctx context.Context) error {
	if ctx == nil || o == nil || o.release == nil || o.release.fence == nil || o.release.fence.commands == nil {
		return errors.New("explicit owned readiness context required")
	}
	r := o.release
	f := r.fence
	id := r.plan.Identity
	if err := f.Fence(ctx, id, r.plan.Replace); err != nil {
		return err
	}
	args := []string{"--kubeconfig", f.options.Kubeconfig, "--context", id.Context, "get", "services", "--namespace", id.Namespace, "--selector", "app.kubernetes.io/instance=" + id.Release + ",app.kubernetes.io/name=astronomer,app.kubernetes.io/component=server", "--output", "go-template=" + readyzServiceTemplate}
	raw, err := f.execute(ctx, f.kubectl, args)
	if err != nil {
		return err
	}
	service, uid, err := readyzService(raw, id.Namespace)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:"+strconv.FormatUint(uint64(o.port), 10))
	if err != nil {
		return errors.New("reserve owned readiness loopback port")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return errors.New("release readiness port reservation")
	}
	if err := f.Fence(ctx, id, r.plan.Replace); err != nil {
		return err
	}
	args = []string{"--kubeconfig", f.options.Kubeconfig, "--context", id.Context, "port-forward", "--namespace", id.Namespace, "--address=127.0.0.1", "service/" + service, strconv.Itoa(port) + ":8000"}
	if err := o.forward(ctx, args, port); err != nil {
		return err
	}
	final, err := f.execute(ctx, f.kubectl, []string{"--kubeconfig", f.options.Kubeconfig, "--context", id.Context, "get", "services", "--namespace", id.Namespace, "--selector", "app.kubernetes.io/instance=" + id.Release + ",app.kubernetes.io/name=astronomer,app.kubernetes.io/component=server", "--output", "go-template=" + readyzServiceTemplate})
	if err != nil {
		return err
	}
	lastService, lastUID, err := readyzService(final, id.Namespace)
	if err != nil || lastService != service || lastUID != uid {
		return errors.New("owned server Service changed during readiness")
	}
	return f.Fence(ctx, id, r.plan.Replace)
}

func readyzService(raw []byte, namespace string) (string, string, error) {
	rows, err := readinessRows(raw)
	if err != nil || len(rows) != 1 {
		return "", "", errors.New("exactly one owned server Service on port8000 required")
	}
	fields := strings.Split(rows[0], "\t")
	if len(fields) != 3 || !kubeName.MatchString(fields[0]) || fields[1] == "" || strings.ContainsAny(fields[1], "\t\r\n ") || fields[2] != namespace {
		return "", "", errors.New("owned readiness Service identity differs")
	}
	return fields[0], fields[1], nil
}

func (o *ReadyzObservation) forward(ctx context.Context, args []string, port int) (err error) {
	c := o.release.fence.commands
	tool := o.release.fence.kubectl
	identity, err := toolIdentity(tool)
	if err != nil {
		return err
	}
	c.mu.Lock()
	_, verified := c.verified[tool]
	if !verified || c.sequence >= 1000 {
		c.mu.Unlock()
		return errors.New("actual verified tool admission and command budget required")
	}
	c.sequence++
	sequence := c.sequence
	c.mu.Unlock()
	bounded, stop := context.WithTimeout(ctx, 2*time.Minute)
	defer stop()
	childContext, cancel := context.WithCancel(bounded)
	defer cancel()
	spec := process.Spec{Executable: tool.Path, Args: append([]string(nil), args...), Environment: append([]process.Environment(nil), c.options.Environment...), Directory: c.options.Directory, CaptureDirectory: c.options.CaptureDirectory, ID: string(tool.Kind), Sequence: sequence, Timeout: c.options.Timeout, TerminationGrace: c.options.TerminationGrace, MaximumCaptureBytes: c.options.MaximumCaptureBytes}
	type completion struct {
		result process.Result
		err    error
	}
	done := make(chan completion, 1)
	go func() {
		result, childErr := c.run(childContext, spec)
		done <- completion{result: result, err: childErr}
	}()
	var completed *completion
	defer func() {
		// process.Run owns its timeout and bounded TERM/KILL/pipe joins. Never
		// return from success, cancellation, probe failure or setup failure before
		// this invocation actually returns its owned result.
		cancel()
		if completed == nil {
			value := <-done
			completed = &value
		}
		if !completed.result.LeaderReaped || completed.result.Kind != process.Cancelled {
			err = errors.Join(err, errors.New("owned readiness forwarder did not acknowledge cancellation and join"))
		}
		after, bindingErr := toolIdentity(tool)
		if bindingErr != nil || !os.SameFile(identity, after) {
			err = errors.Join(err, errors.New("readiness forwarder tool changed"))
		}
	}()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: 2 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	banner := fmt.Sprintf("Forwarding from 127.0.0.1:%d -> 8000", port)
	captureName := fmt.Sprintf("%s-%03d.stdout", spec.ID, sequence)
	for {
		select {
		case value := <-done:
			completed = &value
			return errors.New("owned readiness forwarder exited before health observation")
		case <-bounded.Done():
			return bounded.Err()
		case <-ticker.C:
		}
		ready, err := forwardBanner(bounded, c.options.CaptureDirectory, captureName, banner)
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		request, err := http.NewRequestWithContext(bounded, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/readyz", port), nil)
		if err != nil {
			return errors.New("construct exact owned readiness request")
		}
		response, requestErr := client.Do(request)
		if requestErr != nil {
			if bounded.Err() != nil {
				return bounded.Err()
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || len(body) > 64<<10 {
			return errors.New("owned readiness response body is truncated or exceeds budget")
		}
		if response.StatusCode != http.StatusOK {
			continue
		}
		select {
		case value := <-done:
			completed = &value
			return errors.New("readiness forwarder exited before acknowledged health")
		default:
		}
		if _, err := writeBackupBytes(bounded, c, fmt.Sprintf("readyz-%03d.json", sequence), body, 64<<10); err != nil {
			return err
		}
		return bounded.Err()
	}
}

func forwardBanner(ctx context.Context, directory, name, expected string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if filepath.Base(name) != name {
		return false, errors.New("exact private forwarder capture name required")
	}
	parent, err := os.Lstat(directory)
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0o700 || parent.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return false, errors.New("private forwarder capture directory differs")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return false, errors.New("open private forwarder capture root")
	}
	defer func(close func() error) { _ = close() }(root.Close)
	openedParent, err := root.Stat(".")
	if err != nil || !os.SameFile(parent, openedParent) {
		return false, errors.New("private forwarder capture root replaced")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("open private forwarder progress")
	}
	defer func(close func() error) { _ = close() }(file.Close)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) || info.Sys().(*syscall.Stat_t).Nlink != 1 || info.Size() > 64<<10 {
		return false, errors.New("forwarder progress identity or byte bound differs")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return false, errors.New("read bounded forwarder progress")
	}
	after, err := root.Stat(name)
	if err != nil || !os.SameFile(info, after) || !after.Mode().IsRegular() || after.Mode().Perm() != 0o600 || after.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) || after.Sys().(*syscall.Stat_t).Nlink != 1 {
		return false, errors.New("forwarder capture replaced during read")
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == expected {
			return true, ctx.Err()
		}
	}
	return false, ctx.Err()
}
