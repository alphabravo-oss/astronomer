package charlie

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	agentNamespaceName  = "astronomer-charlie"
	agentReleaseName    = "charlie-agent"
	bootstrapSecret     = "charlie-agent-bootstrap"
	enrollmentSecret    = "charlie-agent-enrollment"
	bridgeTLSSecret     = "charlie-agent-bridge-tls"
	mcpTLSSecret        = "charlie-agent-mcp-tls"
	centralCASecret     = "charlie-agent-central-ca"
	artifactPullSecret  = "charlie-artifact-pull"
	productMCPSecret    = "astronomer-charlie-mcp-tls"
	productBridgeSecret = "astronomer-charlie-bridge-client-tls"
	mcpServiceName      = "astronomer-charlie-mcp"
	productNetworkName  = "astronomer-charlie-product"
	bridgeEgressName    = "astronomer-charlie-bridge-egress"
	mcpPort             = 7444
	bridgePort          = 7443
)

// AgentInstaller materializes the dormant Charlie agent after a package is
// consumed. Astronomer's Helm chart never pulls this workload.
type AgentInstaller interface {
	Prepare(context.Context) error
	Activate(context.Context, ActivationRequest) error
}

type ActivationRequest struct {
	Validated        ValidatedOnboarding
	Trust            GeneratedLocalTrust
	InstallationID   string
	ProductNamespace string
}

type HelmReleaseSpec struct {
	ChartRef    string
	ChartDigest string
	Image       string
	ImageDigest string
	Values      map[string]any
	PullUser    string
	PullSecret  string
	ReuseValues bool
}

type HelmReleaser interface {
	Apply(context.Context, HelmReleaseSpec) error
	Uninstall(context.Context) error
}

// AgentDeactivator removes the Charlie agent and product-side integration
// material after disconnect.
type AgentDeactivator interface {
	Deactivate(context.Context) error
}

type KubernetesRuntimeActivator struct {
	client           kubernetes.Interface
	agentNamespace   string
	productNamespace string
	helm             HelmReleaser
}

func NewKubernetesRuntimeActivator(client kubernetes.Interface, productNamespace string, helm HelmReleaser) (*KubernetesRuntimeActivator, error) {
	if client == nil || strings.TrimSpace(productNamespace) == "" {
		return nil, fmt.Errorf("Charlie runtime activation requires a Kubernetes client and product namespace")
	}
	return &KubernetesRuntimeActivator{
		client: client, agentNamespace: agentNamespaceName, productNamespace: productNamespace, helm: helm,
	}, nil
}

func (a *KubernetesRuntimeActivator) Prepare(ctx context.Context) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("Charlie runtime activation is unavailable")
	}
	_, err := a.client.CoreV1().Namespaces().Get(ctx, a.agentNamespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = a.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: a.agentNamespace, Labels: map[string]string{
				"app.kubernetes.io/part-of": "charlie", "app.kubernetes.io/managed-by": "astronomer",
			}},
		}, metav1.CreateOptions{})
	}
	return err
}

func (a *KubernetesRuntimeActivator) Activate(ctx context.Context, request ActivationRequest) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("Charlie runtime activation is unavailable")
	}
	if err := a.Prepare(ctx); err != nil {
		return err
	}
	if err := a.writeTrustSecrets(ctx, request); err != nil {
		return err
	}
	if err := a.applyProductNetwork(ctx); err != nil {
		return err
	}
	if a.helm == nil {
		return nil
	}
	spec, err := helmSpec(request)
	if err != nil {
		return err
	}
	return a.helm.Apply(ctx, spec)
}

func (a *KubernetesRuntimeActivator) Deactivate(ctx context.Context) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("Charlie runtime activation is unavailable")
	}
	if a.helm != nil {
		if err := a.helm.Uninstall(ctx); err != nil {
			return err
		}
	}
	for _, name := range []string{productMCPSecret, productBridgeSecret} {
		if err := deleteIgnoreNotFound(a.client.CoreV1().Secrets(a.productNamespace).Delete(ctx, name, metav1.DeleteOptions{})); err != nil {
			return err
		}
	}
	if err := deleteIgnoreNotFound(a.client.CoreV1().Services(a.productNamespace).Delete(ctx, mcpServiceName, metav1.DeleteOptions{})); err != nil {
		return err
	}
	if err := deleteIgnoreNotFound(a.client.NetworkingV1().NetworkPolicies(a.productNamespace).Delete(ctx, productNetworkName, metav1.DeleteOptions{})); err != nil {
		return err
	}
	if err := deleteIgnoreNotFound(a.client.NetworkingV1().NetworkPolicies(a.productNamespace).Delete(ctx, bridgeEgressName, metav1.DeleteOptions{})); err != nil {
		return err
	}
	return deleteIgnoreNotFound(a.client.CoreV1().Namespaces().Delete(ctx, a.agentNamespace, metav1.DeleteOptions{}))
}

func deleteIgnoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (a *KubernetesRuntimeActivator) writeTrustSecrets(ctx context.Context, request ActivationRequest) error {
	if strings.TrimSpace(request.Trust.Agent.CACertificatePEM) == "" {
		return a.splitExistingBootstrap(ctx)
	}
	if err := applyOpaqueSecret(ctx, a.client, a.agentNamespace, enrollmentSecret, map[string][]byte{
		"onboarding-package.json": append([]byte(nil), request.Validated.RawPackage...),
	}); err != nil {
		return err
	}
	if err := applyOpaqueSecret(ctx, a.client, a.agentNamespace, bridgeTLSSecret, map[string][]byte{
		"tls.crt": []byte(request.Trust.Agent.BridgeServerCertificate),
		"tls.key": []byte(request.Trust.Agent.BridgeServerPrivateKey),
		"ca.crt":  []byte(request.Trust.Agent.CACertificatePEM),
	}); err != nil {
		return err
	}
	if err := applyOpaqueSecret(ctx, a.client, a.agentNamespace, mcpTLSSecret, map[string][]byte{
		"tls.crt": []byte(request.Trust.Agent.MCPClientCertificate),
		"tls.key": []byte(request.Trust.Agent.MCPClientPrivateKey),
		"ca.crt":  []byte(request.Trust.Agent.CACertificatePEM),
	}); err != nil {
		return err
	}
	if err := applyOpaqueSecret(ctx, a.client, a.agentNamespace, centralCASecret, map[string][]byte{
		"ca.crt": []byte(request.Validated.Package.Central.CaBundlePem),
	}); err != nil {
		return err
	}
	if cred := strings.TrimSpace(request.Validated.ArtifactCredential); cred != "" {
		if err := applyDockerconfig(ctx, a.client, a.agentNamespace, artifactPullSecret, imageRegistryHost(request.Validated.Package.Artifact.Image), cred); err != nil {
			return err
		}
	}
	if err := applyOpaqueSecret(ctx, a.client, a.productNamespace, productMCPSecret, map[string][]byte{
		"tls.crt":                   []byte(request.Trust.Public.MCPServerCertificate),
		"tls.key":                   []byte(request.Trust.Astronomer.MCPServerPrivateKey),
		"ca.crt":                    []byte(request.Trust.Public.CACertificatePEM),
		"action-signing-public-key": append([]byte(nil), request.Validated.SigningPublicKey...),
	}); err != nil {
		return err
	}
	return applyOpaqueSecret(ctx, a.client, a.productNamespace, productBridgeSecret, map[string][]byte{
		"tls.crt": []byte(request.Trust.Public.BridgeClientCertificate),
		"tls.key": []byte(request.Trust.Astronomer.BridgeClientPrivateKey),
		"ca.crt":  []byte(request.Trust.Public.CACertificatePEM),
	})
}

func (a *KubernetesRuntimeActivator) splitExistingBootstrap(ctx context.Context) error {
	secret, err := a.client.CoreV1().Secrets(a.agentNamespace).Get(ctx, bootstrapSecret, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	data := secret.Data
	_ = applyOpaqueSecret(ctx, a.client, a.agentNamespace, enrollmentSecret, map[string][]byte{"onboarding-package.json": data["onboarding-package.json"]})
	_ = applyOpaqueSecret(ctx, a.client, a.agentNamespace, bridgeTLSSecret, map[string][]byte{
		"tls.crt": data["bridge-server.crt"], "tls.key": data["bridge-server.key"], "ca.crt": data["ca.crt"],
	})
	_ = applyOpaqueSecret(ctx, a.client, a.agentNamespace, mcpTLSSecret, map[string][]byte{
		"tls.crt": data["mcp-client.crt"], "tls.key": data["mcp-client.key"], "ca.crt": data["ca.crt"],
	})
	return nil
}

func (a *KubernetesRuntimeActivator) applyProductNetwork(ctx context.Context) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: mcpServiceName, Namespace: a.productNamespace,
			Labels: map[string]string{"app.kubernetes.io/name": "astronomer", "app.kubernetes.io/component": "charlie-mcp"},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app.kubernetes.io/name": "astronomer", "app.kubernetes.io/instance": "astronomer",
				"app.kubernetes.io/component": "server",
			},
			Ports: []corev1.ServicePort{{Name: "mcp", Port: mcpPort, TargetPort: intstr.FromInt(mcpPort), Protocol: corev1.ProtocolTCP}},
		},
	}
	if err := applyService(ctx, a.client, svc); err != nil {
		return err
	}
	tcp := corev1.ProtocolTCP
	mcp := intstr.FromInt(mcpPort)
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: productNetworkName, Namespace: a.productNamespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/name": "astronomer", "app.kubernetes.io/instance": "astronomer",
				"app.kubernetes.io/component": "server",
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": a.agentNamespace}},
					PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "charlie-agent"}},
				}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &mcp}},
			}},
		},
	}
	if err := applyNetworkPolicy(ctx, a.client, policy); err != nil {
		return err
	}
	bridge := intstr.FromInt(bridgePort)
	egress := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: bridgeEgressName, Namespace: a.productNamespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/name": "astronomer", "app.kubernetes.io/instance": "astronomer",
				"app.kubernetes.io/component": "server",
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": a.agentNamespace}},
					PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "charlie-agent"}},
				}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &bridge}},
			}},
		},
	}
	return applyNetworkPolicy(ctx, a.client, egress)
}

func helmSpec(request ActivationRequest) (HelmReleaseSpec, error) {
	image := request.Validated.Package.Artifact.Image
	repo, digest, ok := strings.Cut(image, "@")
	if !ok || repo == "" || !strings.HasPrefix(digest, "sha256:") {
		return HelmReleaseSpec{}, fmt.Errorf("Charlie agent image is not digest-pinned")
	}
	identities, err := ExpectedLocalIdentityURIs(request.InstallationID)
	if err != nil {
		return HelmReleaseSpec{}, err
	}
	replicas := request.Validated.Package.ReplicaCount
	if replicas < 2 {
		replicas = 2
	}
	centralCIDRs, err := charlieCentralCIDRs(request.Validated.Package.Central.BaseUrl)
	if err != nil {
		return HelmReleaseSpec{}, err
	}
	return HelmReleaseSpec{
		ChartRef: request.Validated.Package.Artifact.Chart, ChartDigest: request.Validated.Package.Artifact.ChartDigest,
		Image: image, ImageDigest: digest, PullUser: "charlie", PullSecret: request.Validated.ArtifactCredential,
		Values: map[string]any{
			"replicaCount": replicas, "nameOverride": agentReleaseName, "fullnameOverride": agentReleaseName,
			"image": map[string]any{"repository": repo, "digest": digest, "pullPolicy": "IfNotPresent"},
			"runtime": map[string]any{
				"enabled": true, "modeCeiling": "disabled", "emergencyDisabled": false,
				"disclosureAcknowledgement": CapabilityDisclosureDigest(),
			},
			"charlie": map[string]any{
				"baseUrl": request.Validated.Package.Central.BaseUrl, "agentId": string(request.Validated.Package.LogicalAgentId),
				"product": request.Validated.Package.ProductSlug, "environment": string(request.Validated.Package.EnvironmentId),
				"tenant": string(request.Validated.Package.TenantId),
			},
			"bridge": map[string]any{
				"enabled": true, "port": bridgePort,
				"serverNames":              []any{"charlie-agent-bridge.astronomer-charlie.svc"},
				"trustedClientIdentities":  []any{identities.BridgeClient},
				"productNamespaceSelector": map[string]any{"kubernetes.io/metadata.name": request.ProductNamespace},
				"productPodSelector":       map[string]any{"app.kubernetes.io/component": "server"},
			},
			"mcp": map[string]any{
				"host": "astronomer-charlie-mcp." + request.ProductNamespace + ".svc", "port": mcpPort,
				"serverIdentity": identities.MCPServer,
			},
			"existingSecrets": map[string]any{
				"enrollment":   map[string]any{"name": enrollmentSecret, "key": "onboarding-package.json", "replicaCount": replicas, "rolloutChecksum": "v1"},
				"bridgeTLS":    map[string]any{"name": bridgeTLSSecret, "certificateKey": "tls.crt", "privateKeyKey": "tls.key", "clientCAKey": "ca.crt", "rolloutChecksum": "v1"},
				"mcpTLS":       map[string]any{"name": mcpTLSSecret, "certificateKey": "tls.crt", "privateKeyKey": "tls.key", "serverCAKey": "ca.crt", "rolloutChecksum": "v1"},
				"charlieCA":    map[string]any{"name": centralCASecret, "key": "ca.crt", "rolloutChecksum": "v1"},
				"registryPull": map[string]any{"name": artifactPullSecret, "rolloutChecksum": "v1"},
			},
			"networkPolicy": map[string]any{
				"enabled": true,
				"central": map[string]any{"port": 443, "cidrs": centralCIDRs},
				"mcp": map[string]any{
					"namespaceSelector": map[string]any{"kubernetes.io/metadata.name": request.ProductNamespace},
					"podSelector":       map[string]any{"app.kubernetes.io/component": "server"},
				},
			},
		},
	}, nil
}

var lookupCharlieIPs = net.LookupIP

func charlieCentralCIDRs(baseURL string) ([]any, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("Charlie central URL is invalid")
	}
	ips, err := lookupCharlieIPs(parsed.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("Charlie central address could not be resolved")
	}
	seen := map[string]bool{}
	cidrs := make([]any, 0, len(ips))
	for _, ip := range ips {
		suffix := "/32"
		if ip.To4() == nil {
			suffix = "/128"
		}
		value := ip.String() + suffix
		if seen[value] {
			continue
		}
		seen[value] = true
		cidrs = append(cidrs, value)
	}
	return cidrs, nil
}

func applyOpaqueSecret(ctx context.Context, client kubernetes.Interface, namespace, name string, data map[string][]byte) error {
	for key, value := range data {
		if len(value) == 0 {
			return fmt.Errorf("Charlie Secret %s/%s field %s is empty", namespace, name, key)
		}
	}
	secrets := client.CoreV1().Secrets(namespace)
	wanted := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{
			"app.kubernetes.io/managed-by": "astronomer", "app.kubernetes.io/part-of": "charlie",
		}},
		Type: corev1.SecretTypeOpaque, Data: data,
	}
	if _, err := secrets.Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		_, err = secrets.Create(ctx, wanted, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	}
	_, err := secrets.Update(ctx, wanted, metav1.UpdateOptions{})
	return err
}

func applyDockerconfig(ctx context.Context, client kubernetes.Interface, namespace, name, host, password string) error {
	if host == "" || password == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"auths": map[string]any{host: map[string]string{
		"username": "charlie", "password": password,
		"auth": base64.StdEncoding.EncodeToString([]byte("charlie:" + password)),
	}}})
	if err != nil {
		return err
	}
	secrets := client.CoreV1().Secrets(namespace)
	wanted := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{
			"app.kubernetes.io/managed-by": "astronomer", "app.kubernetes.io/part-of": "charlie",
		}},
		Type: corev1.SecretTypeDockerConfigJson, Data: map[string][]byte{corev1.DockerConfigJsonKey: payload},
	}
	if _, err := secrets.Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		_, err = secrets.Create(ctx, wanted, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	}
	_, err = secrets.Update(ctx, wanted, metav1.UpdateOptions{})
	return err
}

func applyService(ctx context.Context, client kubernetes.Interface, svc *corev1.Service) error {
	services := client.CoreV1().Services(svc.Namespace)
	current, err := services.Get(ctx, svc.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = services.Create(ctx, svc, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	svc.ResourceVersion = current.ResourceVersion
	svc.Spec.ClusterIP = current.Spec.ClusterIP
	_, err = services.Update(ctx, svc, metav1.UpdateOptions{})
	return err
}

func applyNetworkPolicy(ctx context.Context, client kubernetes.Interface, policy *networkingv1.NetworkPolicy) error {
	policies := client.NetworkingV1().NetworkPolicies(policy.Namespace)
	if _, err := policies.Get(ctx, policy.Name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		_, err = policies.Create(ctx, policy, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	}
	_, err := policies.Update(ctx, policy, metav1.UpdateOptions{})
	return err
}

func imageRegistryHost(image string) string {
	ref := strings.TrimSpace(image)
	ref, _, _ = strings.Cut(ref, "@")
	host, _, _ := strings.Cut(ref, "/")
	if strings.Contains(host, ".") || strings.Contains(host, ":") {
		return host
	}
	return ""
}
