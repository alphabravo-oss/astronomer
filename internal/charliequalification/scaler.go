package charliequalification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type AgentScaler interface {
	Replicas(context.Context) (int, error)
	Scale(context.Context, int) error
	WaitReady(context.Context, int) error
}

type KubectlScaler struct {
	binary     string
	kubeconfig string
	namespace  string
	name       string
}

var kubernetesNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)

func NewKubectlScaler(binary, kubeconfig, namespace, name string) (*KubectlScaler, error) {
	binary = strings.TrimSpace(binary)
	kubeconfig = strings.TrimSpace(kubeconfig)
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if binary == "" {
		binary = "kubectl"
	}
	if kubeconfig == "" || !kubernetesNamePattern.MatchString(namespace) || !kubernetesNamePattern.MatchString(name) {
		return nil, errors.New("kubectl scaler requires a kubeconfig and valid namespace and StatefulSet name")
	}
	return &KubectlScaler{binary: binary, kubeconfig: kubeconfig, namespace: namespace, name: name}, nil
}

func (s *KubectlScaler) Replicas(ctx context.Context) (int, error) {
	var state struct {
		Spec struct {
			Replicas int `json:"replicas"`
		} `json:"spec"`
	}
	if err := s.runJSON(ctx, &state, "get", "statefulset", s.name, "-o", "json"); err != nil {
		return 0, err
	}
	if state.Spec.Replicas < 0 || state.Spec.Replicas > 20 {
		return 0, errors.New("agent replica count is outside the qualification bound")
	}
	return state.Spec.Replicas, nil
}

func (s *KubectlScaler) Scale(ctx context.Context, replicas int) error {
	if replicas < 0 || replicas > 20 {
		return errors.New("agent replica count is outside the qualification bound")
	}
	return s.run(ctx, "scale", "statefulset", s.name, fmt.Sprintf("--replicas=%d", replicas))
}

func (s *KubectlScaler) WaitReady(ctx context.Context, replicas int) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var state struct {
			Spec struct {
				Replicas int `json:"replicas"`
			} `json:"spec"`
			Status struct {
				Ready int `json:"readyReplicas"`
			} `json:"status"`
		}
		if err := s.runJSON(ctx, &state, "get", "statefulset", s.name, "-o", "json"); err == nil && state.Spec.Replicas == replicas && state.Status.Ready == replicas {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("timed out waiting for Charlie agent replicas")
		case <-ticker.C:
		}
	}
}

func (s *KubectlScaler) runJSON(ctx context.Context, destination any, args ...string) error {
	var output boundedBuffer
	output.maximum = 1 << 20
	command := s.command(ctx, args...)
	command.Stdout = &output
	if err := command.Run(); err != nil || output.exceeded || json.Unmarshal(output.Bytes(), destination) != nil {
		return errors.New("bounded kubectl status read failed")
	}
	return nil
}

type boundedBuffer struct {
	bytes.Buffer
	maximum  int
	exceeded bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	remaining := b.maximum - b.Len()
	if remaining <= 0 {
		b.exceeded = true
		return len(value), nil
	}
	if len(value) > remaining {
		b.exceeded = true
		_, _ = b.Buffer.Write(value[:remaining])
		return len(value), nil
	}
	return b.Buffer.Write(value)
}

func (s *KubectlScaler) run(ctx context.Context, args ...string) error {
	if err := s.command(ctx, args...).Run(); err != nil {
		return errors.New("kubectl mutation failed")
	}
	return nil
}

func (s *KubectlScaler) command(ctx context.Context, args ...string) *exec.Cmd {
	base := []string{"--kubeconfig", s.kubeconfig, "--namespace", s.namespace}
	return exec.CommandContext(ctx, s.binary, append(base, args...)...)
}
