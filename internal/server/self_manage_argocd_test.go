package server

import (
	"context"
	"encoding/json"
	"reflect"
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

func TestParseImageRefForSelfManagedHelmValues(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		registry   string
		repository string
		tag        string
	}{
		{name: "local registry name", ref: "localastro/server:v1", registry: "localastro", repository: "server", tag: "v1"},
		{name: "host and namespace", ref: "ghcr.io/org/server:v2", registry: "ghcr.io/org", repository: "server", tag: "v2"},
		{name: "registry port and nested path", ref: "registry.example:5000/team/platform/server:v3", registry: "registry.example:5000/team/platform", repository: "server", tag: "v3"},
		{name: "bare repository", ref: "server:v4", registry: "", repository: "server", tag: "v4"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseImageRef(tc.ref)
			if err != nil {
				t.Fatalf("parseImageRef(%q): %v", tc.ref, err)
			}
			if got["registry"] != tc.registry || got["repository"] != tc.repository || got["tag"] != tc.tag {
				t.Fatalf("parseImageRef(%q) = %#v, want registry=%q repository=%q tag=%q", tc.ref, got, tc.registry, tc.repository, tc.tag)
			}
		})
	}
}

func TestParseImageRefRejectsLossyReferences(t *testing.T) {
	for _, ref := range []string{
		"ghcr.io/org/server@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"ghcr.io/org/server:v1@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"ghcr.io/org/server",
		"registry.example:5000/team/server:",
		"/server:v1",
		"ghcr.io//server:v1",
		":v1",
	} {
		t.Run(ref, func(t *testing.T) {
			if _, err := parseImageRef(ref); err == nil {
				t.Fatalf("parseImageRef(%q) succeeded; want explicit error", ref)
			}
		})
	}
}

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
  existingSecret: astronomer-bootstrap
  existingSecretKey: password
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
	if got := bootstrapValues["existingSecret"]; got != "astronomer-bootstrap" {
		t.Fatalf("bootstrap.existingSecret = %v, want astronomer-bootstrap", got)
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

func TestBuildSelfManagedAstronomerValuesDecomposesDistinctImageRegistries(t *testing.T) {
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
				"DATABASE_URL":              []byte("postgres://reference"),
				"REDIS_URL":                 []byte("redis://reference"),
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
					InitContainers: []corev1.Container{{Name: "migrate", Image: "registry.example:5000/platform/migrate:migrate-tag"}},
					Containers: []corev1.Container{{
						Name:  "server",
						Image: "localastro/astronomer-go-server:server-tag",
						EnvFrom: []corev1.EnvFromSource{{
							SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: localAstronomerReleaseName + "-secrets"}},
						}},
						Env: []corev1.EnvVar{
							{Name: "ASTRONOMER_BOOTSTRAP_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: localAstronomerReleaseName + "-bootstrap"}, Key: "password"}}},
							{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: localAstronomerReleaseName + "-secrets"}, Key: "DATABASE_URL"}}},
							{Name: "REDIS_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: localAstronomerReleaseName + "-secrets"}, Key: "REDIS_URL"}}},
						},
					}},
				}},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: localAstronomerReleaseName + "-worker", Namespace: localAstronomerNamespace},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "worker", Image: "ghcr.io/acme/astronomer-go-worker:worker-tag"}},
				}},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: localAstronomerReleaseName + "-frontend", Namespace: localAstronomerNamespace},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "frontend", Image: "frontend.registry:5443/nested/astronomer-frontend:f03fcf5"}},
				}},
			},
		},
	)
	if err := client.Tracker().Add(helmReleaseSecretFixture(t, 7, "deployed", map[string]any{
		"image":     map[string]any{"registry": ""},
		"config":    map[string]any{"agentImageRepository": "stale/agent", "agentImageTag": "stale"},
		"bootstrap": map[string]any{"username": "admin", "email": "admin@astronomer.local"},
	})); err != nil {
		t.Fatal(err)
	}

	valuesYAML, err := buildSelfManagedAstronomerValues(context.Background(), &config.Config{
		AgentImageRepository: "agents.registry:5001/team/astronomer-go-agent",
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
	if got := frontendImage["registry"]; got != "frontend.registry:5443/nested" {
		t.Fatalf("frontend.image.registry = %v, want frontend.registry:5443/nested", got)
	}
	if got := frontendImage["repository"]; got != "astronomer-frontend" {
		t.Fatalf("frontend.image.repository = %v, want astronomer-frontend", got)
	}
	if got := frontendImage["tag"]; got != "f03fcf5" {
		t.Fatalf("frontend.image.tag = %v, want f03fcf5", got)
	}
	imageValues := values["image"].(map[string]any)
	if got := imageValues["registry"]; got != "" {
		t.Fatalf("image.registry = %v, want explicit empty global registry", got)
	}
	assertSelfManagedImageValues(t, imageValues["server"], "localastro", "astronomer-go-server", "server-tag")
	assertSelfManagedImageValues(t, imageValues["worker"], "ghcr.io/acme", "astronomer-go-worker", "worker-tag")
	assertSelfManagedImageValues(t, imageValues["migrate"], "registry.example:5000/platform", "migrate", "migrate-tag")
	assertSelfManagedImageValues(t, imageValues["agent"], "agents.registry:5001/team", "astronomer-go-agent", "agent-tag")
	if _, ok := imageValues["frontend"]; ok {
		t.Fatalf("image.frontend should not be set; frontend chart reads frontend.image")
	}
	bootstrapValues := values["bootstrap"].(map[string]any)
	if got := bootstrapValues["existingSecret"]; got != localAstronomerReleaseName+"-bootstrap" {
		t.Fatalf("bootstrap.existingSecret = %v", got)
	}
	if got := bootstrapValues["username"]; got != "admin" {
		t.Fatalf("bootstrap.username = %v, want admin", got)
	}
	if got := bootstrapValues["email"]; got != "admin@astronomer.local" {
		t.Fatalf("bootstrap.email = %v, want admin@astronomer.local", got)
	}
	configValues := values["config"].(map[string]any)
	if got := configValues["agentImageRepository"]; got != "agents.registry:5001/team/astronomer-go-agent" {
		t.Fatalf("config.agentImageRepository = %v", got)
	}
	imageValues["server"].(map[string]any)["tag"] = "server-upgraded"
	upgradedSource, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	secondYAML, err := buildSelfManagedAstronomerValues(context.Background(), &config.Config{
		AgentImageRepository: "agents.registry:5001/team/astronomer-go-agent", AgentImageTag: "agent-tag",
	}, client, "https://astronomer.dev.alphabravo.io", string(upgradedSource))
	if err != nil {
		t.Fatalf("second fixed-point build: %v", err)
	}
	second := unmarshalSelfManagedValues(t, secondYAML)
	if got := second["image"].(map[string]any)["server"].(map[string]any)["tag"]; got != "server-upgraded" {
		t.Fatalf("reference-only current Application image upgrade reverted to live/Helm source: %v", got)
	}
}

func TestSelfManagedImageValuesAreAReconcileFixedPoint(t *testing.T) {
	refs := map[string]string{
		"server":  "localastro/astronomer-go-server:v1",
		"worker":  "ghcr.io/acme/astronomer-go-worker:v2",
		"migrate": "registry.example:5000/team/migrate:v3",
		"agent":   "mirror.example/nested/team/agent:v4",
	}
	frontendRef := "frontend.registry:5443/org/astronomer-frontend:v5"
	bootstrap := selfManagedImageBootstrapYAML(t, refs, frontendRef)
	current := `
image:
  registry: stale.example/global
  server:
    repository: stale.example/global/localastro/astronomer-go-server
    tag: v1
frontend:
  replicaCount: 3
  image:
    repository: stale.example/global/frontend.registry/org/astronomer-frontend
    tag: v5
server:
  replicaCount: 3
worker:
  replicaCount: 2
`

	firstYAML, err := mergeSelfManagedValues(current, bootstrap)
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	first := unmarshalSelfManagedValues(t, firstYAML)
	assertRenderedSelfManagedImages(t, first, refs, frontendRef)

	// Model the next reconcile: Kubernetes reports exactly the refs rendered by
	// Helm, those refs are decomposed again, and bootstrap-owned values replace
	// the prior cycle. The image value trees must be an exact fixed point.
	firstImages := first["image"].(map[string]any)
	nextRefs := make(map[string]string, len(refs))
	for component := range refs {
		nextRefs[component] = renderSelfManagedImageForTest(firstImages, firstImages[component])
	}
	firstFrontend := first["frontend"].(map[string]any)["image"]
	nextFrontendRef := renderSelfManagedImageForTest(firstImages, firstFrontend)
	secondBootstrap := selfManagedImageBootstrapYAML(t, nextRefs, nextFrontendRef)
	secondYAML, err := mergeSelfManagedValues(firstYAML, secondBootstrap)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	second := unmarshalSelfManagedValues(t, secondYAML)
	assertRenderedSelfManagedImages(t, second, refs, frontendRef)
	if !reflect.DeepEqual(first["image"], second["image"]) {
		t.Fatalf("image values changed across reconcile:\nfirst=%#v\nsecond=%#v", first["image"], second["image"])
	}
	secondFrontend := second["frontend"].(map[string]any)["image"]
	if !reflect.DeepEqual(firstFrontend, secondFrontend) {
		t.Fatalf("frontend image values changed across reconcile:\nfirst=%#v\nsecond=%#v", firstFrontend, secondFrontend)
	}
}

func assertSelfManagedImageValues(t *testing.T, raw any, registry, repository, tag string) {
	t.Helper()
	image, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("image values = %#v, want map", raw)
	}
	if image["registry"] != registry || image["repository"] != repository || image["tag"] != tag {
		t.Fatalf("image values = %#v, want registry=%q repository=%q tag=%q", image, registry, repository, tag)
	}
}

func selfManagedImageBootstrapYAML(t *testing.T, refs map[string]string, frontendRef string) string {
	t.Helper()
	images := map[string]any{"registry": ""}
	for component, ref := range refs {
		parsed, err := parseImageRef(ref)
		if err != nil {
			t.Fatalf("parse %s image %q: %v", component, ref, err)
		}
		images[component] = parsed
	}
	frontend, err := parseImageRef(frontendRef)
	if err != nil {
		t.Fatalf("parse frontend image %q: %v", frontendRef, err)
	}
	raw, err := yaml.Marshal(map[string]any{
		"image": images,
		"frontend": map[string]any{
			"enabled": true,
			"image":   frontend,
		},
	})
	if err != nil {
		t.Fatalf("marshal bootstrap values: %v", err)
	}
	return string(raw)
}

func unmarshalSelfManagedValues(t *testing.T, raw string) map[string]any {
	t.Helper()
	values := map[string]any{}
	if err := yaml.Unmarshal([]byte(raw), &values); err != nil {
		t.Fatalf("unmarshal values: %v", err)
	}
	return values
}

func assertRenderedSelfManagedImages(t *testing.T, values map[string]any, refs map[string]string, frontendRef string) {
	t.Helper()
	images := values["image"].(map[string]any)
	if images["registry"] != "" {
		t.Fatalf("global image registry = %v, want explicit empty", images["registry"])
	}
	for component, want := range refs {
		if got := renderSelfManagedImageForTest(images, images[component]); got != want {
			t.Fatalf("rendered %s image = %q, want %q", component, got, want)
		}
	}
	frontend := values["frontend"].(map[string]any)["image"]
	if got := renderSelfManagedImageForTest(images, frontend); got != frontendRef {
		t.Fatalf("rendered frontend image = %q, want %q", got, frontendRef)
	}
}

func renderSelfManagedImageForTest(global, raw any) string {
	globalValues := global.(map[string]any)
	image := raw.(map[string]any)
	registry, _ := globalValues["registry"].(string)
	if registry == "" {
		registry, _ = image["registry"].(string)
	}
	prefix := ""
	if registry != "" {
		prefix = registry + "/"
	}
	return prefix + image["repository"].(string) + ":" + image["tag"].(string)
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
