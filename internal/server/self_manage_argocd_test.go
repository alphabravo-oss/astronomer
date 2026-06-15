package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestMergeSelfManagedValuesPreservesDesiredReplicas(t *testing.T) {
	current := `
server:
  replicaCount: 2
worker:
  replicaCount: 2
frontend:
  enabled: true
  replicaCount: 2
  image:
    repository: astronomer-frontend
    tag: old
image:
  server:
    repository: astronomer-go-server
    tag: old
config:
  corsAllowedOrigins: http://old.example
tls:
  source: none
bootstrap:
  username: old-admin
  email: old@example.com
  password: old-bootstrap
`
	bootstrap := `
server:
  replicaCount: 1
worker:
  replicaCount: 1
frontend:
  enabled: true
  replicaCount: 1
  image:
    repository: astronomer-frontend
    tag: new
image:
  server:
    repository: astronomer-go-server
    tag: new
config:
  corsAllowedOrigins: http://new.example
gateway:
  enabled: true
tls:
  source: secret
  secretName: astronomer-tls
bootstrap:
  username: admin
  email: admin@alphabravo.io
  password: new-bootstrap
`

	mergedYAML, err := mergeSelfManagedValues(current, bootstrap)
	if err != nil {
		t.Fatalf("mergeSelfManagedValues returned error: %v", err)
	}

	var merged map[string]any
	if err := yaml.Unmarshal([]byte(mergedYAML), &merged); err != nil {
		t.Fatalf("unmarshal merged values: %v", err)
	}

	serverValues := merged["server"].(map[string]any)
	if got := serverValues["replicaCount"]; got != float64(2) && got != 2 {
		t.Fatalf("server replicaCount = %v, want 2", got)
	}
	workerValues := merged["worker"].(map[string]any)
	if got := workerValues["replicaCount"]; got != float64(2) && got != 2 {
		t.Fatalf("worker replicaCount = %v, want 2", got)
	}
	frontendValues := merged["frontend"].(map[string]any)
	if got := frontendValues["replicaCount"]; got != float64(2) && got != 2 {
		t.Fatalf("frontend replicaCount = %v, want 2", got)
	}
	frontendImage := frontendValues["image"].(map[string]any)
	if got := frontendImage["tag"]; got != "new" {
		t.Fatalf("frontend.image.tag = %v, want new", got)
	}
	configValues := merged["config"].(map[string]any)
	if got := configValues["corsAllowedOrigins"]; got != "http://new.example" {
		t.Fatalf("config.corsAllowedOrigins = %v, want updated bootstrap value", got)
	}
	imageValues := merged["image"].(map[string]any)
	serverImage := imageValues["server"].(map[string]any)
	if got := serverImage["tag"]; got != "new" {
		t.Fatalf("image.server.tag = %v, want new", got)
	}
	if _, ok := merged["gateway"]; !ok {
		t.Fatalf("gateway values not preserved from bootstrap set")
	}
	tlsValues := merged["tls"].(map[string]any)
	if got := tlsValues["source"]; got != "secret" {
		t.Fatalf("tls.source = %v, want secret", got)
	}
	if got := tlsValues["secretName"]; got != "astronomer-tls" {
		t.Fatalf("tls.secretName = %v, want astronomer-tls", got)
	}
	bootstrapValues := merged["bootstrap"].(map[string]any)
	if got := bootstrapValues["password"]; got != "new-bootstrap" {
		t.Fatalf("bootstrap.password = %v, want new-bootstrap", got)
	}
	if got := bootstrapValues["email"]; got != "admin@alphabravo.io" {
		t.Fatalf("bootstrap.email = %v, want admin@alphabravo.io", got)
	}
}

func TestSelfManagedPublicListenerValuesMirrorsExistingIngress(t *testing.T) {
	className := "nginx"
	client := fake.NewClientset(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      localAstronomerReleaseName,
			Namespace: localAstronomerNamespace,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{
				{Host: "astronomer.dev.alphabravo.io"},
			},
			TLS: []networkingv1.IngressTLS{
				{SecretName: "astronomer-tls"},
			},
		},
	})

	values, err := selfManagedPublicListenerValues(context.Background(), client, "fallback.example")
	if err != nil {
		t.Fatalf("selfManagedPublicListenerValues returned error: %v", err)
	}

	ingressValues := values["ingress"].(map[string]any)
	if got := ingressValues["enabled"]; got != true {
		t.Fatalf("ingress.enabled = %v, want true", got)
	}
	if got := ingressValues["className"]; got != "nginx" {
		t.Fatalf("ingress.className = %v, want nginx", got)
	}
	if got := ingressValues["host"]; got != "astronomer.dev.alphabravo.io" {
		t.Fatalf("ingress.host = %v, want astronomer.dev.alphabravo.io", got)
	}
	gatewayValues := values["gateway"].(map[string]any)
	if got := gatewayValues["enabled"]; got != false {
		t.Fatalf("gateway.enabled = %v, want false", got)
	}
	tlsValues := values["tls"].(map[string]any)
	if got := tlsValues["source"]; got != "secret" {
		t.Fatalf("tls.source = %v, want secret", got)
	}
	if got := tlsValues["secretName"]; got != "astronomer-tls" {
		t.Fatalf("tls.secretName = %v, want astronomer-tls", got)
	}
}

func TestSelfManagedPublicListenerValuesFallsBackToGateway(t *testing.T) {
	client := fake.NewClientset()

	values, err := selfManagedPublicListenerValues(context.Background(), client, "astronomer.example")
	if err != nil {
		t.Fatalf("selfManagedPublicListenerValues returned error: %v", err)
	}

	ingressValues := values["ingress"].(map[string]any)
	if got := ingressValues["enabled"]; got != false {
		t.Fatalf("ingress.enabled = %v, want false", got)
	}
	gatewayValues := values["gateway"].(map[string]any)
	if got := gatewayValues["enabled"]; got != true {
		t.Fatalf("gateway.enabled = %v, want true", got)
	}
	if got := gatewayValues["className"]; got != "nginx" {
		t.Fatalf("gateway.className = %v, want nginx", got)
	}
	hosts := gatewayValues["hosts"].([]string)
	if len(hosts) != 1 || hosts[0] != "astronomer.example" {
		t.Fatalf("gateway.hosts = %v, want [astronomer.example]", hosts)
	}
}

func TestBuildSelfManagedAstronomerValuesWritesFrontendImageToChartPath(t *testing.T) {
	className := "nginx"
	replicas := int32(1)
	client := fake.NewClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      localAstronomerReleaseName + "-secrets",
				Namespace: localAstronomerNamespace,
			},
			Data: map[string][]byte{
				"SECRET_KEY":                []byte("secret-key"),
				"ASTRONOMER_ENCRYPTION_KEY": []byte("encryption-key"),
				"POSTGRES_PASSWORD":         []byte("postgres"),
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      localAstronomerReleaseName + "-bootstrap",
				Namespace: localAstronomerNamespace,
			},
			Data: map[string][]byte{"password": []byte("bootstrap-password")},
		},
		&networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      localAstronomerReleaseName,
				Namespace: localAstronomerNamespace,
			},
			Spec: networkingv1.IngressSpec{
				IngressClassName: &className,
				Rules:            []networkingv1.IngressRule{{Host: "astronomer.dev.alphabravo.io"}},
				TLS:              []networkingv1.IngressTLS{{SecretName: "astronomer-tls"}},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: localAstronomerReleaseName + "-server", Namespace: localAstronomerNamespace},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{{Name: "migrate", Image: "astronomer-go-migrate:migrate-tag"}},
					Containers:     []corev1.Container{{Name: "server", Image: "astronomer-go-server:server-tag"}},
				}},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: localAstronomerReleaseName + "-worker", Namespace: localAstronomerNamespace},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "worker", Image: "astronomer-go-worker:worker-tag"}},
				}},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: localAstronomerReleaseName + "-frontend", Namespace: localAstronomerNamespace},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "frontend", Image: "astronomer-frontend:f03fcf5"}},
				}},
			},
		},
	)

	valuesYAML, err := buildSelfManagedAstronomerValues(context.Background(), &config.Config{
		AgentImageRepository: "astronomer-go-agent",
		AgentImageTag:        "agent-tag",
	}, client, "https://astronomer.dev.alphabravo.io")
	if err != nil {
		t.Fatalf("buildSelfManagedAstronomerValues returned error: %v", err)
	}

	var values map[string]any
	if err := yaml.Unmarshal([]byte(valuesYAML), &values); err != nil {
		t.Fatalf("unmarshal values: %v", err)
	}
	frontendValues := values["frontend"].(map[string]any)
	frontendImage := frontendValues["image"].(map[string]any)
	if got := frontendImage["repository"]; got != "astronomer-frontend" {
		t.Fatalf("frontend.image.repository = %v, want astronomer-frontend", got)
	}
	if got := frontendImage["tag"]; got != "f03fcf5" {
		t.Fatalf("frontend.image.tag = %v, want f03fcf5", got)
	}
	imageValues := values["image"].(map[string]any)
	if _, ok := imageValues["frontend"]; ok {
		t.Fatalf("image.frontend should not be set; frontend chart reads frontend.image")
	}
	bootstrapValues := values["bootstrap"].(map[string]any)
	if got := bootstrapValues["password"]; got != "bootstrap-password" {
		t.Fatalf("bootstrap.password = %v, want bootstrap-password", got)
	}
	if got := bootstrapValues["username"]; got != "admin" {
		t.Fatalf("bootstrap.username = %v, want admin", got)
	}
	if got := bootstrapValues["email"]; got != "admin@astronomer.local" {
		t.Fatalf("bootstrap.email = %v, want admin@astronomer.local", got)
	}
}

func TestLocalArgoManagedClusterLabelsIncludesStandardSelectors(t *testing.T) {
	clusterID := uuid.New()
	cluster := sqlc.Cluster{
		ID:                clusterID,
		Name:              "local",
		Environment:       "production",
		Region:            "us-east-1",
		Provider:          "local",
		Distribution:      "k3s",
		AgentVersion:      "v0.4.1",
		KubernetesVersion: "v1.29.3+k3s1",
		Annotations:       json.RawMessage(`{"astronomer.io/agent-privilege-profile":"operator"}`),
	}

	projectID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	labels := localArgoClusterSecretLabelsForProjects(cluster, []sqlc.Project{{ID: projectID, Name: "platform"}})
	want := map[string]string{
		"argocd.argoproj.io/secret-type":                 "cluster",
		"astronomer.io/managed-by":                       "astronomer",
		"astronomer.io/cluster-id":                       clusterID.String(),
		"astronomer.io/cluster-name":                     "local",
		"astronomer.io/environment":                      "production",
		"astronomer.io/is-local":                         "true",
		"astronomer.io/region":                           "us-east-1",
		"astronomer.io/provider":                         "local",
		"astronomer.io/distribution":                     "k3s",
		"astronomer.io/agent-privilege-profile":          "operator",
		"astronomer.io/agent-version":                    "v0.4.1",
		"astronomer.io/kubernetes-version":               "v1.29.3-k3s1",
		"astronomer.io/project":                          "platform",
		"astronomer.io/project-id":                       projectID.String(),
		"astronomer.io/project.platform":                 "true",
		"astronomer.io/project-id." + projectID.String(): "true",
	}
	if len(labels) != len(want) {
		t.Fatalf("label count = %d (%v), want %d (%v)", len(labels), labels, len(want), want)
	}
	for k, v := range want {
		if got := labels[k]; got != v {
			t.Fatalf("labels[%q] = %q, want %q (full=%v)", k, got, v, labels)
		}
	}

	rowLabelsJSON, err := json.Marshal(localArgoManagedClusterLabelsForProjects(cluster, []sqlc.Project{{ID: projectID, Name: "platform"}}))
	if err != nil {
		t.Fatalf("marshal row labels: %v", err)
	}
	var rowLabels map[string]string
	if err := json.Unmarshal(rowLabelsJSON, &rowLabels); err != nil {
		t.Fatalf("unmarshal row labels: %v", err)
	}
	if _, ok := rowLabels["argocd.argoproj.io/secret-type"]; ok {
		t.Fatalf("row labels must not contain ArgoCD's secret type marker: %v", rowLabels)
	}
	if got := rowLabels["astronomer.io/distribution"]; got != "k3s" {
		t.Fatalf("row distribution label = %q, want k3s", got)
	}
	if got := rowLabels["astronomer.io/kubernetes-version"]; got != "v1.29.3-k3s1" {
		t.Fatalf("row kubernetes-version label = %q, want v1.29.3-k3s1", got)
	}
	if got := rowLabels["astronomer.io/project-id"]; got != projectID.String() {
		t.Fatalf("row project-id label = %q, want %s", got, projectID.String())
	}
}
