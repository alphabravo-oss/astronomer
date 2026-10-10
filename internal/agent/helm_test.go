package agent

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"helm.sh/helm/v4/pkg/cli"
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

func TestHelmOperationRejectsArithmeticOverflowWithoutChangingValidTimeouts(t *testing.T) {
	maximumDuration := int64(1<<63 - 1)
	maximum := int64(time.Duration(maximumDuration) / time.Second)
	if strconv.IntSize == 64 {
		ctx, cancel, err := helmOperationContext(context.Background(), int(maximum))
		if err != nil {
			t.Fatal("largest representable timeout rejected")
		}
		defer cancel()
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("valid timeout lost its finite owner")
		}
		for _, seconds := range []int{int(maximum + 1), int(maximumDuration)} {
			if helmReadyTimeout(seconds) != 0 {
				t.Fatal("overflow wrapped to an admitted positive timeout")
			}
			if _, _, err := helmOperationContext(context.Background(), seconds); err == nil {
				t.Fatal("overflow operation admitted")
			}
		}
	}
	parent, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	ctx, cancel, err := helmOperationContext(parent, 300)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	want, _ := parent.Deadline()
	actual, _ := ctx.Deadline()
	if !actual.Equal(want) {
		t.Fatal("earlier caller deadline extended")
	}
}
