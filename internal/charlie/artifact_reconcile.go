package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const advertisedReleaseSchema = "charlie.artifacts.release/v1"

// AdvertisedRelease is Charlie's current generic-agent image/chart pair.
type AdvertisedRelease struct {
	Schema         string `json:"schema"`
	Image          string `json:"image"`
	ManifestDigest string `json:"manifest_digest"`
	Chart          string `json:"chart"`
	ChartDigest    string `json:"chart_digest"`
}

// ArtifactReconciler pulls Charlie's advertised agent release over the same
// 443 registry origin used at consume time and helm-upgrades when the pin moves.
type ArtifactReconciler struct {
	Queries  activeCharlieConnectionReader
	Client   kubernetes.Interface
	Helm     HelmReleaser
	HTTP     *http.Client
	Interval time.Duration
	Now      func() time.Time
}

func NewArtifactReconciler(queries activeCharlieConnectionReader, client kubernetes.Interface, helm HelmReleaser) *ArtifactReconciler {
	if queries == nil || client == nil || helm == nil {
		return nil
	}
	return &ArtifactReconciler{
		Queries: queries, Client: client, Helm: helm,
		HTTP: &http.Client{Timeout: 20 * time.Second}, Interval: 5 * time.Minute,
	}
}

func (r *ArtifactReconciler) Run(ctx context.Context) {
	if r == nil {
		return
	}
	_ = r.Reconcile(ctx)
	interval := r.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = r.Reconcile(ctx)
		}
	}
}

func (r *ArtifactReconciler) Reconcile(ctx context.Context) error {
	if r == nil || r.Queries == nil || r.Client == nil || r.Helm == nil {
		return fmt.Errorf("Charlie artifact reconciler is unavailable")
	}
	connection, err := r.Queries.GetActiveCharlieConnection(ctx)
	if err != nil {
		return nil
	}
	if !connection.Active || strings.TrimSpace(connection.CentralUrl) == "" {
		return nil
	}
	secret, err := r.Client.CoreV1().Secrets(agentNamespaceName).Get(ctx, artifactPullSecret, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	password := dockerconfigPassword(secret)
	if password == "" {
		return nil
	}
	offered, err := fetchAdvertisedRelease(ctx, r.httpClient(), connection.CentralUrl, password)
	if err != nil {
		return err
	}
	if offered.ManifestDigest == connection.ImageDigest && offered.ChartDigest == connection.ChartDigest {
		return nil
	}
	repo, digest, ok := strings.Cut(offered.Image, "@")
	if !ok || !strings.HasPrefix(digest, "sha256:") {
		return fmt.Errorf("Charlie advertised agent image is not digest-pinned")
	}
	modeCeiling := connection.RequestedMode
	if !validMode(Mode(modeCeiling)) {
		modeCeiling = string(ModeDisabled)
	}
	return r.Helm.Apply(ctx, HelmReleaseSpec{
		ChartRef: offered.Chart, ChartDigest: offered.ChartDigest,
		Image: offered.Image, ImageDigest: digest, PullUser: "charlie", PullSecret: password, ReuseValues: true,
		Values: map[string]any{
			"image":   map[string]any{"repository": repo, "digest": digest, "pullPolicy": "IfNotPresent"},
			"runtime": map[string]any{"modeCeiling": modeCeiling},
		},
	})
}

func (r *ArtifactReconciler) httpClient() *http.Client {
	if r != nil && r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func fetchAdvertisedRelease(ctx context.Context, client *http.Client, origin, password string) (AdvertisedRelease, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(origin), "/") + "/charlie/v1/artifacts/release"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return AdvertisedRelease{}, err
	}
	request.SetBasicAuth("charlie", password)
	response, err := client.Do(request)
	if err != nil {
		return AdvertisedRelease{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return AdvertisedRelease{}, err
	}
	if response.StatusCode != http.StatusOK {
		return AdvertisedRelease{}, fmt.Errorf("Charlie advertised release returned HTTP %d", response.StatusCode)
	}
	var offered AdvertisedRelease
	if json.Unmarshal(body, &offered) != nil || offered.Schema != advertisedReleaseSchema {
		return AdvertisedRelease{}, fmt.Errorf("Charlie advertised release is invalid")
	}
	if !strings.HasPrefix(offered.ManifestDigest, "sha256:") || !strings.HasPrefix(offered.ChartDigest, "sha256:") {
		return AdvertisedRelease{}, fmt.Errorf("Charlie advertised release is not digest-pinned")
	}
	return offered, nil
}

func dockerconfigPassword(secret *corev1.Secret) string {
	if secret == nil {
		return ""
	}
	raw := secret.Data[corev1.DockerConfigJsonKey]
	if len(raw) == 0 {
		return ""
	}
	var payload struct {
		Auths map[string]struct {
			Password string `json:"password"`
		} `json:"auths"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	for _, auth := range payload.Auths {
		if strings.TrimSpace(auth.Password) != "" {
			return auth.Password
		}
	}
	return ""
}
