package databasejob

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
)

func ownedJobFixture(private bool) (JobRequest, distribution.DatabaseScanImages) {
	claim := uuid.MustParse("9824ad66-a4ed-42a5-8101-d9436c7de0e6")
	digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	image := func(repo, index, amd, arm string) distribution.DatabaseScanImage {
		return distribution.DatabaseScanImage{Reference: repo + "@" + digest(index), Platforms: []distribution.DatabaseScanPlatform{{Architecture: "amd64", Digest: digest(amd)}, {Architecture: "arm64", Digest: digest(arm)}}}
	}
	env := []corev1.EnvVar{
		{Name: "DELIVERY_ENABLED", Value: "true"}, {Name: "DELIVERY_DATABASE_UPDATES_ENABLED", Value: "true"},
		{Name: "RELEASE_MANIFEST_PATH", Value: PrivateJobMaterialRoot + "/release.json"},
		{Name: "DELIVERY_SOURCE_TRUST_DIRECTORY", Value: PrivateJobMaterialRoot + "/trust"},
		{Name: "DISTRIBUTION_MIRROR_PLAN_PATH", Value: PrivateJobMaterialRoot + "/mirror-plan.json"},
		{Name: "DISTRIBUTION_MIRROR_SIGNATURE_PATH", Value: PrivateJobMaterialRoot + "/mirror-signature.json"},
		{Name: "DISTRIBUTION_MIRROR_KEY_REF", Value: "mirror"},
		{Name: "DELIVERY_DATABASE_REGISTRY_ROOTS_REF", Value: "registry"},
		{Name: "DELIVERY_DATABASE_CACHE_DIRECTORY", Value: InputVolumeRoot + "/input-" + claim.String() + "/staging"},
		{Name: "DISTRIBUTION_DESTINATION_POLICY", Value: `{"restricted":true}`},
		{Name: "DELIVERY_DATABASE_SCANNER_DIGEST", Value: digest("d")},
		{Name: "DELIVERY_DATABASE_SCANNER_VERSION", Value: "0.75.0"},
		{Name: "DELIVERY_DATABASE_UPDATE_REPOSITORY", Value: "registry.example.test/database-updates"},
		{Name: "TMPDIR", Value: "/tmp"}, {Name: "HOME", Value: "/tmp"},
	}
	if private {
		env = append(env, corev1.EnvVar{Name: "DELIVERY_DATABASE_READ_CREDENTIAL_REF", Value: "pull"}, corev1.EnvVar{Name: "DELIVERY_DATABASE_READ_REGISTRY", Value: "registry.example.test"}, corev1.EnvVar{Name: "DELIVERY_DATABASE_READ_REALM_URL", Value: "https://registry.example.test/token"}, corev1.EnvVar{Name: "DELIVERY_DATABASE_READ_SERVICE", Value: "registry.example.test"}, corev1.EnvVar{Name: "DELIVERY_DATABASE_READ_REPOSITORIES", Value: `["database-updates"]`})
	}
	return JobRequest{ClaimID: claim, FeedDigest: digest("a"), WorkloadRepository: "registry.example.test/workload", WorkloadDigest: digest("b"), Architecture: "amd64", NodeName: "owned-node", PreparationEnvironment: env, PrivatePull: private}, distribution.DatabaseScanImages{DistributionIdentity: "fixture-data", Helper: image("registry.example.test/helper", "c", "1", "2"), Scanner: image("registry.example.test/scanner", "d", "3", "4")}
}

func TestOwnedScanJobsSeparatePreparationCredentialsFromReadOnlyScanner(t *testing.T) {
	for _, architecture := range []string{"amd64", "arm64"} {
		for _, private := range []bool{false, true} {
			t.Run(architecture+map[bool]string{false: "-anonymous", true: "-private"}[private], func(t *testing.T) {
				request, images := ownedJobFixture(private)
				request.Architecture = architecture
				request.ImagePullSecret="database-scan-"+request.ClaimID.String()+"-image-pull"
				value, err := buildScanJobs(request, images)
				if err != nil {
					t.Fatal(err)
				}
				prep, scan := value.Preparation.Spec.Template.Spec, value.Scan.Spec.Template.Spec
				if len(prep.ImagePullSecrets)!=1||len(scan.ImagePullSecrets)!=1||prep.ImagePullSecrets[0].Name!=request.ImagePullSecret||scan.ImagePullSecrets[0].Name!=request.ImagePullSecret {t.Fatal("explicit Kubelet credential not scoped to both exact Jobs")}
				if prep.NodeName != request.NodeName || scan.NodeName != request.NodeName || scan.NodeSelector["kubernetes.io/arch"] != architecture {
					t.Fatal("job moved away from the measured node")
				}
				if *prep.AutomountServiceAccountToken || *scan.AutomountServiceAccountToken || *value.Scan.Spec.BackoffLimit != 0 || *value.Scan.Spec.ActiveDeadlineSeconds != 3000 {
					t.Fatal("unbounded or credentialed job")
				}
				if value.Volume.Namespace != ScanNamespace || value.Volume.Spec.AccessModes[0] != corev1.ReadWriteOnce || value.Volume.Spec.Resources.Requests.Storage().String() != "32Gi" {
					t.Fatal("input volume outside finite owned scope")
				}
				if len(value.DenyEgress.Spec.PolicyTypes) != 1 || value.DenyEgress.Spec.PolicyTypes[0] != networkv1.PolicyTypeEgress || len(value.DenyEgress.Spec.Egress) != 0 || value.DenyEgress.Spec.PodSelector.MatchLabels["astronomer.io/database-role"] != "scan" {
					t.Fatal("scan egress denial changed")
				}
				if prep.InitContainers[0].Args[0] != "--initialize-material" || prep.InitContainers[1].Args[0] != "--bootstrap-material" || prep.InitContainers[2].Args[0] != "--initialize-input" {
					t.Fatal("private material initialization order changed")
				}
				stage := prep.Containers[0]
				temporary := false
				for _, mount := range stage.VolumeMounts {
					if mount.Name == "temporary" {
						temporary = mount.MountPath == "/tmp" && !mount.ReadOnly
					}
				}
				if !temporary || !*stage.SecurityContext.ReadOnlyRootFilesystem {
					t.Fatal("preparation private trust snapshots lack bounded writable temporary space")
				}
				for _, key := range []string{"TMPDIR", "HOME"} {
					found := false
					for _, env := range stage.Env {
						if env.Name == key {
							found = env.Value == "/tmp"
						}
					}
					if !found {
						t.Fatal("preparation temporary location depends on ambient image settings")
					}
				}
				for _, volume := range prep.Volumes {
					if volume.Name == "temporary" && (volume.EmptyDir == nil || volume.EmptyDir.SizeLimit == nil || volume.EmptyDir.SizeLimit.String() != "2Gi") {
						t.Fatal("preparation temporary volume is unbounded")
					}
				}
				if len(scan.InitContainers) != 3 || scan.InitContainers[1].RestartPolicy == nil || *scan.InitContainers[1].RestartPolicy != corev1.ContainerRestartPolicyAlways || scan.InitContainers[2].Args[0] != "--await-prepared" {
					t.Fatal("native final observer/readiness ordering changed")
				}
				for _, container := range append(append([]corev1.Container(nil), scan.InitContainers...), scan.Containers...) {
					for _, mount := range container.VolumeMounts {
						if mount.Name == "authority" || mount.Name == "projected" {
							t.Fatal("scan Pod gained preparation material")
						}
					}
					if len(container.Env) != 0 && container.Name != "scanner" {
						t.Fatal("observer gained process configuration")
					}
				}
				scanner := scan.Containers[0]
				if len(scanner.VolumeMounts) != 2 || scanner.VolumeMounts[0].Name != "input" || !scanner.VolumeMounts[0].ReadOnly || scanner.VolumeMounts[1].Name != "temporary" {
					t.Fatal("scanner gained writable input or coordination")
				}
				for _, volume := range scan.Volumes {
					if volume.Secret != nil || volume.Projected != nil || volume.HostPath != nil {
						t.Fatal("scan Pod gained credential or host volume")
					}
				}
				if !scan.Volumes[0].PersistentVolumeClaim.ReadOnly {
					t.Fatal("PVC not enforced readonly")
				}
				if *scanner.SecurityContext.RunAsUser != JobProcessUID || !*scanner.SecurityContext.RunAsNonRoot || *scanner.SecurityContext.AllowPrivilegeEscalation || !*scanner.SecurityContext.ReadOnlyRootFilesystem || len(scanner.SecurityContext.Capabilities.Add) != 0 {
					t.Fatal("scanner privilege changed")
				}
				if scanner.Command[0] != "/usr/local/bin/trivy" || !containsArgument(scanner.Args, "--skip-db-update") || !containsArgument(scanner.Args, "--skip-java-db-update") || !containsArgument(scanner.Args, "--offline-scan") || !containsArgument(scanner.Args, "--disable-telemetry") {
					t.Fatal("scanner can refresh remotely")
				}
				wantChild := map[string]string{"amd64": "3", "arm64": "4"}[architecture]
				if scanner.Image != "registry.example.test/scanner@sha256:"+strings.Repeat(wantChild, 64) || strings.Contains(scanner.Image, ":latest") {
					t.Fatal("scanner uses mutable/index image")
				}
				for _, container := range prep.InitContainers {
					if container.Command[0] != "/usr/local/bin/database-cache-helper" {
						t.Fatal("helper does not use candidate image executable")
					}
				}
				// Caller mutation must not alter constructed API objects.
				original := prep.Containers[0].Env[0].Value
				request.PreparationEnvironment[0].Value = "changed"
				images.Helper.Platforms[0].Digest = "changed"
				if value.Preparation.Spec.Template.Spec.Containers[0].Env[0].Value != original {
					t.Fatal("job retained mutable caller environment")
				}
			})
		}
	}
}
func containsArgument(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestOwnedScanJobsRejectMutableAndIncompleteCurrentData(t *testing.T) {
	mutations := map[string]func(*JobRequest, *distribution.DatabaseScanImages){
		"claim":             func(r *JobRequest, _ *distribution.DatabaseScanImages) { r.ClaimID = uuid.Nil },
		"foreign-kubelet-secret":func(r *JobRequest,_ *distribution.DatabaseScanImages){r.ImagePullSecret="foreign-secret"},
		"mutable-workload":  func(r *JobRequest, _ *distribution.DatabaseScanImages) { r.WorkloadDigest = "latest" },
		"tagged-repository": func(r *JobRequest, _ *distribution.DatabaseScanImages) { r.WorkloadRepository += ":latest" },
		"credential-url": func(r *JobRequest, _ *distribution.DatabaseScanImages) {
			r.WorkloadRepository = "https://user:fixture@registry.example.test/workload"
		},
		"architecture": func(r *JobRequest, _ *distribution.DatabaseScanImages) { r.Architecture = "386" },
		"node":         func(r *JobRequest, _ *distribution.DatabaseScanImages) { r.NodeName = "wrong/node" },
		"image-tag": func(_ *JobRequest, i *distribution.DatabaseScanImages) {
			i.Helper.Reference = "registry.example.test/helper:latest"
		},
		"image-digest": func(_ *JobRequest, i *distribution.DatabaseScanImages) {
			i.Scanner.Reference = "registry.example.test/scanner@sha256:../other"
		},
		"missing-platform": func(_ *JobRequest, i *distribution.DatabaseScanImages) { i.Helper.Platforms = i.Helper.Platforms[:1] },
		"repeated-platform": func(_ *JobRequest, i *distribution.DatabaseScanImages) {
			i.Scanner.Platforms[1] = i.Scanner.Platforms[0]
		},
		"changed-platform": func(_ *JobRequest, i *distribution.DatabaseScanImages) { i.Helper.Platforms[0].Digest = "sha256:wrong" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			r, i := ownedJobFixture(false)
			mutate(&r, &i)
			v, err := buildScanJobs(r, i)
			if err == nil || !reflect.DeepEqual(v, ScanJobs{}) {
				t.Fatal("invalid data constructed mutating objects")
			}
		})
	}
	for _, args := range []struct {
		ctx       context.Context
		authority *distribution.DatabaseAuthority
		proof     *distribution.VerifiedDatabaseAuthorityBase
	}{{}, {ctx: context.Background()}} {
		r, _ := ownedJobFixture(false)
		if _, err := BuildScanJobs(args.ctx, args.authority, args.proof, r); err == nil {
			t.Fatal("missing opaque authority accepted")
		}
	}
}

func TestOwnedPreparationEnvironmentRequiresClosedPublicAndPrivateScopes(t *testing.T) {
	mutations := map[string]func(*JobRequest){
		"missing-fixed": func(r *JobRequest) { r.PreparationEnvironment = r.PreparationEnvironment[1:] },
		"duplicate": func(r *JobRequest) {
			r.PreparationEnvironment = append(r.PreparationEnvironment, r.PreparationEnvironment[0])
		},
		"arbitrary-secret": func(r *JobRequest) {
			r.PreparationEnvironment = append(r.PreparationEnvironment, corev1.EnvVar{Name: "DATABASE_URL", Value: "not-allowed"})
		},
		"secret-reference": func(r *JobRequest) {
			r.PreparationEnvironment[0].ValueFrom = &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{Key: "secret"}}
		},
		"changed-fixed": func(r *JobRequest) { r.PreparationEnvironment[0].Value = "false" },
		"unbounded":     func(r *JobRequest) { r.PreparationEnvironment[9].Value = strings.Repeat("x", 32769) },
		"unowned-temp": func(r *JobRequest) {
			for index := range r.PreparationEnvironment {
				if r.PreparationEnvironment[index].Name == "TMPDIR" {
					r.PreparationEnvironment[index].Value = "/arbitrary"
				}
			}
		},
		"control":         func(r *JobRequest) { r.PreparationEnvironment[9].Value = "bad\nvalue" },
		"unexpected-pull": func(r *JobRequest) { r.PrivatePull = false },
		"missing-private": func(r *JobRequest) {
			r.PreparationEnvironment = r.PreparationEnvironment[:len(r.PreparationEnvironment)-1]
		},
		"changed-pull-path": func(r *JobRequest) { r.PreparationEnvironment[len(r.PreparationEnvironment)-5].Value = "other.json" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			r, i := ownedJobFixture(true)
			mutate(&r)
			if _, err := buildScanJobs(r, i); err == nil {
				t.Fatal("preparation escaped constructor scope")
			}
		})
	}
	r, i := ownedJobFixture(false)
	r.PrivatePull = true
	if _, err := buildScanJobs(r, i); err == nil {
		t.Fatal("private preparation silently anonymous")
	}
}

func TestOwnedScanJobsBindMappedReleaseToExactBootstrapAndTypedPath(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "anonymous", true: "private"}[private], func(t *testing.T) {
			request, images := ownedJobFixture(private)
			request.MappedRelease = true
			request.PreparationEnvironment = append(request.PreparationEnvironment, corev1.EnvVar{Name: "RELEASE_MIRROR_MAPPING_PATH", Value: PrivateJobMaterialRoot + "/release-mapping.json"})
			jobs, err := buildScanJobs(request, images)
			if err != nil {
				t.Fatal(err)
			}
			args := jobs.Preparation.Spec.Template.Spec.InitContainers[1].Args
			if !containsArgument(args, "--mapped-release") || containsArgument(args, "--private-pull") != private {
				t.Fatal("closed mapped bootstrap selector diverged")
			}
			found := false
			for _, env := range jobs.Preparation.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "RELEASE_MIRROR_MAPPING_PATH" {
					found = env.Value == PrivateJobMaterialRoot+"/release-mapping.json"
				}
				if env.Name == "DISTRIBUTION_MIRROR_KEY_REF" && env.Value != "mirror" {
					t.Fatal("canonical public-key basename changed")
				}
				if env.Name == "DELIVERY_DATABASE_REGISTRY_ROOTS_REF" && env.Value != "registry" {
					t.Fatal("canonical roots basename changed")
				}
				if env.Name == "DELIVERY_DATABASE_READ_CREDENTIAL_REF" && env.Value != "pull" {
					t.Fatal("canonical private credential basename changed")
				}
			}
			if !found {
				t.Fatal("config.Load would lose the exact constructor-selected mapping")
			}
			request.PreparationEnvironment[len(request.PreparationEnvironment)-1].Value = "changed"
			if jobs.Preparation.Spec.Template.Spec.Containers[0].Env[len(request.PreparationEnvironment)-1].Value != PrivateJobMaterialRoot+"/release-mapping.json" {
				t.Fatal("mapped Job retained caller environment storage")
			}
		})
	}
	for _, name := range []string{"missing-mapping", "unexpected-mapping", "changed-mapping", "duplicate-mapping", "mirror-double-suffix", "roots-double-suffix", "pull-double-suffix"} {
		t.Run(name, func(t *testing.T) {
			request, images := ownedJobFixture(true)
			request.MappedRelease = true
			request.PreparationEnvironment = append(request.PreparationEnvironment, corev1.EnvVar{Name: "RELEASE_MIRROR_MAPPING_PATH", Value: PrivateJobMaterialRoot + "/release-mapping.json"})
			switch name {
			case "missing-mapping":
				request.PreparationEnvironment = request.PreparationEnvironment[:len(request.PreparationEnvironment)-1]
			case "unexpected-mapping":
				request.MappedRelease = false
			case "changed-mapping":
				request.PreparationEnvironment[len(request.PreparationEnvironment)-1].Value = "/unowned/mapping.json"
			case "duplicate-mapping":
				request.PreparationEnvironment = append(request.PreparationEnvironment, request.PreparationEnvironment[len(request.PreparationEnvironment)-1])
			default:
				target, suffix := "DISTRIBUTION_MIRROR_KEY_REF", ".pub"
				if name == "roots-double-suffix" {
					target, suffix = "DELIVERY_DATABASE_REGISTRY_ROOTS_REF", ".pem"
				}
				if name == "pull-double-suffix" {
					target, suffix = "DELIVERY_DATABASE_READ_CREDENTIAL_REF", ".json"
				}
				for index := range request.PreparationEnvironment {
					if request.PreparationEnvironment[index].Name == target {
						request.PreparationEnvironment[index].Value += suffix
					}
				}
			}
			if jobs, err := buildScanJobs(request, images); err == nil || !reflect.DeepEqual(jobs, ScanJobs{}) {
				t.Fatal("invalid mapped scope constructed Jobs")
			}
		})
	}
	// Neither the optional environment nor flag is added to the legacy shape.
	request, images := ownedJobFixture(false)
	jobs, err := buildScanJobs(request, images)
	if err != nil {
		t.Fatal(err)
	}
	if containsArgument(jobs.Preparation.Spec.Template.Spec.InitContainers[1].Args, "--mapped-release") {
		t.Fatal("legacy bootstrap implicitly enabled mapped material")
	}
	for _, value := range jobs.Preparation.Spec.Template.Spec.Containers[0].Env {
		if value.Name == "RELEASE_MIRROR_MAPPING_PATH" {
			t.Fatal("legacy environment unexpectedly selected mapping")
		}
	}
}
