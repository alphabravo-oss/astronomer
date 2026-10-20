//go:build linux

package upgradecli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

func TestOperatorChartRouteBindsAuthenticatedArchiveAndExactHelmVersion(t *testing.T) {
	fixture := newLocalChartCryptoFixture(t, false)
	commands, helm := commandFixture(t)
	commands.verified[helm] = struct{}{}
	plan := upgrade.Plan{Target: fixture.chart.metadata.target, Previous: "v1.2.0", Mode: upgrade.Preview, Identity: upgrade.Identity{Context: "owned", APIServer: "https://127.0.0.1:6443", Namespace: "astronomer", NamespaceUID: "ns-original", Release: "astronomer", ReleaseUID: "release-original"}, Timeout: time.Minute, CleanupTimeout: time.Second}
	calls := 0
	commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		calls++
		if len(spec.Args) != 6 || spec.Args[0] != "pull" || spec.Args[1] != "oci://private.example.test/charts/astronomer" || spec.Args[2] != "--version" || spec.Args[3] != strings.TrimPrefix(plan.Target, "v") || spec.Args[4] != "--destination" {
			t.Fatal("operator chart route/version argv changed")
		}
		if err := os.WriteFile(filepath.Join(spec.Args[5], "astronomer-"+spec.Args[3]+".tgz"), fixture.archive, 0644); err != nil {
			t.Fatal(err)
		}
		return captureFixture(t, spec, nil), nil
	}
	retained, err := fixture.chart.captureOperatorRoute(context.Background(), commands, helm, plan, "oci://private.example.test/charts/astronomer")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(retained)
	if err != nil || !bytes.Equal(raw, fixture.archive) || calls != 1 {
		t.Fatal("signed routed archive not retained")
	}
	info, err := os.Lstat(retained)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("routed private input lost mode")
	}
}
func TestOperatorChartRouteRejectsDifferentBytesExtraOutputsAndCanceledPublication(t *testing.T) {
	for _, mode := range []string{"different", "extra", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newLocalChartCryptoFixture(t, false)
			commands, helm := commandFixture(t)
			commands.verified[helm] = struct{}{}
			plan := upgrade.Plan{Target: fixture.chart.metadata.target, Previous: "v1.2.0", Mode: upgrade.Preview, Identity: upgrade.Identity{Context: "owned", APIServer: "https://127.0.0.1:6443", Namespace: "astronomer", NamespaceUID: "ns-original", Release: "astronomer", ReleaseUID: "release-original"}, Timeout: time.Minute, CleanupTimeout: time.Second}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
				raw := fixture.archive
				if mode == "different" {
					raw = append(append([]byte(nil), raw...), 0)
				}
				if err := os.WriteFile(filepath.Join(spec.Args[5], "astronomer-"+spec.Args[3]+".tgz"), raw, 0644); err != nil {
					t.Fatal(err)
				}
				if mode == "extra" {
					if err := os.WriteFile(filepath.Join(spec.Args[5], "unexpected.tgz"), []byte("extra"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				result := captureFixture(t, spec, nil)
				if mode == "cancel" {
					cancel()
				}
				return result, nil
			}
			name, err := fixture.chart.captureOperatorRoute(ctx, commands, helm, plan, "oci://private.example.test/charts/astronomer")
			if err == nil || name != "" {
				t.Fatal("unsafe operator route published")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost")
			}
			if _, err := os.Lstat(filepath.Join(commands.options.CaptureDirectory, "operator-verified-chart.tgz")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed route left published verified input")
			}
		})
	}
}
