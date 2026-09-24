package grafanaproxy

import (
	"bytes"
	_ "embed"
)

//go:embed browser_csrf.js
var browserCSRF string

// PrepareHTML removes Kubernetes' private URL prefix and installs the browser
// CSRF adapter before Grafana boots. Session credentials never enter the HTML.
func PrepareHTML(body []byte, kubePrefix string) []byte {
	body = bytes.ReplaceAll(body, []byte(kubePrefix+"/"), []byte("/"))
	return bytes.Replace(body, []byte("</head>"), []byte("<script>"+browserCSRF+"</script></head>"), 1)
}
