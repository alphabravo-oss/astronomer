package grafanaproxy

import "bytes"

// PrepareHTML removes Kubernetes' private proxy prefix. Browser authentication
// and Origin checks now belong to the isolated tool gateway; no control-plane
// CSRF token or fetch adapter is injected into downstream documents.
func PrepareHTML(body []byte, kubePrefix string) []byte {
	return bytes.ReplaceAll(body, []byte(kubePrefix+"/"), []byte("/"))
}
