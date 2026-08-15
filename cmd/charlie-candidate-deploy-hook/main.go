package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charliequalification"
)

const deployPrefix = "ASTRONOMER_CHARLIE_DEPLOY_"

func main() {
	if run() != nil {
		charliequalification.LogHookLifecycle(nil, charliequalification.HookStoppedWithFailure)
		os.Exit(1)
	}
}

func run() error {
	if err := charliequalification.ValidateAcknowledgement(os.Getenv(deployPrefix + "EFFECTS_ACK")); err != nil {
		return err
	}
	address := strings.TrimSpace(os.Getenv(deployPrefix + "LISTEN"))
	if address == "" {
		address = "127.0.0.1:9444"
	}
	token, err := readPrivateSecret(os.Getenv(deployPrefix + "HOOK_TOKEN_FILE"))
	if err != nil || len(token) < 32 || len(token) > 512 {
		return errors.New("strong private hook token is required")
	}
	command := strings.TrimSpace(os.Getenv(deployPrefix + "COMMAND"))
	deployer, err := charliequalification.NewCommandCandidateDeployer(command, 14*time.Minute)
	if err != nil {
		return err
	}
	hook, err := charliequalification.NewCandidateDeployHook(token, deployer)
	if err != nil {
		return err
	}
	server, err := charliequalification.NewHTTPServer(address, hook.Handler())
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	charliequalification.LogHookLifecycle(nil, charliequalification.HookStarted)
	select {
	case err = <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func readPrivateSecret(path string) (string, error) {
	path = strings.TrimSpace(path)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("secret file must be owner-only")
	}
	contents, err := os.ReadFile(path)
	if err != nil || len(contents) > 16<<10 {
		return "", fmt.Errorf("cannot read bounded secret file")
	}
	value := strings.TrimSpace(string(contents))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("secret file must contain one line")
	}
	return value, nil
}
