//go:build linux

package upgradecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func acquisitionTestMetadata(t *testing.T) []byte {
	t.Helper()
	record := map[string]any{"tag_name": "v1.2.3", "draft": false, "prerelease": false}
	assets := make([]map[string]any, 0, 6)
	for i, name := range []string{"RELEASE_IMAGES", "SHA256SUMS", "astronomer-1.2.3.tgz", "release-manifest.json", "release-manifest.sigstore.json", "release-recovery-notes.json"} {
		assets = append(assets, map[string]any{"id": i + 1, "name": name, "size": 128, "state": "uploaded"})
	}
	record["assets"] = assets
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReleaseAcquisitionSelectsExactVersionAndRetainsNonAuthoritativeRecoveryFiles(t *testing.T) {
	assets, err := selectedReleaseAssets(acquisitionTestMetadata(t), "v1.2.3")
	if err != nil || len(assets) != 6 {
		t.Fatalf("bounded release selection = %v %v", assets, err)
	}
	found := false
	for _, asset := range assets {
		if asset.Name == "release-recovery-notes.json" {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy release-recovery wildcard capture lost")
	}
	if _, err := selectedReleaseAssets(acquisitionTestMetadata(t), "v1.2.4"); err == nil {
		t.Fatal("different selected tag admitted")
	}
}

func TestReleaseAcquisitionRejectsAmbiguousIncompleteAndUnboundedMetadata(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"draft", func(record map[string]any) { record["draft"] = true }},
		{"missing_state", func(record map[string]any) { delete(record, "prerelease") }},
		{"duplicate_id", func(record map[string]any) {
			assets := record["assets"].([]any)
			assets[1].(map[string]any)["id"] = assets[0].(map[string]any)["id"]
		}},
		{"duplicate_name", func(record map[string]any) {
			assets := record["assets"].([]any)
			assets[1].(map[string]any)["name"] = assets[0].(map[string]any)["name"]
		}},
		{"missing_chart", func(record map[string]any) {
			assets := record["assets"].([]any)
			assets[2].(map[string]any)["name"] = "astronomer-1.2.2.tgz"
		}},
		{"oversized_chart", func(record map[string]any) {
			assets := record["assets"].([]any)
			assets[2].(map[string]any)["size"] = releaseAssetContentMaximum + 1
		}},
		{"oversized_metadata", func(record map[string]any) {
			assets := record["assets"].([]any)
			assets[3].(map[string]any)["size"] = releaseAssetMetadataMaximum + 1
		}},
		{"unfinished_asset", func(record map[string]any) {
			assets := record["assets"].([]any)
			assets[3].(map[string]any)["state"] = "new"
		}},
		{"incomplete_recovery", func(record map[string]any) {
			assets := record["assets"].([]any)
			assets[5].(map[string]any)["name"] = "release-recovery.json"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var record map[string]any
			if err := json.Unmarshal(acquisitionTestMetadata(t), &record); err != nil {
				t.Fatal(err)
			}
			test.change(record)
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := selectedReleaseAssets(raw, "v1.2.3"); err == nil {
				t.Fatal("invalid release acquisition metadata admitted")
			}
		})
	}
}

func TestOfflineReleaseInputPreservesPublicOriginalsAndRejectsAliasesBudgetAndCancellation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "release-manifest.json")
	raw := []byte("synthetic-untrusted-public-original")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	body, bound, err := readOfflineReleaseInput(context.Background(), path, int64(len(raw)))
	if err != nil || !bytes.Equal(body, raw) {
		t.Fatalf("public original capture = %q %v", body, err)
	}
	if err := recheckOfflineReleaseInputs(context.Background(), []offlineReleaseInput{bound}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatal("public original permissions changed")
	}
	if _, _, err := readOfflineReleaseInput(context.Background(), path, int64(len(raw))-1); err == nil {
		t.Fatal("oversized original admitted")
	}
	alias := filepath.Join(directory, "alias.json")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readOfflineReleaseInput(context.Background(), alias, 1024); err == nil {
		t.Fatal("aliased original admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := readOfflineReleaseInput(ctx, path, 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := recheckOfflineReleaseInputs(context.Background(), []offlineReleaseInput{bound}); err == nil {
		t.Fatal("changed original binding admitted")
	}
}
