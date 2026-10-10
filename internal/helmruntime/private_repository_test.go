package helmruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/helmpath"
	repo "helm.sh/helm/v4/pkg/repo/v1"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// This connected fixture requires a declared owned private listener address;
// ordinary unit runs explicitly skip it. Qualification must select this parent
// with the owned address and reject the skip, rather than inherit unit results.
func TestHelmPrivateRepositoryVersionCredentialsCAAndBodyCancellation(t *testing.T) {
	address := os.Getenv("ASTRONOMER_HELM_HTTP_PRIVATE_FIXTURE_IP")
	if address == "" {
		t.Skip("owned private Helm repository address is not configured")
	}
	ip := net.ParseIP(address)
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		t.Fatal("fixture requires an explicitly owned RFC1918/ULA address")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(address, "0"))
	if err != nil {
		t.Fatal("owned private fixture listener is unavailable")
	}
	first, second := fixtureChartArchive(t, "1.0.0"), fixtureChartArchive(t, "2.0.0")
	var archiveCalls atomic.Int32
	blockedDone := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "fixture-user" || password != "fixture-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/index.yaml":
			_, _ = w.Write([]byte(fixtureRepositoryIndex(first, second)))
		case "/fixture-1.0.0.tgz":
			archiveCalls.Add(1)
			_, _ = w.Write(first)
		case "/fixture-2.0.0.tgz":
			archiveCalls.Add(1)
			_, _ = w.Write(second)
		case "/blocked-chart.tgz":
			defer close(blockedDone)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/oversized/index.yaml":
			w.Header().Set("Content-Length", fmt.Sprint(maximumRepositoryIndex+1))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	server.Listener = listener
	certificate, ca := fixtureRepositoryCertificate(t, ip)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	root := t.TempDir()
	caFile := filepath.Join(root, "ca.pem")
	if err := os.WriteFile(caFile, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := Config{RepositoryConfig: filepath.Join(root, "repositories.yaml"), RepositoryCache: filepath.Join(root, "repository"), ContentCache: filepath.Join(root, "content")}
	entry := &repo.Entry{Name: "owned", URL: server.URL, Username: "fixture-user", Password: "fixture-password", CAFile: caFile}
	file := repo.NewFile()
	file.Add(entry)
	if err := file.WriteFile(runtime.RepositoryConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(runtime.RepositoryCache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime.RepositoryCache, helmpath.CacheIndexFile(entry.Name)), []byte(fixtureRepositoryIndex(first, second)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LocateRepositoryChart(context.Background(), runtime, "owned/fixture", "", "1.0.0"); err == nil || archiveCalls.Load() != 0 {
		t.Fatal("unconfigured private repository reached the listener")
	}
	bits := 128
	if ip.To4() != nil {
		bits = 32
	}
	runtime.RepositoryDestinationPolicy = fmt.Sprintf(`{"origins":[{"url":%q,"cidrs":[%q]}]}`, server.URL, address+fmt.Sprintf("/%d", bits))
	for _, coordinates := range []struct{ name, url, version string }{{"owned/fixture", "", "1.0.0"}, {"fixture", server.URL, "2.0.0"}} {
		path, err := LocateRepositoryChart(context.Background(), runtime, coordinates.name, coordinates.url, coordinates.version)
		if err != nil {
			t.Fatal("configured private chart transfer failed")
		}
		chart, err := loader.Load(path)
		if err != nil || chart.Metadata.Version != coordinates.version {
			t.Fatal("standard repository version or archive loading changed")
		}
	}
	if archiveCalls.Load() != 2 {
		t.Fatal("private authentication/TLS/version transfer count changed")
	}
	transfer := repositoryGetter{ctx: context.Background(), runtime: runtime, owner: entry}
	if _, err := transfer.Get(server.URL + "/oversized/index.yaml"); !errors.Is(err, httpclient.ErrResponseBodyLimit) {
		t.Fatal("repository index size bound was not applied")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	transfer.ctx = ctx
	if _, err := transfer.Get(server.URL + "/blocked-chart.tgz"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("body transfer did not retain its operation deadline")
	}
	select {
	case <-blockedDone:
	case <-time.After(time.Second):
		t.Fatal("private repository did not observe body cancellation")
	}
}

func fixtureChartArchive(t *testing.T, version string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zipped)
	value := []byte("apiVersion: v2\nname: fixture\nversion: " + version + "\n")
	if err := archive.WriteHeader(&tar.Header{Name: "fixture/Chart.yaml", Mode: 0o600, Size: int64(len(value))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(value); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func fixtureRepositoryIndex(first, second []byte) string {
	firstDigest, secondDigest := sha256.Sum256(first), sha256.Sum256(second)
	return fmt.Sprintf("apiVersion: v1\nentries:\n  fixture:\n  - apiVersion: v2\n    name: fixture\n    version: 1.0.0\n    digest: %s\n    urls: [fixture-1.0.0.tgz]\n  - apiVersion: v2\n    name: fixture\n    version: 2.0.0\n    digest: %s\n    urls: [fixture-2.0.0.tgz]\n", hex.EncodeToString(firstDigest[:]), hex.EncodeToString(secondDigest[:]))
}

func fixtureRepositoryCertificate(t *testing.T, ip net.IP) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	profile := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{ip}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, profile, profile, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
