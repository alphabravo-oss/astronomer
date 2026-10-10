//go:build linux

package distribution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDatabasePublicationSignerPinsExecutableAndRejectsChangedOwner(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "signer")
	if err := os.WriteFile(path, []byte("fixture executable identity"), 0700); err != nil {
		t.Fatal(err)
	}
	signer, err := NewDatabasePublicationSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	defer signer.Close()
	if err := signer.current(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Run("protected-bootstrap-request", func(t *testing.T) {
		requestURL, requestToken := "https://pipelines.actions.githubusercontent.com/owned/idtoken?api-version=2.0", "fixture-oidc-token"
		request, err := CaptureDatabasePublicationGitHubRequest(requestURL, requestToken)
		if err != nil {
			t.Fatal("protected request capture failed")
		}
		captured, err := NewDatabasePublicationSignerWithGitHubOIDC(path, request)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		request.requestURL, request.requestToken = "https://caller.test/changed", "changed"
		t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://ambient.test/must-not-use")
		t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "ambient-must-not-use")
		t.Setenv("COSIGN_KEY", "ambient-key-must-not-use")
		environment, err := databasePublicationSignerEnvironment(false, "", captured.githubRequest)
		expected := []string{"PATH=/usr/bin:/bin", "ACTIONS_ID_TOKEN_REQUEST_URL=" + requestURL, "ACTIONS_ID_TOKEN_REQUEST_TOKEN=" + requestToken}
		if err != nil || !reflect.DeepEqual(environment, expected) {
			t.Fatal("signer did not freeze exactly the typed protected request")
		}
		qa, err := databasePublicationSignerEnvironment(true, "fixture-password", captured.githubRequest)
		if err != nil || !reflect.DeepEqual(qa, []string{"PATH=/usr/bin:/bin", "COSIGN_PASSWORD=fixture-password"}) {
			t.Fatal("QA signer inherited GitHub or ambient credentials")
		}
		if _, err := databasePublicationSignerEnvironment(false, "", nil); err == nil {
			t.Fatal("missing protected request reached signer child environment")
		}
		for _, value := range []any{captured.githubRequest, *captured.githubRequest} {
			for _, format := range []string{"%v", "%+v", "%#v", "%s", "%d"} {
				rendering := fmt.Sprintf(format, value)
				if rendering != "protected GitHub publication request" || strings.Contains(rendering, requestToken) || strings.Contains(rendering, requestURL) {
					t.Fatal("pointer or value formatting retained protected request fields")
				}
			}
		}
		for _, invalid := range []struct{ url, token string }{{"", ""}, {requestURL, ""}, {"", requestToken}, {"http://caller.test/token", requestToken}, {"https://user:password@caller.test/token", requestToken}, {requestURL + "#fragment", requestToken}, {requestURL, "invalid\x00token"}, {requestURL, strings.Repeat("x", (16<<10)+1)}, {"https://caller.test/" + strings.Repeat("x", 8192), requestToken}} {
			if result, err := CaptureDatabasePublicationGitHubRequest(invalid.url, invalid.token); err == nil || result != nil {
				t.Fatal("invalid or incomplete protected request accepted")
			}
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := signer.current(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("signer rehash lost canonical cancellation")
	}
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture executable identity"), 0700); err != nil {
		t.Fatal(err)
	}
	if signer.current(t.Context()) == nil {
		t.Fatal("same bytes in a replaced executable inherited pinned identity")
	}
	signer.Close()
	if signer.current(t.Context()) == nil {
		t.Fatal("closed executable remained available")
	}
	for _, value := range []string{"relative-signer", filepath.Join(directory, "missing")} {
		if candidate, err := NewDatabasePublicationSigner(value); err == nil || candidate != nil {
			t.Fatal("unavailable signer accepted")
		}
	}
	nonexecuting := filepath.Join(directory, "not-executable")
	if err := os.WriteFile(nonexecuting, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if candidate, err := NewDatabasePublicationSigner(nonexecuting); err == nil || candidate != nil {
		t.Fatal("non-executable signer accepted")
	}
	linked := filepath.Join(directory, "linked")
	if err := os.Symlink(path, linked); err != nil {
		t.Fatal(err)
	}
	if candidate, err := NewDatabasePublicationSigner(linked); err == nil || candidate != nil {
		t.Fatal("symlink signer accepted")
	}
}

func TestDatabasePublicationSignatureCleanupPreservesReplacementsAndUnboundOutputs(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(directory)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "payload.json")
	if err := os.WriteFile(path, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	owned, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(directory, "payload.original")
	if err := os.Rename(path, original); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("same UID replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(directory, "signature")
	if err := os.WriteFile(partial, []byte("partial unbound signature"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanupDatabaseSignatureDirectory(directory, before, map[string]os.FileInfo{"payload.json": owned})
	replacement, err := os.ReadFile(path)
	if err != nil || string(replacement) != "same UID replacement" {
		t.Fatal("cleanup deleted a replacement path")
	}
	if _, err := os.Lstat(partial); err != nil {
		t.Fatal("cleanup deleted an unbound partial output")
	}
	if _, err := os.Lstat(original); err != nil {
		t.Fatal("cleanup escaped the exact owned file set")
	}
	// A successful exact owned set is removed without recursive cleanup.
	success := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(success, 0700); err != nil {
		t.Fatal(err)
	}
	successBefore, _ := os.Lstat(success)
	successPath := filepath.Join(success, "payload.json")
	if err := os.WriteFile(successPath, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	successFile, _ := os.Lstat(successPath)
	cleanupDatabaseSignatureDirectory(success, successBefore, map[string]os.FileInfo{"payload.json": successFile})
	if _, err := os.Lstat(success); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("exact owned signature directory cleanup did not complete")
	}
}
