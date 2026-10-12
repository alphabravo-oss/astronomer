package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

func assertOfflineTransferRejectsUnsafeMembers(t *testing.T, payload []byte, options distribution.OfflineOptions) {
	t.Helper()
	members, err := distribution.ReadBoundedRegularArchive(payload, 2*distribution.MaximumExpandedBytes, 2*distribution.MaximumExpandedBytes, 4*distribution.MaximumMembers)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"extra", "missing", "unknown-index", "duplicate", "traversal", "symlink", "hardlink", "trailing"} {
		t.Run("transfer-"+mutation, func(t *testing.T) {
			changed := map[string][]byte{}
			for name, data := range members {
				changed[name] = bytes.Clone(data)
			}
			var compressed []byte
			switch mutation {
			case "extra":
				changed["unbound.txt"] = []byte("extra")
			case "missing":
				for name := range changed {
					if name != "offline-index.json" {
						delete(changed, name)
						break
					}
				}
			case "unknown-index":
				changed["offline-index.json"] = append([]byte(`{"unknown":true,`), changed["offline-index.json"][1:]...)
			case "duplicate", "traversal", "symlink", "hardlink":
				var buffer bytes.Buffer
				gzipWriter := gzip.NewWriter(&buffer)
				writer := tar.NewWriter(gzipWriter)
				name := "offline-index.json"
				typeflag := byte(tar.TypeReg)
				if mutation == "traversal" {
					name = "../offline-index.json"
				}
				if mutation == "symlink" {
					typeflag = tar.TypeSymlink
				}
				if mutation == "hardlink" {
					typeflag = tar.TypeLink
				}
				size := int64(len(changed["offline-index.json"]))
				if typeflag != tar.TypeReg {
					size = 0
				}
				for repeat := 0; repeat < 2; repeat++ {
					if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: typeflag, Size: size, Mode: 0600, Linkname: "outside"}); err != nil {
						t.Fatal(err)
					}
					if size > 0 {
						if _, err := writer.Write(changed["offline-index.json"]); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				if err := gzipWriter.Close(); err != nil {
					t.Fatal(err)
				}
				compressed = buffer.Bytes()
			case "trailing":
				compressed = append(bytes.Clone(payload), []byte("trailing untrusted payload")...)
			}
			if compressed == nil {
				var err error
				compressed, err = packMembers(changed)
				if err != nil {
					t.Fatal(err)
				}
			}
			archive := filepath.Join(t.TempDir(), "unsafe.tar.gz")
			if err := os.WriteFile(archive, compressed, 0600); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "must-not-exist")
			if err := importOfflineTransfer(t.Context(), archive, destination, options); err == nil {
				t.Fatal("unsafe or open transfer imported")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("unverified archive caused filesystem mutation")
			}
		})
	}
}

func TestOfflineFlagsRequireIdentityAndRefuseNetworkConfiguration(t *testing.T) {
	if err := offlineTransferFromFlags("v1", "", "", "absent", "", "", "", "", "", "", "", "", "", "", "", ""); err == nil {
		t.Fatal("identity-free transfer accepted")
	}
	if err := offlineTransferFromFlags("v1", "", "", "absent", "", "registry.example.test/distribution", "sha256:pin", "sha256:catalog", "v1.2.2", "/mounted/trust", "cosign_key", "vendor", "", "", "network-policy", ""); err == nil {
		t.Fatal("offline action accepted network configuration")
	}
}
