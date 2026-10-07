package agent

import (
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"helm.sh/helm/v3/pkg/cli"
)

func TestHelmSettingsForNamespaceIsRequestLocal(t *testing.T) {
	t.Parallel()

	base := cli.New()
	base.SetNamespace("astronomer-system")
	request := helmSettingsForNamespace(base, "cis-operator-system")
	if request.Namespace() != "cis-operator-system" {
		t.Fatalf("request namespace = %q", request.Namespace())
	}
	if base.Namespace() != "astronomer-system" {
		t.Fatalf("shared settings namespace mutated to %q", base.Namespace())
	}
}

func TestHelmReadyTimeout(t *testing.T) {
	t.Parallel()

	// Caller-provided seconds win.
	if got := helmReadyTimeout(30); got != 30*time.Second {
		t.Fatalf("helmReadyTimeout(30) = %v, want 30s", got)
	}

	// Zero/negative falls back to the helm CLI default so that install and
	// upgrade actually wait for workloads to become Ready (Wait=true) rather
	// than timing out immediately and reporting "deployed" prematurely.
	if got := helmReadyTimeout(0); got != defaultHelmReadyTimeout {
		t.Fatalf("helmReadyTimeout(0) = %v, want %v", got, defaultHelmReadyTimeout)
	}
	if got := helmReadyTimeout(-5); got != defaultHelmReadyTimeout {
		t.Fatalf("helmReadyTimeout(-5) = %v, want %v", got, defaultHelmReadyTimeout)
	}
}

func TestHelmUpgradeUsesStoredChartWhenCoordinatesOmitted(t *testing.T) {
	t.Parallel()
	if !helmUpgradeUsesStoredChart(&protocol.HelmRequestPayload{ReleaseName: "astronomer", ReuseValues: true}) {
		t.Fatal("overlay upgrade with no chart coordinates should reuse the last release chart")
	}
	if helmUpgradeUsesStoredChart(&protocol.HelmRequestPayload{ChartName: "astronomer"}) {
		t.Fatal("explicit chart name should locate a chart")
	}
	if helmUpgradeUsesStoredChart(nil) {
		t.Fatal("nil request")
	}
}
