package helmruntime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"helm.sh/helm/v4/pkg/action"
	chartcommon "helm.sh/helm/v4/pkg/chart/common"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/kube"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	"helm.sh/helm/v4/pkg/release"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/client-go/rest"
)

func TestHelmSDKDirectCanceledControlReturnsBeforeHookWorker(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	stage, cfg := joinedSDKFixture(t, owner, "hook")
	install := joinedInstall(cfg, owner)
	stage.expectHookCleanup = true
	controlChart := joinedChart("1.0.0", "pre-install")
	controlChart.Templates[0].Data = append(controlChart.Templates[0].Data, []byte("    helm.sh/hook-delete-policy: hook-failed\n")...)
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := install.RunWithContext(owner, controlChart, nil)
		result <- err
	}()
	joinSDKCleanup(t, cancel, stage, done)
	waitJoinedSignal(t, stage.entered, "SDK hook never entered owned transfer")
	waitJoinedSignal(t, stage.networkEntered, "owned hook response never established")
	cancel()
	waitJoinedSignal(t, stage.canceled, "SDK hook did not observe owned body cancellation")
	waitJoinedSignal(t, stage.networkStopped, "owned server did not observe hook cancellation")
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("upstream early control return lost cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream direct control characterization did not return")
	}
	select {
	case <-stage.exited:
		t.Fatal("held worker exited before its explicit completion gate")
	default:
	}
	stage.release()
	waitJoinedSignal(t, stage.exited, "direct-control worker was not explicitly drained")
	waitJoinedSignal(t, stage.hookCleanupDone, "direct-control failed-hook cleanup was not drained")
	// Official4.3 records hook-start and outer failRelease, while failed-hook
	// deletion has no third recordRelease. This is a source-bound exact count.
	if stage.updateCount.Load() != 2 || stage.hookCleanupCount.Load() != 1 {
		t.Fatal("direct-control hook storage/deletion sequence changed")
	}
	// The upstream API deliberately returns early. This negative control is
	// not product behavior or a claim that a remote mutation was rolled back.
}

func TestHelmSDKJoinedInstallWaitPreservesPendingHistoryUntilWorkerStops(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	stage, cfg := joinedSDKFixture(t, owner, "wait")
	install := joinedInstall(cfg, owner)
	joinedSDKLifecycle(t, owner, cancel, stage, cfg, 1, releasecommon.StatusPendingInstall, func(control context.Context) (release.Releaser, error) {
		return install.RunWithContext(control, joinedChart("1.0.0", ""), map[string]any{"fixture": "owned value"})
	})
}

func TestHelmSDKJoinedUpgradeHookPreservesOldAndPendingRevisionUntilWorkerStops(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	stage, cfg := joinedSDKFixture(t, owner, "hook")
	prior := &releasev1.Release{Name: "fixture", Namespace: "owned", Version: 1, Chart: joinedChart("1.0.0", ""), Info: &releasev1.Info{Status: releasecommon.StatusDeployed, Description: "retained prior revision"}}
	if err := cfg.Releases.Create(prior); err != nil {
		t.Fatal(err)
	}
	upgrade := action.NewUpgrade(cfg)
	upgrade.Namespace = "owned"
	upgrade.WaitStrategy = kube.LegacyStrategy
	upgrade.WaitOptions = []kube.WaitOption{kube.WithWaitContext(owner)}
	upgrade.ServerSideApply = "false"
	upgrade.Timeout = 2 * time.Second
	joinedSDKLifecycle(t, owner, cancel, stage, cfg, 2, releasecommon.StatusPendingUpgrade, func(control context.Context) (release.Releaser, error) {
		return upgrade.RunWithContext(control, "fixture", joinedChart("2.0.0", "pre-upgrade"), map[string]any{"fixture": "new owned value"})
	})
	value, err := cfg.Releases.Get("fixture", 1)
	if err != nil {
		t.Fatal(err)
	}
	old, err := ReleaseV1(value)
	if err != nil || old.Info.Status != releasecommon.StatusDeployed || old.Info.Description != prior.Info.Description {
		t.Fatal("failed upgrade changed the retained prior revision")
	}
}

func TestHelmJoinedActionRejectsExpiredOrMissingOwnerWithoutStartingSDK(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	run := func(context.Context) (release.Releaser, error) { called = true; return nil, nil }
	if _, err := RunJoined(owner, run); !errors.Is(err, context.Canceled) || called {
		t.Fatal("expired action started the SDK")
	}
	if _, err := RunJoined(nil, run); err == nil || called {
		t.Fatal("missing owner started the SDK")
	}
	if _, err := RunJoined(context.Background(), nil); err == nil {
		t.Fatal("missing SDK runner admitted")
	}
}

func joinedSDKLifecycle(t *testing.T, owner context.Context, cancel context.CancelFunc, stage *heldSDKStage, cfg *action.Configuration, revision int, pending releasecommon.Status, run func(context.Context) (release.Releaser, error)) {
	t.Helper()
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() { defer close(done); _, err := RunJoined(owner, run); result <- err }()
	joinSDKCleanup(t, cancel, stage, done)
	waitJoinedSignal(t, stage.entered, "SDK stage did not start")
	waitJoinedSignal(t, stage.networkEntered, "owned wait/hook response never established")
	value, err := cfg.Releases.Get("fixture", revision)
	if err != nil {
		t.Fatal(err)
	}
	current, err := ReleaseV1(value)
	if err != nil || current.Version != revision || current.Info.Status != pending {
		t.Fatal("pending durable revision was not recorded before the worker")
	}
	cancel()
	waitJoinedSignal(t, stage.canceled, "owned body did not cancel")
	waitJoinedSignal(t, stage.networkStopped, "owned server did not observe body cancellation")
	select {
	case <-result:
		t.Fatal("joined adapter returned while SDK worker was still held")
	default:
	}
	stage.release()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("joined action lost cancellation identity")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("joined action did not drain")
	}
	waitJoinedSignal(t, stage.exited, "SDK wait/hook did not finish before return")
	value, err = cfg.Releases.Get("fixture", revision)
	if err != nil {
		t.Fatal(err)
	}
	current, err = ReleaseV1(value)
	if err != nil || current.Info.Status != releasecommon.StatusFailed || current.Version != revision {
		t.Fatal("canceled worker reported success or lost revision history")
	}
	if stage.kind == "hook" && (len(current.Hooks) != 1 || current.Hooks[0].LastRun.Phase != releasev1.HookPhaseFailed) {
		t.Fatal("hook failure was not recorded before joined return")
	}
}

type heldSDKStage struct {
	kind                      string
	client                    *http.Client
	url                       string
	owner                     context.Context
	entered, canceled, exited chan struct{}
	allowExit                 chan struct{}
	once                      sync.Once
	networkStopped            <-chan struct{}
	networkEntered            <-chan struct{}
	expectHookCleanup         bool
	hookCleanupDone           chan struct{}
	hookCleanupCount          atomic.Int32
	updateCount               atomic.Int32
	updated                   chan struct{}
}

func (stage *heldSDKStage) release() { stage.once.Do(func() { close(stage.allowExit) }) }

func (stage *heldSDKStage) hold() error {
	close(stage.entered)
	response, err := stage.client.Get(stage.url) // Background SDK-style request.
	if err == nil {
		_, err = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	close(stage.canceled)
	<-stage.allowExit
	close(stage.exited)
	return errors.Join(err, stage.owner.Err())
}

type heldSDKKube struct {
	*kubefake.PrintingKubeClient
	stage *heldSDKStage
}

func (client *heldSDKKube) GetWaiter(strategy kube.WaitStrategy) (kube.Waiter, error) {
	return client.GetWaiterWithOptions(strategy)
}

func (client *heldSDKKube) GetWaiterWithOptions(_ kube.WaitStrategy, _ ...kube.WaitOption) (kube.Waiter, error) {
	return &heldSDKWaiter{PrintingKubeWaiter: &kubefake.PrintingKubeWaiter{Out: io.Discard, LogOutput: io.Discard}, stage: client.stage}, nil
}

type heldSDKWaiter struct {
	*kubefake.PrintingKubeWaiter
	stage *heldSDKStage
}

func (waiter *heldSDKWaiter) Wait(resources kube.ResourceList, timeout time.Duration) error {
	if waiter.stage.kind == "wait" {
		return waiter.stage.hold()
	}
	return waiter.PrintingKubeWaiter.Wait(resources, timeout)
}

func (waiter *heldSDKWaiter) WatchUntilReady(resources kube.ResourceList, timeout time.Duration) error {
	if waiter.stage.kind == "hook" {
		return waiter.stage.hold()
	}
	return waiter.PrintingKubeWaiter.WatchUntilReady(resources, timeout)
}

// Only the direct negative control requests hook-failed deletion. This last
// backend callback runs after Helm sets failed-hook phase; its completion marks
// the end of the remaining remote/release mutation callbacks, not an invented
// third storage update or a public SDK goroutine-counter API.
func (waiter *heldSDKWaiter) WaitForDelete(resources kube.ResourceList, timeout time.Duration) error {
	err := waiter.PrintingKubeWaiter.WaitForDelete(resources, timeout)
	if waiter.stage.expectHookCleanup {
		if waiter.stage.hookCleanupCount.Add(1) == 1 {
			close(waiter.stage.hookCleanupDone)
		}
	}
	return err
}

func joinedSDKFixture(t *testing.T, owner context.Context, kind string) (*heldSDKStage, *action.Configuration) {
	t.Helper()
	serverDone := make(chan struct{})
	serverEntered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		w.(http.Flusher).Flush()
		close(serverEntered)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	getter, err := RequestClientGetter(owner, requestGetterFixture{config: &rest.Config{Host: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	restConfig, err := getter.ToRESTConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	stage := &heldSDKStage{kind: kind, client: client, url: server.URL, owner: owner, entered: make(chan struct{}), canceled: make(chan struct{}), exited: make(chan struct{}), allowExit: make(chan struct{}), networkStopped: serverDone, networkEntered: serverEntered, hookCleanupDone: make(chan struct{}), updated: make(chan struct{}, 8)}
	t.Cleanup(stage.release)
	memory := driver.NewMemory()
	memory.SetNamespace("owned")
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
	cfg.Capabilities = chartcommon.DefaultCapabilities.Copy()
	cfg.KubeClient = &heldSDKKube{PrintingKubeClient: &kubefake.PrintingKubeClient{Out: io.Discard, LogOutput: io.Discard}, stage: stage}
	cfg.Releases = storage.Init(&joinedStorageObserver{Driver: memory, stage: stage})
	return stage, cfg
}

func joinedInstall(cfg *action.Configuration, owner context.Context) *action.Install {
	install := action.NewInstall(cfg)
	install.Namespace, install.ReleaseName = "owned", "fixture"
	install.WaitStrategy = kube.LegacyStrategy
	install.WaitOptions = []kube.WaitOption{kube.WithWaitContext(owner)}
	install.ServerSideApply = false
	install.Timeout = 2 * time.Second
	return install
}

func joinedChart(version, hook string) *chart.Chart {
	value := &chart.Chart{Metadata: &chart.Metadata{APIVersion: "v2", Name: "fixture", Version: version}}
	if hook != "" {
		value.Templates = []*chartcommon.File{{Name: "templates/hook.yaml", Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: fixture-hook\n  annotations:\n    helm.sh/hook: " + hook + "\n")}}
	}
	return value
}

func waitJoinedSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}

type joinedStorageObserver struct {
	driver.Driver
	stage *heldSDKStage
}

func (observer *joinedStorageObserver) Update(key string, value release.Releaser) error {
	err := observer.Driver.Update(key, value)
	observer.stage.updateCount.Add(1)
	select {
	case observer.stage.updated <- struct{}{}:
	default:
	}
	return err
}

func joinSDKCleanup(t *testing.T, cancel context.CancelFunc, stage *heldSDKStage, done <-chan struct{}) {
	t.Helper()
	t.Cleanup(func() {
		cancel()
		stage.release()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("SDK action caller did not drain during failed-fixture cleanup")
		}
		select {
		case <-stage.entered:
			select {
			case <-stage.exited:
			case <-time.After(2 * time.Second):
				t.Error("SDK held callback did not drain during failed-fixture cleanup")
			}
			if stage.expectHookCleanup {
				select {
				case <-stage.hookCleanupDone:
				case <-time.After(2 * time.Second):
					t.Error("SDK failed-hook terminal deletion callback did not drain during cleanup")
				}
			}
		default:
		}
	})
}
