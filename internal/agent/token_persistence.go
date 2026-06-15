package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func persistRotatedToken(ctx context.Context, cfg *AgentConfig, token string) error {
	if cfg == nil || token == "" || cfg.TokenSecretName == "" || cfg.TokenSecretKey == "" {
		return nil
	}
	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return nil
	}
	namespace := strings.TrimSpace(string(nsBytes))
	if namespace == "" {
		return nil
	}
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil
	}
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, cfg.TokenSecretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if secret.StringData == nil {
		secret.StringData = map[string]string{}
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.StringData[cfg.TokenSecretKey] = token
	secret.Data[cfg.TokenSecretKey] = []byte(token)
	if _, err := clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update token secret: %w", err)
	}
	return nil
}
