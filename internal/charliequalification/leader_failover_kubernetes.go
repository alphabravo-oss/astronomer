package charliequalification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// KubernetesLeaderFailoverConfig names one fixed Charlie product-agent
// StatefulSet. It has no resource kind, object name, command, URL, patch, or
// arbitrary evidence input.
type KubernetesLeaderFailoverConfig struct {
	Kubeconfig   string
	Namespace    string
	StatefulSet  string
	PollInterval time.Duration
}

type leaderFailoverTarget interface {
	Snapshot(context.Context, int) (string, int, error)
	DeleteAndWaitReplacement(context.Context, int, string) (time.Time, error)
	WaitReady(context.Context, int) error
}

// KubernetesLeaderFailoverTarget deletes only the exact leader ordinal of the
// configured StatefulSet. Kubernetes UID preconditions prevent a replacement
// pod from being deleted if the observed pod changes before the request lands.
type KubernetesLeaderFailoverTarget struct {
	client      kubernetes.Interface
	namespace   string
	statefulSet string
	poll        time.Duration
}

func NewKubernetesLeaderFailoverTarget(config KubernetesLeaderFailoverConfig) (*KubernetesLeaderFailoverTarget, error) {
	if config.Kubeconfig == "" || !kubernetesNamePattern.MatchString(config.Namespace) || !kubernetesNamePattern.MatchString(config.StatefulSet) {
		return nil, errors.New("leader failover requires an owner-only kubeconfig and fixed StatefulSet target")
	}
	file, err := os.Open(config.Kubeconfig)
	if err != nil {
		return nil, errors.New("leader failover kubeconfig must be owner-only")
	}
	defer func() { _ = file.Close() }()
	info, statErr := file.Stat()
	contents, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || readErr != nil || len(contents) == 0 || len(contents) > 1<<20 {
		return nil, errors.New("leader failover kubeconfig must be an owner-only bounded regular file")
	}
	rawConfig, err := clientcmd.Load(contents)
	if err != nil {
		return nil, errors.New("leader failover kubeconfig is invalid")
	}
	for _, authInfo := range rawConfig.AuthInfos {
		if authInfo != nil && (authInfo.Exec != nil || authInfo.AuthProvider != nil || authInfo.ClientCertificate != "" || authInfo.ClientKey != "" || authInfo.TokenFile != "") {
			return nil, errors.New("leader failover kubeconfig may not execute plugins or reference credential files")
		}
	}
	for _, cluster := range rawConfig.Clusters {
		if cluster != nil && (cluster.CertificateAuthority != "" || cluster.ProxyURL != "") {
			return nil, errors.New("leader failover kubeconfig may not reference CA files or proxy URLs")
		}
	}
	poll := config.PollInterval
	if poll == 0 {
		poll = time.Second
	}
	if poll < 10*time.Millisecond || poll > 10*time.Second {
		return nil, errors.New("leader failover polling is outside its safe bound")
	}
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(contents)
	if err != nil {
		return nil, errors.New("leader failover kubeconfig is invalid")
	}
	serverURL, parseErr := url.Parse(restConfig.Host)
	if parseErr != nil || serverURL.Scheme != "https" || serverURL.Hostname() == "" || serverURL.User != nil || (serverURL.Path != "" && serverURL.Path != "/") || serverURL.RawQuery != "" || serverURL.Fragment != "" {
		return nil, errors.New("leader failover Kubernetes endpoint must be a fixed HTTPS origin")
	}
	restConfig.Timeout = 15 * time.Second
	restConfig.Proxy = func(*http.Request) (*url.URL, error) { return nil, nil }
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, errors.New("leader failover Kubernetes client is unavailable")
	}
	return &KubernetesLeaderFailoverTarget{client: client, namespace: config.Namespace, statefulSet: config.StatefulSet, poll: poll}, nil
}

func (t *KubernetesLeaderFailoverTarget) Snapshot(ctx context.Context, ordinal int) (string, int, error) {
	if t == nil || t.client == nil {
		return "", 0, errors.New("leader failover Kubernetes client is unavailable")
	}
	statefulSet, err := t.client.AppsV1().StatefulSets(t.namespace).Get(ctx, t.statefulSet, metav1.GetOptions{})
	if err != nil || statefulSet.UID == "" || statefulSet.Spec.Replicas == nil || *statefulSet.Spec.Replicas < 2 || *statefulSet.Spec.Replicas > 20 || ordinal < 0 || ordinal >= int(*statefulSet.Spec.Replicas) {
		return "", 0, errors.New("leader failover StatefulSet is not a bounded redundant target")
	}
	pod, err := t.client.CoreV1().Pods(t.namespace).Get(ctx, t.podName(ordinal), metav1.GetOptions{})
	if err != nil || !ownedReadyPod(pod, statefulSet.UID, t.statefulSet) {
		return "", 0, errors.New("leader failover pod is not a ready member of the fixed StatefulSet")
	}
	return string(pod.UID), int(*statefulSet.Spec.Replicas), nil
}

func (t *KubernetesLeaderFailoverTarget) DeleteAndWaitReplacement(ctx context.Context, ordinal int, observedUID string) (time.Time, error) {
	if t == nil || t.client == nil || ordinal < 0 || ordinal > 19 || observedUID == "" {
		return time.Time{}, errors.New("leader failover deletion binding is invalid")
	}
	uid := types.UID(observedUID)
	if err := t.client.CoreV1().Pods(t.namespace).Delete(ctx, t.podName(ordinal), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil {
		return time.Time{}, errors.New("leader failover pod deletion failed")
	}
	ticker := time.NewTicker(t.poll)
	defer ticker.Stop()
	for {
		statefulSet, setErr := t.client.AppsV1().StatefulSets(t.namespace).Get(ctx, t.statefulSet, metav1.GetOptions{})
		pod, podErr := t.client.CoreV1().Pods(t.namespace).Get(ctx, t.podName(ordinal), metav1.GetOptions{})
		if setErr == nil && podErr == nil && string(pod.UID) != observedUID && ownedReadyPod(pod, statefulSet.UID, t.statefulSet) {
			return time.Now().UTC(), nil
		}
		select {
		case <-ctx.Done():
			return time.Time{}, errors.New("leader failover replacement did not become ready")
		case <-ticker.C:
		}
	}
}

func (t *KubernetesLeaderFailoverTarget) WaitReady(ctx context.Context, replicas int) error {
	if t == nil || t.client == nil || replicas < 2 || replicas > 20 {
		return errors.New("leader failover replica restoration bound is invalid")
	}
	ticker := time.NewTicker(t.poll)
	defer ticker.Stop()
	for {
		statefulSet, err := t.client.AppsV1().StatefulSets(t.namespace).Get(ctx, t.statefulSet, metav1.GetOptions{})
		ready := err == nil && statefulSet.Spec.Replicas != nil && int(*statefulSet.Spec.Replicas) == replicas
		if ready {
			for ordinal := 0; ordinal < replicas; ordinal++ {
				pod, podErr := t.client.CoreV1().Pods(t.namespace).Get(ctx, t.podName(ordinal), metav1.GetOptions{})
				if podErr != nil || !ownedReadyPod(pod, statefulSet.UID, t.statefulSet) {
					ready = false
					break
				}
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("leader failover StatefulSet did not restore readiness")
		case <-ticker.C:
		}
	}
}

func (t *KubernetesLeaderFailoverTarget) podName(ordinal int) string {
	return fmt.Sprintf("%s-%d", t.statefulSet, ordinal)
}

func ownedReadyPod(pod *corev1.Pod, ownerUID types.UID, ownerName string) bool {
	if pod == nil || pod.UID == "" || pod.DeletionTimestamp != nil {
		return false
	}
	owned := false
	for _, owner := range pod.OwnerReferences {
		if owner.Controller != nil && *owner.Controller && owner.Kind == "StatefulSet" && owner.Name == ownerName && owner.UID == ownerUID {
			owned = true
			break
		}
	}
	if !owned {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
