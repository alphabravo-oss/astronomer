//go:build unix && integration

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"os"

	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

type localQAKubernetesInput struct {
	Scope             string `json:"scope"`
	SourceCommit      string `json:"source_commit"`
	Kubeconfig        string `json:"kubeconfig"`
	AgentNamespace    string `json:"agent_namespace"`
	AgentPod          string `json:"agent_pod"`
	AgentPodUID       string `json:"agent_pod_uid"`
	AgentContainer    string `json:"agent_container"`
	AgentImage        string `json:"agent_image"`
	AgentChildDigest  string `json:"agent_child_digest"`
	AgentConfigDigest string `json:"agent_config_digest"`
}

func localQAKubernetes(t *testing.T) (kubernetes.Interface, localQAKubernetesInput) {
	t.Helper()
	bytes := localQAReadPrivateInput(t, os.Getenv("W04_LOCAL_QA_KUBERNETES_INPUT"), 64<<10)
	defer clear(bytes)
	var input localQAKubernetesInput
	if jsoncontract.Decode(bytes, &input) != nil || input.Scope != localQARuntimeLabel || input.SourceCommit != version.GitCommit || input.AgentNamespace == "" || input.AgentPod == "" || input.AgentPodUID == "" || input.AgentContainer == "" {
		t.Fatal("local QA actual agent/API constructor input incomplete")
	}
	for _, value := range []string{input.AgentChildDigest, input.AgentConfigDigest} {
		selected := digest.Digest(value)
		if selected.Validate() != nil || selected.Algorithm() != digest.SHA256 {
			t.Fatal("local QA source-bound agent platform identity invalid")
		}
	}
	repository, index, found := strings.Cut(input.AgentImage, "@")
	if !found || repository == "" || digest.Digest(index).Validate() != nil {
		t.Fatal("local QA agent image must be immutable")
	}
	contents := localQAReadPrivateInput(t, input.Kubeconfig, 1<<20)
	defer clear(contents)
	cfg, err := clientcmd.RESTConfigFromKubeConfig(contents)
	if err != nil {
		t.Fatal("local QA real downstream API config invalid")
	}
	// This explicit ROOT-owned kubeconfig supplies real fixture authority. No
	// feed, report, graph URL or bearer token supplies Kubernetes permissions.
	cfg.Timeout = 20 * time.Second
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal("local QA selected Kubernetes client unavailable")
	}
	return client, input
}
func localQAObserveCurrentAgent(t *testing.T, ctx context.Context, cluster uuid.UUID, serverURL string) {
	t.Helper()
	client, input := localQAKubernetes(t)
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pod, err := client.CoreV1().Pods(input.AgentNamespace).Get(bounded, input.AgentPod, metav1.GetOptions{})
	if err != nil || string(pod.UID) != input.AgentPodUID || pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
		t.Fatal("local QA actual candidate agent Pod not current")
	}
	repository, _, _ := strings.Cut(input.AgentImage, "@")
	found := false
	for _, container := range pod.Spec.Containers {
		if container.Name == input.AgentContainer {
			if found || container.Image != input.AgentImage {
				t.Fatal("local QA actual agent image differs from source-bound graph")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("local QA selected actual agent container absent")
	}
	clusterBound, serverBound, insecureBound := false, false, false
	for _, container := range pod.Spec.Containers {
		if container.Name != input.AgentContainer {
			continue
		}
		for _, variable := range container.Env {
			expected := ""
			switch variable.Name {
			case "ASTRONOMER_CLUSTER_ID":
				if clusterBound {
					t.Fatal("local QA actual agent cluster binding repeated")
				}
				clusterBound = true
				expected = cluster.String()
			case "ASTRONOMER_SERVER_URL":
				if serverBound {
					t.Fatal("local QA actual agent endpoint binding repeated")
				}
				serverBound = true
				expected = serverURL
			case "ASTRONOMER_INSECURE":
				if insecureBound {
					t.Fatal("local QA actual agent secure binding repeated")
				}
				insecureBound = true
				expected = "false"
			default:
				continue
			}
			if variable.Value != "" || variable.ValueFrom == nil || variable.ValueFrom.ConfigMapKeyRef == nil || variable.ValueFrom.ConfigMapKeyRef.Name == "" || variable.ValueFrom.ConfigMapKeyRef.Optional != nil && *variable.ValueFrom.ConfigMapKeyRef.Optional {
				t.Fatal("local QA actual agent did not retain canonical installer config binding")
			}
			reference := variable.ValueFrom.ConfigMapKeyRef
			config, e := client.CoreV1().ConfigMaps(input.AgentNamespace).Get(bounded, reference.Name, metav1.GetOptions{})
			if e != nil || config.DeletionTimestamp != nil || config.Data[reference.Key] != expected {
				t.Fatal("local QA actual candidate agent belongs to another cluster/endpoint or insecure transport")
			}
		}
	}
	if !clusterBound || !serverBound || !insecureBound {
		t.Fatal("local QA actual agent missing canonical cluster/endpoint/secure config")
	}

	found = false
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == input.AgentContainer {
			if found || !status.Ready || status.State.Running == nil || !localQARuntimeImage(status.ImageID, repository, input.AgentChildDigest, input.AgentConfigDigest) {
				t.Fatal("local QA real candidate agent runtime identity/readiness differs")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("local QA actual agent runtime identity missing")
	}
}
func localQARuntimeImage(actual, repository, child, config string) bool {
	return actual == repository+"@"+child || actual == "docker-pullable://"+repository+"@"+child || actual == config || actual == "containerd://"+config || actual == "docker://"+config
}

func localQAObserveCompletedJob(t *testing.T, ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, witness sqlc.ImageVulnerabilityDatabaseWitness) {
	t.Helper()
	client, _ := localQAKubernetes(t)
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pod, err := client.CoreV1().Pods(databasejob.ScanNamespace).Get(bounded, claim.ScanPodName, metav1.GetOptions{})
	if err != nil || string(pod.UID) != witness.ScanPodUid.String() || pod.Status.Phase != corev1.PodSucceeded || pod.DeletionTimestamp != nil || pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("local QA genuine completed scan Pod/token boundary differs")
	}
	jobName := ""
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "Job" && owner.APIVersion == "batch/v1" && owner.Controller != nil && *owner.Controller {
			if jobName != "" || string(owner.UID) != witness.ScanJobUid.String() {
				t.Fatal("local QA actual Job owner identity differs")
			}
			jobName = owner.Name
		}
	}
	if jobName == "" {
		t.Fatal("local QA actual scan Job owner absent")
	}
	job, err := client.BatchV1().Jobs(databasejob.ScanNamespace).Get(bounded, jobName, metav1.GetOptions{})
	if err != nil || string(job.UID) != witness.ScanJobUid.String() || job.Status.Succeeded != 1 || job.Status.Active != 0 || job.Status.CompletionTime == nil {
		t.Fatal("local QA real Job did not terminate completely")
	}
	if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Name != "scanner" {
		t.Fatal("local QA scanner topology changed")
	}
	scanner := pod.Spec.Containers[0]
	security := scanner.SecurityContext
	if security == nil || security.RunAsUser == nil || *security.RunAsUser != databasejob.JobProcessUID || security.RunAsNonRoot == nil || !*security.RunAsNonRoot || security.ReadOnlyRootFilesystem == nil || !*security.ReadOnlyRootFilesystem || security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation {
		t.Fatal("local QA actual scanner non-root/readonly process contract changed")
	}
	input := false
	for _, mount := range scanner.VolumeMounts {
		if mount.MountPath == databasejob.InputVolumeRoot {
			if !mount.ReadOnly {
				t.Fatal("local QA actual sealed DB/workload volume is writable")
			}
			input = true
		}
		if mount.Name != "input" && mount.Name != "temporary" {
			t.Fatal("local QA scanner adopted private credential/coordination mount")
		}
	}
	if !input || len(scanner.Env) != 2 {
		t.Fatal("local QA actual scanner isolated input/environment absent")
	}
	args := map[string]bool{}
	for _, argument := range scanner.Args {
		args[argument] = true
	}
	for _, argument := range []string{"--skip-db-update", "--skip-java-db-update", "--offline-scan", "--disable-telemetry"} {
		if !args[argument] {
			t.Fatal("local QA actual scanner update/network restriction missing")
		}
	}
	observer := false
	for _, container := range pod.Spec.InitContainers {
		if container.Name == "observe" {
			if observer || container.RestartPolicy == nil || *container.RestartPolicy != corev1.ContainerRestartPolicyAlways {
				t.Fatal("local QA actual native sidecar topology changed")
			}
			observer = true
		}
	}
	if !observer {
		t.Fatal("local QA actual final-measurement sidecar absent")
	}
	// All ordinary and native-sidecar container statuses must have actual exit
	// timestamps. A periodic pretermination measurement cannot satisfy this.
	scannerExited, observerExited := false, false
	for _, status := range append(append([]corev1.ContainerStatus(nil), pod.Status.ContainerStatuses...), pod.Status.InitContainerStatuses...) {
		if status.State.Terminated == nil || status.State.Terminated.ExitCode != 0 || status.State.Terminated.FinishedAt.IsZero() {
			t.Fatal("local QA actual container did not terminate successfully")
		}
		switch status.Name {
		case "scanner":
			scannerExited = true
			if status.ImageID != witness.ScannerImageID {
				t.Fatal("local QA scanner runtime imageID changed")
			}
		case "observe":
			observerExited = true
			if status.ImageID != witness.HelperImageID {
				t.Fatal("local QA observer runtime imageID changed")
			}
		}
	}
	if !scannerExited || !observerExited || witness.FinalLogAt.Before(witness.ScannerFinishedAt.Truncate(time.Second).Add(time.Second)) {
		t.Fatal("local QA final flush did not prove post-termination measurement")
	}
	node, err := client.CoreV1().Nodes().Get(bounded, claim.NodeName, metav1.GetOptions{})
	if err != nil || string(node.UID) != claim.NodeUid.String() {
		t.Fatal("local QA real agent/API/workload Node identity differs")
	}
	workload, err := client.CoreV1().Pods(claim.WorkloadNamespace).Get(bounded, claim.WorkloadPodName, metav1.GetOptions{})
	if err != nil || string(workload.UID) != claim.WorkloadPodUid.String() {
		t.Fatal("local QA actual workload replaced before witness check")
	}
	// Successful genuine scan establishes the selected actual offline scan against
	// the sealed pair, not that Java was opened or needed. UNKNOWN remains until a
	// measured needed/not-needed capability exists; no clean posture is claimed.
	if witness.JavaApplicability != "unknown" {
		t.Fatal("local QA unimplemented Java applicability must remain unknown")
	}
}
