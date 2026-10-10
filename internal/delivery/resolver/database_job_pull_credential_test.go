package resolver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseJobPullCapturesIndependentScopedMaterialAndRedactsEveryValue(t *testing.T) {
	directory := t.TempDir()
	material := []byte(`{"auths":{"registry.example.test":{"username":"pull-only-user","password":"synthetic-private-pull-token"}}}`)
	if err := os.WriteFile(filepath.Join(directory, "kubelet.json"), material, 0600); err != nil {
		t.Fatal(err)
	}
	registries := []string{"registry.example.test"}
	owner, err := CaptureDatabaseJobPullCredential(t.Context(), directory, "kubelet", registries)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	registries[0] = "foreign.example.test"
	copied, err := owner.Data(t.Context())
	if err != nil || string(copied) != string(material) {
		t.Fatal("independent mounted Kubelet material not retained")
	}
	copied[0] = 'x'
	clear(copied)
	if err := owner.Revalidate(t.Context()); err != nil {
		t.Fatal("caller-mutated exported copy changed owner")
	}
	for _, value := range []any{owner, *owner} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d"} {
			formatted := fmt.Sprintf(verb, value)
			if formatted != "<private database Job pull credential>" || strings.Contains(formatted, "synthetic-private") {
				t.Fatal("private token exposed by formatting")
			}
		}
	}
}
func TestDatabaseJobPullRejectsChangedMountAndUnknownDockerScope(t *testing.T) {
	valid := []byte(`{"auths":{"registry.example.test":{"username":"pull-user","password":"synthetic-token"}}}`)
	for name, material := range map[string][]byte{
		"foreign registry":   []byte(`{"auths":{"foreign.example.test":{"username":"pull-user","password":"synthetic-token"}}}`),
		"extra registry":     []byte(`{"auths":{"registry.example.test":{"username":"pull-user","password":"synthetic-token"},"foreign.example.test":{"username":"pull-user","password":"synthetic-token"}}}`),
		"credential helper":  []byte(`{"auths":{"registry.example.test":{"username":"pull-user","password":"synthetic-token"}},"credsStore":"ambient"}`),
		"nested duplicate":   []byte(`{"auths":{"registry.example.test":{"username":"pull-user","password":"synthetic-token","password":"changed"}}}`),
		"unknown auth field": []byte(`{"auths":{"registry.example.test":{"username":"pull-user","password":"synthetic-token","identitytoken":"extra"}}}`),
		"URL registry":       []byte(`{"auths":{"https://registry.example.test":{"username":"pull-user","password":"synthetic-token"}}}`),
		"missing password":   []byte(`{"auths":{"registry.example.test":{"username":"pull-user"}}}`),
		"extra document":     append(append([]byte(nil), valid...), []byte(` {}`)...),
		"oversized":          []byte(strings.Repeat("x", 32769)),
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "kubelet.json"), material, 0600); err != nil {
				t.Fatal(err)
			}
			if owner, err := CaptureDatabaseJobPullCredential(t.Context(), directory, "kubelet", []string{"registry.example.test"}); err == nil {
				owner.Close()
				t.Fatal("unbounded/unscoped Docker credential accepted")
			}
		})
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "kubelet.json")
	if err := os.WriteFile(path, valid, 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := CaptureDatabaseJobPullCredential(t.Context(), directory, "kubelet", []string{"registry.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := os.WriteFile(path, []byte(`{"auths":{"registry.example.test":{"username":"pull-user","password":"changed"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Data(t.Context()); err == nil {
		t.Fatal("changed original mounted credential ignored")
	}
}
func TestDatabaseJobPullCannotAdoptPublicSymlinkMissingOrCanceledMaterial(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "kubelet.json")
	valid := []byte(`{"auths":{"registry.example.test":{"username":"pull-user","password":"synthetic-token"}}}`)
	if err := os.WriteFile(path, valid, 0644); err != nil {
		t.Fatal(err)
	}
	if owner, err := CaptureDatabaseJobPullCredential(t.Context(), directory, "kubelet", []string{"registry.example.test"}); err == nil {
		owner.Close()
		t.Fatal("public credential mode accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	context, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CaptureDatabaseJobPullCredential(context, directory, "kubelet", []string{"registry.example.test"}); !errors.Is(err, context.Err()) {
		t.Fatal("canceled credential capture continued")
	}
	target := filepath.Join(directory, "actual.json")
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if owner, err := CaptureDatabaseJobPullCredential(t.Context(), directory, "kubelet", []string{"registry.example.test"}); err == nil {
		owner.Close()
		t.Fatal("symlink credential adopted")
	}
	if _, err := CaptureDatabaseJobPullCredential(t.Context(), directory, "../actual", []string{"registry.example.test"}); err == nil {
		t.Fatal("reference traversal accepted")
	}
	if _, err := CaptureDatabaseJobPullCredential(t.Context(), directory, "absent", []string{"registry.example.test"}); err == nil {
		t.Fatal("missing credential silently anonymous")
	}
}
