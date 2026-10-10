package databasejob

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

const (
	ScanNamespace    = "astronomer-database-scans"
	InputVolumeRoot  = "/run/astronomer/input"
	CoordinationRoot = "/run/astronomer/coordination"
	HelperExecutable = "/usr/local/bin/database-cache-helper"
)

// JobRequest is owner-selected orchestration DATA. The actual owner must lock
// current accepted079/base/assignment/workload/node identities before using the
// result, and again before each transition/report transaction. This builder
// authenticates the helper/scanner image closure; it cannot accept a feed or
// turn a Pod, preparation or completed Job into a scan-used witness.
type JobRequest struct {
	ClaimID                                                      uuid.UUID
	FeedDigest, WorkloadRepository, WorkloadDigest, Architecture string
	NodeName                                                     string
	// The immutable Secret contains only the constructor's closed preparation
	// material. Its public typed environment is generated at composition; no
	// caller-selected arbitrary environment, secret headers or URLs enter here.
	PreparationEnvironment []corev1.EnvVar
	PrivatePull            bool
	// Only constructor-owned release mapping selects the extra closed material
	// and fixed typed config path; this cannot grant destination permission.
	MappedRelease bool
	// Exact owner-created immutable pull Secret; never mounted in scanner/helper.
	ImagePullSecret string
}

type ScanJobs struct {
	Preparation, Scan         *batchv1.Job
	Volume                    *corev1.PersistentVolumeClaim
	DenyEgress                *networkv1.NetworkPolicy
	HelperChild, ScannerChild string
}

// BuildScanJobs requires the SAME captured canonical authority, not a caller
// snapshot of profile images. Returned API objects are DATA. No API requests,
// Kubernetes credentials, signing, claims or filesystem writes occur here.
func BuildScanJobs(ctx context.Context, authority *distribution.DatabaseAuthority, proof *distribution.VerifiedDatabaseAuthorityBase, request JobRequest) (ScanJobs, error) {
	if ctx == nil || authority == nil || proof == nil {
		return ScanJobs{}, errors.New("owned scan current physical authority unavailable")
	}
	images, err := authority.DatabaseScanImages(ctx, proof)
	if err != nil {
		return ScanJobs{}, err
	}
	return buildScanJobs(request, images)
}

func buildScanJobs(request JobRequest, images distribution.DatabaseScanImages) (ScanJobs, error) {
	if request.ClaimID.Version() != 4 || request.ClaimID.Variant() != uuid.RFC4122 || !recordDigest.MatchString(request.FeedDigest) || !recordDigest.MatchString(request.WorkloadDigest) || len(validation.IsDNS1123Subdomain(request.NodeName)) != 0 || request.NodeName == "" {
		return ScanJobs{}, errors.New("owned scan current workload or claim identity invalid")
	}
	if (request.Architecture != "amd64" && request.Architecture != "arm64") || !validWorkloadRepository(request.WorkloadRepository) {
		return ScanJobs{}, errors.New("owned scan immutable workload location invalid")
	}
	environment, err := preparationEnvironment(request.PreparationEnvironment, request.ClaimID, request.PrivatePull, request.MappedRelease)
	if err != nil {
		return ScanJobs{}, err
	}
	helper, err := platformImage(images.Helper, request.Architecture)
	if err != nil {
		return ScanJobs{}, err
	}
	scanner, err := platformImage(images.Scanner, request.Architecture)
	if err != nil {
		return ScanJobs{}, err
	}
	if request.ImagePullSecret != "" && request.ImagePullSecret != "database-scan-"+request.ClaimID.String()+"-image-pull" {
		return ScanJobs{}, errors.New("owned scan Kubelet pull Secret differs from exact claim")
	}
	inputRoot := InputVolumeRoot + "/input-" + request.ClaimID.String()
	name := "database-scan-" + request.ClaimID.String()
	claim := request.ClaimID.String()
	labels := map[string]string{"app.kubernetes.io/managed-by": "astronomer", "astronomer.io/database-claim": claim}
	metadata := func(suffix string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name + suffix, Namespace: ScanNamespace, Labels: copyLabels(labels)}
	}
	result := ScanJobs{HelperChild: helper, ScannerChild: scanner}
	result.Volume = &corev1.PersistentVolumeClaim{ObjectMeta: metadata("-input"), Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("32Gi")}}}}
	// The owner must create/read back this policy BEFORE creating the scan Job.
	// Native CNI enforcement still needs an actual no-egress qualification lane.
	result.DenyEgress = &networkv1.NetworkPolicy{ObjectMeta: metadata("-deny"), Spec: networkv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"astronomer.io/database-claim": claim, "astronomer.io/database-role": "scan"}}, PolicyTypes: []networkv1.PolicyType{networkv1.PolicyTypeEgress}, Egress: []networkv1.NetworkPolicyEgressRule{}}}
	result.Preparation = scanJob(metadata("-prepare"), request.NodeName, request.Architecture, 3000, "prepare")
	result.Scan = scanJob(metadata("-scan"), request.NodeName, request.Architecture, 3000, "scan")
	if request.ImagePullSecret != "" {
		result.Preparation.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: request.ImagePullSecret}}
		result.Scan.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: request.ImagePullSecret}}
	}
	prep := &result.Preparation.Spec.Template.Spec
	prep.Volumes = []corev1.Volume{inputVolume(result.Volume.Name), emptyVolume("authority"), emptyVolume("temporary"), {Name: "projected", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: name + "-authority", DefaultMode: pointer[int32](0444)}}}}
	bootstrapArgs := []string{"--bootstrap-material"}
	if request.PrivatePull {
		bootstrapArgs = append(bootstrapArgs, "--private-pull")
	}
	if request.MappedRelease {
		bootstrapArgs = append(bootstrapArgs, "--mapped-release")
	}
	bootstrap := helperContainer("authority-bootstrap", helper, bootstrapArgs...)
	bootstrap.SecurityContext = processSecurity(true)
	bootstrap.VolumeMounts = []corev1.VolumeMount{{Name: "authority", MountPath: PrivateJobMaterialRoot}, {Name: "projected", MountPath: ProjectedJobMaterialRoot, ReadOnly: true}}
	initialize := helperContainer("input-bootstrap", helper, "--initialize-input", "--claim-id", claim)
	initialize.SecurityContext = processSecurity(true)
	initialize.VolumeMounts = []corev1.VolumeMount{{Name: "input", MountPath: InputVolumeRoot}}
	normalize := helperContainer("authority-initialize", helper, "--initialize-material")
	normalize.SecurityContext = processSecurity(true)
	normalize.VolumeMounts = []corev1.VolumeMount{{Name: "authority", MountPath: PrivateJobMaterialRoot}}
	prep.InitContainers = []corev1.Container{normalize, bootstrap, initialize}
	stage := helperContainer("prepare", helper, "--stage", "--claim-id", claim, "--feed-digest", request.FeedDigest, "--staging-root", inputRoot+"/staging", "--cache-root", inputRoot+"/cache", "--workload-root", inputRoot+"/workload", "--coordination-root", CoordinationRoot, "--workload-repository", request.WorkloadRepository, "--workload-digest", request.WorkloadDigest, "--architecture", request.Architecture)
	stage.Env = environment
	// Canonical trust constructors create private temporary snapshots. Only this
	// bounded emptyDir supplies writable temporary space; the authority remains RO.
	stage.VolumeMounts = []corev1.VolumeMount{{Name: "authority", MountPath: PrivateJobMaterialRoot, ReadOnly: true}, {Name: "input", MountPath: InputVolumeRoot}, {Name: "temporary", MountPath: "/tmp"}}
	prep.Containers = []corev1.Container{stage}
	scan := &result.Scan.Spec.Template.Spec
	scan.Volumes = []corev1.Volume{inputVolume(result.Volume.Name), emptyVolume("coordination"), emptyVolume("temporary")}
	scan.Volumes[0].PersistentVolumeClaim.ReadOnly = true
	init := helperContainer("coordination-bootstrap", helper, "--initialize-coordination")
	init.SecurityContext = processSecurity(true)
	init.VolumeMounts = []corev1.VolumeMount{{Name: "coordination", MountPath: CoordinationRoot}}
	inputMounts := []corev1.VolumeMount{{Name: "input", MountPath: InputVolumeRoot, ReadOnly: true}, {Name: "coordination", MountPath: CoordinationRoot}}
	observe := helperContainer("observe", helper, "--cache-root", inputRoot+"/cache", "--workload-root", inputRoot+"/workload", "--coordination-root", CoordinationRoot)
	observe.RestartPolicy = pointer(corev1.ContainerRestartPolicyAlways)
	observe.VolumeMounts = append([]corev1.VolumeMount(nil), inputMounts...)
	await := helperContainer("await", helper, "--await-prepared", "--cache-root", inputRoot+"/cache", "--workload-root", inputRoot+"/workload", "--coordination-root", CoordinationRoot)
	await.VolumeMounts = append([]corev1.VolumeMount(nil), inputMounts...)
	scan.InitContainers = []corev1.Container{init, observe, await}
	// No preparation Secret, writable database, coordination, API token or
	// registry credentials are mounted in the scanner. Memory controls only the
	// scan cache: selected actual bbolt/Java SQLite readonly operation is a
	// separate mandatory runtime qualification, never inferred from this flag.
	scan.Containers = []corev1.Container{{Name: "scanner", Image: scanner, ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{"/usr/local/bin/trivy"}, Args: []string{"--config", "", "--quiet", "--timeout", "20m", "--cache-dir", inputRoot + "/cache/database-cache-" + claim, "image", "--input", inputRoot + "/workload/workload-" + claim, "--cache-backend", "memory", "--skip-db-update", "--skip-java-db-update", "--offline-scan", "--skip-version-check", "--disable-telemetry", "--scanners", "vuln", "--format", "json", "--ignorefile", "", "--list-all-pkgs=false"}, Env: []corev1.EnvVar{{Name: "TMPDIR", Value: "/tmp"}, {Name: "HOME", Value: "/tmp"}}, SecurityContext: processSecurity(false), Resources: processResources(), VolumeMounts: []corev1.VolumeMount{{Name: "input", MountPath: InputVolumeRoot, ReadOnly: true}, {Name: "temporary", MountPath: "/tmp"}}, TerminationMessagePolicy: corev1.TerminationMessageReadFile}}
	return result, nil
}

func scanJob(meta metav1.ObjectMeta, node, architecture string, deadline int64, role string) *batchv1.Job {
	labels := copyLabels(meta.Labels)
	labels["astronomer.io/database-role"] = role
	return &batchv1.Job{ObjectMeta: meta, Spec: batchv1.JobSpec{BackoffLimit: pointer[int32](0), ActiveDeadlineSeconds: &deadline, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{AutomountServiceAccountToken: pointer(false), RestartPolicy: corev1.RestartPolicyNever, NodeName: node, NodeSelector: map[string]string{"kubernetes.io/os": "linux", "kubernetes.io/arch": architecture}, TerminationGracePeriodSeconds: pointer[int64](960), SecurityContext: &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}}}}}
}
func helperContainer(name, image string, args ...string) corev1.Container {
	return corev1.Container{Name: name, Image: image, ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{HelperExecutable}, Args: args, SecurityContext: processSecurity(false), Resources: processResources(), TerminationMessagePolicy: corev1.TerminationMessageReadFile}
}
func processSecurity(root bool) *corev1.SecurityContext {
	uid := int64(JobProcessUID)
	if root {
		uid = 0
	}
	capabilities := &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}
	if root {
		capabilities.Add = []corev1.Capability{"CHOWN", "FOWNER", "DAC_OVERRIDE"}
	}
	return &corev1.SecurityContext{RunAsUser: &uid, RunAsGroup: &uid, RunAsNonRoot: pointer(!root), AllowPrivilegeEscalation: pointer(false), ReadOnlyRootFilesystem: pointer(true), Capabilities: capabilities}
}
func processResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("256Mi")}, Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("2Gi")}}
}
func inputVolume(name string) corev1.Volume {
	return corev1.Volume{Name: "input", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: name}}}
}
func emptyVolume(name string) corev1.Volume {
	return corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: pointer(resource.MustParse("2Gi"))}}}
}
func pointer[T any](value T) *T { return &value }
func copyLabels(source map[string]string) map[string]string {
	value := map[string]string{}
	for key, item := range source {
		value[key] = item
	}
	return value
}
func validWorkloadRepository(repository string) bool {
	if len(repository) == 0 || len(repository) > 512 {
		return false
	}
	parsed, err := remote.NewRepository(repository)
	return err == nil && parsed.Reference.Reference == ""
}

func platformImage(image distribution.DatabaseScanImage, architecture string) (string, error) {
	repository, index, found := strings.Cut(image.Reference, "@")
	if !found || !validWorkloadRepository(repository) || !recordDigest.MatchString(index) {
		return "", errors.New("owned scan authenticated image identity invalid")
	}
	selected := ""
	seen := map[string]bool{}
	if len(image.Platforms) != 2 {
		return "", errors.New("owned scan image platform closure incomplete")
	}
	for _, platform := range image.Platforms {
		if (platform.Architecture != "amd64" && platform.Architecture != "arm64") || seen[platform.Architecture] || !recordDigest.MatchString(platform.Digest) {
			return "", errors.New("owned scan image platform closure invalid")
		}
		seen[platform.Architecture] = true
		if platform.Architecture == architecture {
			selected = platform.Digest
		}
	}
	if selected == "" {
		return "", errors.New("owned scan image platform unavailable")
	}
	return repository + "@" + selected, nil
}

func preparationEnvironment(source []corev1.EnvVar, claim uuid.UUID, privatePull, mappedRelease bool) ([]corev1.EnvVar, error) {
	inputRoot := InputVolumeRoot + "/input-" + claim.String()
	fixed := map[string]string{
		"DELIVERY_ENABLED": "true", "DELIVERY_DATABASE_UPDATES_ENABLED": "true",
		"TMPDIR": "/tmp", "HOME": "/tmp",
		"RELEASE_MANIFEST_PATH":                PrivateJobMaterialRoot + "/release.json",
		"DELIVERY_SOURCE_TRUST_DIRECTORY":      PrivateJobMaterialRoot + "/trust",
		"DISTRIBUTION_MIRROR_PLAN_PATH":        PrivateJobMaterialRoot + "/mirror-plan.json",
		"DISTRIBUTION_MIRROR_SIGNATURE_PATH":   PrivateJobMaterialRoot + "/mirror-signature.json",
		"DISTRIBUTION_MIRROR_KEY_REF":          "mirror",
		"DELIVERY_DATABASE_REGISTRY_ROOTS_REF": "registry",
		"DELIVERY_DATABASE_CACHE_DIRECTORY":    inputRoot + "/staging",
	}
	if mappedRelease {
		fixed["RELEASE_MIRROR_MAPPING_PATH"] = PrivateJobMaterialRoot + "/release-mapping.json"
	}
	allowed := map[string]bool{}
	for name := range fixed {
		allowed[name] = true
	}
	for _, name := range []string{"DISTRIBUTION_DESTINATION_POLICY", "DELIVERY_DATABASE_SCANNER_DIGEST", "DELIVERY_DATABASE_SCANNER_VERSION", "DELIVERY_DATABASE_UPDATE_REPOSITORY", "DELIVERY_DATABASE_READ_CREDENTIAL_REF", "DELIVERY_DATABASE_READ_REGISTRY", "DELIVERY_DATABASE_READ_REALM_URL", "DELIVERY_DATABASE_READ_SERVICE", "DELIVERY_DATABASE_READ_REPOSITORIES"} {
		allowed[name] = true
	}
	seen := map[string]bool{}
	total := 0
	result := make([]corev1.EnvVar, 0, len(source))
	for _, value := range source {
		if !allowed[value.Name] || seen[value.Name] || value.ValueFrom != nil || value.Value == "" || strings.ContainsAny(value.Value, "\x00\r\n") || len(value.Value) > 32768 || len(value.Value) > 65536-total {
			return nil, errors.New("owned preparation environment is outside closed constructor scope")
		}
		if expected, found := fixed[value.Name]; found && value.Value != expected {
			return nil, errors.New("owned preparation environment fixed mount changed")
		}
		if value.Name == "DELIVERY_DATABASE_READ_CREDENTIAL_REF" && value.Value != "pull" {
			return nil, errors.New("owned preparation pull reference changed")
		}
		total += len(value.Value)
		seen[value.Name] = true
		result = append(result, value)
	}
	for name := range fixed {
		if !seen[name] {
			return nil, errors.New("owned preparation fixed environment missing")
		}
	}
	for _, name := range []string{"DISTRIBUTION_DESTINATION_POLICY", "DELIVERY_DATABASE_SCANNER_DIGEST", "DELIVERY_DATABASE_SCANNER_VERSION", "DELIVERY_DATABASE_UPDATE_REPOSITORY"} {
		if !seen[name] {
			return nil, errors.New("owned preparation public authority data missing")
		}
	}
	for _, name := range []string{"DELIVERY_DATABASE_READ_CREDENTIAL_REF", "DELIVERY_DATABASE_READ_REGISTRY", "DELIVERY_DATABASE_READ_REALM_URL", "DELIVERY_DATABASE_READ_SERVICE", "DELIVERY_DATABASE_READ_REPOSITORIES"} {
		if seen[name] != privatePull {
			return nil, errors.New("owned preparation pull scope is incomplete or unexpectedly enabled")
		}
	}
	return result, nil
}
