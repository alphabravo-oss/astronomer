//go:build unix && integration

package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/version"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
)

// Actual dry-run API defaulting/readback control, on the runner's separately
// owned selected cluster. No kubectl printer, admission-default mock or hidden
// kubeconfig/exec-plugin path. No real resource POST or DELETE is possible.
type databaseScanAPIFixture struct {
	Origin                 string          `json:"origin"`
	DestinationPolicy      string          `json:"destination_policy"`
	CAFile                 string          `json:"ca_file"`
	TokenFile              string          `json:"token_file"`
	ClusterUID             types.UID       `json:"cluster_uid"`
	NamespaceUID           types.UID       `json:"namespace_uid"`
	LeaseUUID              string          `json:"lease_uuid"`
	GitVersion             string          `json:"git_version"`
	NodeName               string          `json:"node_name"`
	Architecture           string          `json:"architecture"`
	FeedDigest             string          `json:"feed_digest"`
	WorkloadRepository     string          `json:"workload_repository"`
	WorkloadDigest         string          `json:"workload_digest"`
	PreparationEnvironment []corev1.EnvVar `json:"preparation_environment"`
	PrivatePull            bool            `json:"private_pull"`
	MappedRelease          bool            `json:"mapped_release"`
}

func TestOwnedDatabaseJobsSelectedAPIDryRunDefaultsAndCriticalMutations(t *testing.T) {
	fixture := openDatabaseRestartPostgresFixture(t)
	data := readRestartFixturePublicFile(t, os.Getenv("W04_TEST_KUBERNETES_API_FIXTURE"), 128<<10)
	var input databaseScanAPIFixture
	if jsoncontract.Decode(data, &input) != nil {
		t.Fatal("closed owned API fixture invalid")
	}
	lease, err := uuid.Parse(input.LeaseUUID)
	parsed, parseErr := url.Parse(input.Origin)
	if err != nil || lease.Version() != 4 || lease.String() != input.LeaseUUID || parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || !validDatabaseScanUID(input.ClusterUID) || !validDatabaseScanUID(input.NamespaceUID) || input.GitVersion == "" {
		t.Fatal("exact owned API identity unavailable")
	}
	network, err := httpclient.ParseDestinationPolicy(input.DestinationPolicy)
	if err != nil || !network.Restricted {
		t.Fatal("owned API finite destination policy unavailable")
	}
	ca := readRestartFixturePublicFile(t, input.CAFile, 256<<10)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("owned API public roots invalid")
	}
	token := readDatabaseAPIPrivateCredential(t, input.TokenFile)
	defer clear(token)
	if len(token) == 0 || bytes.ContainsAny(token, " \r\n\t") {
		t.Fatal("owned API scoped credential invalid")
	}
	client, err := httpclient.NewDestinationClient(network, httpclient.DestinationOptions{Timeout: 15 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, MaxResponseBytes: databaseScanMaximumAPIBytes})
	if err != nil {
		t.Fatal("owned API guarded client unavailable")
	}
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	call := func(method, path string, body []byte, target any) {
		t.Helper()
		// No execution path can send a persisted write. Only these two fixed dry-run
		// collection requests and bounded named identity GETs are admitted.
		allowed := method == http.MethodGet && (path == "/version" || path == "/api/v1/namespaces/kube-system" || path == "/api/v1/namespaces/"+databasejob.ScanNamespace)
		if method == http.MethodPost {
			allowed = path == "/apis/batch/v1/namespaces/"+databasejob.ScanNamespace+"/jobs?dryRun=All&fieldValidation=Strict" || path == "/api/v1/namespaces/"+databasejob.ScanNamespace+"/pods?dryRun=All&fieldValidation=Strict"
		}
		if !allowed || len(body) > databaseScanMaximumAPIBytes {
			t.Fatal("owned API fixture request outside finite dry-run scope")
		}
		request, err := http.NewRequestWithContext(ctx, method, input.Origin+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal("owned API request unavailable")
		}
		request.Header.Set("Authorization", "Bearer "+string(token))
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("owned API guarded request failed")
		}
		payload, readErr := io.ReadAll(io.LimitReader(response.Body, databaseScanMaximumAPIBytes+1))
		closeErr := response.Body.Close()
		defer clear(payload)
		if readErr != nil || closeErr != nil || len(payload) > databaseScanMaximumAPIBytes || response.StatusCode != 200 && response.StatusCode != 201 || jsoncontract.Decode(payload, target) != nil {
			t.Fatal("owned API bounded dry-run readback failed")
		}
	}
	var actualVersion version.Info
	call(http.MethodGet, "/version", nil, &actualVersion)
	if actualVersion.GitVersion != input.GitVersion {
		t.Fatal("selected actual Kubernetes version differs")
	}
	var system, namespace corev1.Namespace
	call(http.MethodGet, "/api/v1/namespaces/kube-system", nil, &system)
	call(http.MethodGet, "/api/v1/namespaces/"+databasejob.ScanNamespace, nil, &namespace)
	if system.UID != input.ClusterUID || namespace.UID != input.NamespaceUID || namespace.Annotations["astronomer.io/plan032-owned-lease"] != input.LeaseUUID {
		t.Fatal("actual cluster/namespace does not match exact runner lease")
	}
	proof, err := fixture.authority.Base(ctx)
	if err != nil {
		t.Fatal("actual signed fixture base unavailable")
	}
	claim := uuid.New()
	jobs, err := databasejob.BuildScanJobs(ctx, fixture.authority, proof, databasejob.JobRequest{ClaimID: claim, FeedDigest: input.FeedDigest, WorkloadRepository: input.WorkloadRepository, WorkloadDigest: input.WorkloadDigest, Architecture: input.Architecture, NodeName: input.NodeName, PreparationEnvironment: input.PreparationEnvironment, PrivatePull: input.PrivatePull, MappedRelease: input.MappedRelease})
	if err != nil {
		t.Fatal("canonical signed-profile Job builder failed")
	}
	for _, expected := range []*batchv1.Job{jobs.Preparation, jobs.Scan} {
		t.Run(expected.Name, func(t *testing.T) {
			request := expected.DeepCopy()
			request.Kind = "Job"
			request.APIVersion = "batch/v1"
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(body)
			var actual batchv1.Job
			call(http.MethodPost, "/apis/batch/v1/namespaces/"+databasejob.ScanNamespace+"/jobs?dryRun=All&fieldValidation=Strict", body, &actual)
			if !validDatabaseScanUID(actual.UID) || !databaseScanJobSpecMatches(expected, &actual) {
				t.Fatal("actual selected API defaults rejected canonical Job")
			}
			pod := corev1.Pod{TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: expected.Name + "-dryrun", Namespace: databasejob.ScanNamespace, Labels: expected.Spec.Template.Labels}, Spec: *expected.Spec.Template.Spec.DeepCopy()}
			podBody, err := json.Marshal(pod)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(podBody)
			var actualPod corev1.Pod
			call(http.MethodPost, "/api/v1/namespaces/"+databasejob.ScanNamespace+"/pods?dryRun=All&fieldValidation=Strict", podBody, &actualPod)
			if !databaseScanPodSpecMatches(expected.Spec.Template.Spec, actualPod.Spec) {
				t.Fatal("actual Pod admission defaults rejected canonical scan")
			}
			changed := actualPod.DeepCopy()
			changed.Spec.Containers[0].Image = "untrusted.example/scanner:latest"
			if databaseScanPodSpecMatches(expected.Spec.Template.Spec, changed.Spec) {
				t.Fatal("mutable image survived actual defaulted comparison")
			}
			changed = actualPod.DeepCopy()
			changed.Spec.Containers[0].VolumeMounts = append(changed.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "authority", MountPath: "/credential"})
			if databaseScanPodSpecMatches(expected.Spec.Template.Spec, changed.Spec) {
				t.Fatal("extra credential mount survived actual defaulted comparison")
			}
			changed = actualPod.DeepCopy()
			value := true
			changed.Spec.AutomountServiceAccountToken = &value
			if databaseScanPodSpecMatches(expected.Spec.Template.Spec, changed.Spec) {
				t.Fatal("API token survived actual defaulted comparison")
			}
		})
	}
	if err := ctx.Err(); err != nil {
		t.Fatal("owned API default fixture exceeded whole deadline")
	}
	if !strings.HasPrefix(actualVersion.GitVersion, "v1.") {
		t.Fatal("actual API version receipt missing")
	}
}

func readDatabaseAPIPrivateCredential(t *testing.T, path string) []byte {
	t.Helper()
	if !filepath.IsAbs(path) {
		t.Fatal("absolute private owned API credential missing")
	}
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal("private owned API credential unavailable")
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = unix.Close(descriptor)
		t.Fatal("private API credential descriptor unavailable")
	}
	defer func() { _ = file.Close() }()
	var stat unix.Stat_t
	if unix.Fstat(descriptor, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Mode&07777 != 0600 && stat.Mode&07777 != 0400 {
		t.Fatal("private API credential ownership/mode invalid")
	}
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 16<<10 {
		t.Fatal("private API credential must be bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || len(data) != int(before.Size()) {
		clear(data)
		t.Fatal("private API credential changed while reading")
	}
	return data
}
