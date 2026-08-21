package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

func TestHandleCLIVersionExitsBeforeServerInitialization(t *testing.T) {
	for _, arg := range []string{"--version", "version"} {
		t.Run(arg, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			handled, exitCode := handleCLI([]string{arg}, &stdout, &stderr)

			if !handled || exitCode != 0 {
				t.Fatalf("handleCLI(%q) = handled %v, exit %d; want true, 0", arg, handled, exitCode)
			}
			for _, value := range []string{version.Version, version.GitCommit, version.BuildDate} {
				if !strings.Contains(stdout.String(), value) {
					t.Errorf("version output %q does not contain %q", stdout.String(), value)
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q; want empty", stderr.String())
			}
		})
	}
}

func TestHandleCLIAllowsNormalStartupWithoutArguments(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	handled, exitCode := handleCLI(nil, &stdout, &stderr)

	if handled || exitCode != 0 {
		t.Fatalf("handleCLI(nil) = handled %v, exit %d; want false, 0", handled, exitCode)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestHandleCLIGrafanaProxyRequiresEnv(t *testing.T) {
	t.Setenv("GRAFANA_UPSTREAM", "")
	t.Setenv("ASTRONOMER_URL", "")
	t.Setenv("GRAFANA_HOST", "")
	t.Setenv("GRAFANA_PROXY_KEY", "")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	handled, exitCode := handleCLI([]string{"grafana-proxy"}, &stdout, &stderr)
	if !handled || exitCode == 0 {
		t.Fatalf("handleCLI(grafana-proxy) = handled %v, exit %d; want true, nonzero", handled, exitCode)
	}
	if !strings.Contains(stderr.String(), "grafana-proxy") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestHandleCLILokiAuthRequiresEnv(t *testing.T) {
	t.Setenv("LOKI_UPSTREAM", "")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	handled, exitCode := handleCLI([]string{"loki-auth"}, &stdout, &stderr)
	if !handled || exitCode == 0 {
		t.Fatalf("handleCLI(loki-auth) = handled %v, exit %d; want true, nonzero", handled, exitCode)
	}
	if !strings.Contains(stderr.String(), "loki-auth") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestHandleCLIRejectsUnknownArgumentsBeforeStartup(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	handled, exitCode := handleCLI([]string{"unexpected"}, &stdout, &stderr)

	if !handled || exitCode != 2 {
		t.Fatalf("handleCLI(unknown) = handled %v, exit %d; want true, 2", handled, exitCode)
	}
	if !strings.Contains(stderr.String(), "unknown argument") {
		t.Fatalf("stderr = %q; want unknown argument error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q; want empty", stdout.String())
	}
}
