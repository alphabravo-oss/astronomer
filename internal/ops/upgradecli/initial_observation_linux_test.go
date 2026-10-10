//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func initialFixture(t *testing.T) (*InitialObserver, func(context.Context, Tool, []string) ([]byte, error)) {
	t.Helper()
	commands, helm := commandFixture(t)
	kubectl := helm
	kubectl.Kind = Kubectl
	kubectl.Version = "v1.37.1"
	config := filepath.Join(commands.options.Directory, "initial-kubeconfig")
	if err := os.WriteFile(config, []byte("synthetic explicit private configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	selection := LegacyEnvironment{Release: "astronomer", Namespace: "owned", Context: "selected", APIServer: "https://127.0.0.1:6443"}
	observer, err := NewInitialObserver(commands, helm, kubectl, selection, config)
	if err != nil {
		t.Fatal(err)
	}
	read := func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(args) < 5 || args[0] != "--kubeconfig" || args[1] != config || args[3] != selection.Context {
			t.Fatal("initial command lost explicit private/context binding")
		}
		if tool.Kind == Helm {
			if strings.Join(args[4:9], " ") == "get values astronomer --namespace owned" {
				if strings.Join(args[9:], " ") != "--all --output json" {
					t.Fatal("initial backend values lost canonical all/json scope")
				}
				return []byte(`{"postgres":{"mode":"","bundled":{"enabled":true}},"other":{"privateValue":"synthetic never exported"}}`), nil
			}
			if strings.Join(args[4:9], " ") != "get metadata astronomer --namespace owned" {
				t.Fatal("initial read is not exact Helm metadata")
			}
			return []byte(`{"name":"astronomer","namespace":"owned","revision":2,"status":"deployed","chart":"astronomer","version":"1.1.0","appVersion":"1.1.0","deployedAt":"2026-10-01T00:00:00Z","annotations":{"owner":"fixture"},"labels":{"name":"astronomer"},"dependencies":[],"applyMethod":"csa"}`), nil
		}
		switch args[4] {
		case "config":
			return []byte(selection.APIServer), nil
		case "auth":
			return []byte("yes\n"), nil
		case "exec":
			if args[len(args)-1] != databaseStateScript {
				t.Fatal("initial observation changed canonical credential-free SQL seam")
			}
			return []byte("owned_database|18|1|78|f\n"), nil
		case "get":
			switch args[5] {
			case "namespace":
				return []byte("namespace-uid\towned\tlive"), nil
			case "secret":
				if args[6] != "sh.helm.release.v1.astronomer.v2" {
					t.Fatal("initial revision differs")
				}
				return []byte("revision-uid\tsh.helm.release.v1.astronomer.v2\towned\thelm.sh/release.v1\thelm\tastronomer\t2\tlive"), nil
			case "pods":
				return []byte("owned-postgres\tpod-uid\towned\tastronomer\tpostgres\tlive\n"), nil
			}
		}
		t.Fatal("initial observer attempted unknown or mutating command")
		return nil, errors.New("unreachable")
	}
	observer.execute = read
	return observer, read
}

func TestInitialObserverBindsActualOriginalIdentitiesAndSchema(t *testing.T) {
	observer, _ := initialFixture(t)
	got, err := observer.Observe(context.Background(), "v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.NamespaceUID != "namespace-uid" || got.Identity.ReleaseUID != "revision-uid" || got.Revision != 2 || got.CurrentVersion != "v1.1.0" || got.DatabasePodUID != "pod-uid" || got.Database != "owned_database" || got.DatabaseState.Major != 18 || got.DatabaseState.Version != 78 {
		t.Fatal("binding was not independently observed")
	}
}

func TestInitialObserverRejectsChangedFinalObservationWithoutPartialBinding(t *testing.T) {
	for _, resource := range []string{"namespace", "secret", "pods", "schema"} {
		t.Run(resource, func(t *testing.T) {
			observer, read := initialFixture(t)
			calls := 0
			observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				raw, err := read(ctx, tool, args)
				match := (args[4] == "get" && args[5] == resource) || (resource == "schema" && args[4] == "exec")
				if match {
					calls++
					if calls == 2 {
						raw = []byte(strings.Replace(string(raw), map[string]string{"namespace": "namespace-uid", "secret": "revision-uid", "pods": "pod-uid", "schema": "|78|"}[resource], "changed", 1))
					}
				}
				return raw, err
			}
			got, err := observer.Observe(context.Background(), "v1.2.0")
			if err == nil || got != (InitialInstallation{}) {
				t.Fatal("changed final observation returned usable partial identity")
			}
		})
	}
}

func TestInitialObserverRejectsConfigChangeBetweenReadStages(t *testing.T) {
	observer, read := initialFixture(t)
	calls := 0
	observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		raw, err := read(ctx, tool, args)
		calls++
		if calls == 1 {
			if err := os.WriteFile(observer.kubeconfig, []byte("reconfigured private input"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return raw, err
	}
	got, err := observer.Observe(context.Background(), "v1.2.0")
	if err == nil || calls != 1 || got != (InitialInstallation{}) {
		t.Fatal("changed initial configuration was re-admitted")
	}
}

func TestInitialObserverRejectsFailedReadAndCanceledOutput(t *testing.T) {
	for _, cause := range []string{"read", "cancel"} {
		t.Run(cause, func(t *testing.T) {
			observer, read := initialFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				calls++
				raw, err := read(ctx, tool, args)
				if cause == "read" {
					return raw, errors.New("synthetic read failure")
				}
				cancel()
				return raw, err
			}
			got, err := observer.Observe(ctx, "v1.2.0")
			if err == nil || calls != 1 || got != (InitialInstallation{}) {
				t.Fatal("failed/canceled observation published binding")
			}
		})
	}
}

func TestInitialObserverRejectsRelativeConfigAndUnsafeSelectionBeforeCommands(t *testing.T) {
	observer, _ := initialFixture(t)
	for _, kind := range []string{"relative", "context", "endpoint"} {
		t.Run(kind, func(t *testing.T) {
			selection := observer.selection
			config := observer.kubeconfig
			switch kind {
			case "relative":
				config = "relative-private-input"
			case "context":
				selection.Context = "unsafe\ncontext"
			case "endpoint":
				selection.APIServer = "https://operator:private@example.test"
			}
			if _, err := NewInitialObserver(observer.commands, observer.helm, observer.kubectl, selection, config); err == nil {
				t.Fatal("unsafe selection admitted")
			}
		})
	}
}

func TestInitialObserverRejectsUnsupportedReleaseAndNonforwardTarget(t *testing.T) {
	for _, change := range []string{"status", "version", "unknown", "target"} {
		t.Run(change, func(t *testing.T) {
			observer, read := initialFixture(t)
			target := "v1.2.0"
			observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				raw, err := read(ctx, tool, args)
				if tool.Kind == Helm {
					switch change {
					case "status":
						raw = []byte(strings.Replace(string(raw), `"deployed"`, `"pending-upgrade"`, 1))
					case "version":
						raw = []byte(strings.Replace(string(raw), `"version":"1.1.0"`, `"version":"1.1.0-rc.1"`, 1))
					case "unknown":
						raw = []byte(strings.Replace(string(raw), `"name":`, `"unknown":true,"name":`, 1))
					}
				}
				return raw, err
			}
			if change == "target" {
				target = "v1.1.0"
			}
			got, err := observer.Observe(context.Background(), target)
			if err == nil || got != (InitialInstallation{}) {
				t.Fatal("unsupported original release/target admitted")
			}
		})
	}
}

func TestInitialExternalObservationKeepsConfirmationDistinctFromLiveSchema(t *testing.T) {
	observer, read := initialFixture(t)
	observer.selection.ExternalBackupConfirmed = true
	observer.selection.ExternalSchemaVersion = "78"
	execs := 0
	observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if tool.Kind == Helm && args[5] == "values" {
			return []byte(`{"postgres":{"mode":"external","bundled":{"enabled":false}}}`), nil
		}
		if tool.Kind == Kubectl && args[4] == "get" && args[5] == "pods" {
			return nil, nil
		}
		if tool.Kind == Kubectl && args[4] == "exec" {
			execs++
			t.Fatal("external observation issued bundled SQL")
		}
		return read(ctx, tool, args)
	}
	got, err := observer.Observe(context.Background(), "v1.2.0")
	if err != nil || !got.ExternalDatabase || got.Database != "" || got.DatabasePod != "" || got.DatabaseState.Major != 0 || got.DatabaseState.Version != 78 || execs != 0 {
		t.Fatal("external assertion gained live DB/restore credit", err)
	}
}

func TestInitialExternalObservationRejectsMissingConfirmationAndRecreatedSelection(t *testing.T) {
	for _, kind := range []string{"unconfirmed", "schema", "bundled-arrives", "query-failure", "replace", "decrypt-proof"} {
		t.Run(kind, func(t *testing.T) {
			observer, read := initialFixture(t)
			observer.selection.ExternalBackupConfirmed = true
			observer.selection.ExternalSchemaVersion = "78"
			switch kind {
			case "unconfirmed":
				observer.selection.ExternalBackupConfirmed = false
			case "schema":
				observer.selection.ExternalSchemaVersion = "078"
			case "replace":
				observer.selection.ReplaceDatabase = true
			case "decrypt-proof":
				observer.selection.DecryptProofWebhookID = "synthetic"
			}
			pods := 0
			observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				if tool.Kind == Helm && args[5] == "values" {
					return []byte(`{"postgres":{"mode":"external","bundled":{"enabled":false}}}`), nil
				}
				if tool.Kind == Kubectl && args[4] == "get" && args[5] == "pods" {
					pods++
					if kind == "query-failure" {
						return nil, errors.New("synthetic failed inventory")
					}
					if pods == 1 || kind != "bundled-arrives" {
						return nil, nil
					}
				}
				return read(ctx, tool, args)
			}
			got, err := observer.Observe(context.Background(), "v1.2.0")
			if err == nil || got != (InitialInstallation{}) {
				t.Fatal("incomplete external observation returned authority")
			}
		})
	}
}

func TestInitialObserverNeverInfersExternalFromMissingLegacyPods(t *testing.T) {
	for _, mode := range []string{"bundled", "cloudNativePG", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			observer, read := initialFixture(t)
			observer.selection.ExternalBackupConfirmed = true
			observer.selection.ExternalSchemaVersion = "78"
			execs := 0
			observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				if tool.Kind == Helm && args[5] == "values" {
					return []byte(`{"postgres":{"mode":"` + mode + `","bundled":{"enabled":false}}}`), nil
				}
				if tool.Kind == Kubectl && args[4] == "get" && args[5] == "pods" {
					return nil, nil
				}
				if tool.Kind == Kubectl && args[4] == "exec" {
					execs++
					t.Fatal("unsupported backend reached legacy database command")
				}
				return read(ctx, tool, args)
			}
			got, err := observer.Observe(context.Background(), "v1.2.0")
			if err == nil || got != (InitialInstallation{}) || execs != 0 {
				t.Fatal("missing legacy Pods granted external/CNPG authority", err)
			}
		})
	}
}

func TestInitialObserverRechecksActualBackendValuesBeforePublishing(t *testing.T) {
	observer, read := initialFixture(t)
	values := 0
	observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		raw, err := read(ctx, tool, args)
		if tool.Kind == Helm && args[5] == "values" {
			values++
			if values == 2 {
				return []byte(`{"postgres":{"mode":"external","bundled":{"enabled":false}}}`), nil
			}
		}
		return raw, err
	}
	got, err := observer.Observe(context.Background(), "v1.2.0")
	if err == nil || got != (InitialInstallation{}) || values != 2 {
		t.Fatal("changed backend configuration re-admitted", err)
	}
}

func TestInitialBackendParserPreservesLegacyFallbackAndRejectsAmbiguity(t *testing.T) {
	for _, test := range []struct{ raw, want string }{{`{"postgres":{"mode":"","bundled":{"enabled":true}}}`, "bundled"}, {`{"postgres":{"bundled":{"enabled":false}}}`, "external"}, {`{"postgres":{"mode":"cloudNativePG","bundled":{"enabled":true}}}`, "cloudNativePG"}, {`{"postgres":{"mode":"external"},"futureChartField":42}`, "external"}} {
		got, err := parseHelmDatabaseBackend([]byte(test.raw))
		if err != nil || got != test.want {
			t.Fatal("canonical chart backend semantics changed", err)
		}
	}
	for _, raw := range []string{`{}`, `{"postgres":null}`, `{"postgres":{"mode":""}}`, `{"postgres":{"bundled":{"enabled":"false"}}}`, `{"postgres":{"mode":"external","mode":"bundled"}}`, `{"postgres":{"mode":"external"}} {}`} {
		if got, err := parseHelmDatabaseBackend([]byte(raw)); err == nil || got != "" {
			t.Fatal("ambiguous/missing chart backend accepted")
		}
	}
}
