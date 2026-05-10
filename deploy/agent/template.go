package agenttemplate

import (
	_ "embed"
	"strings"
)

//go:embed install.yaml.template
var installTemplate string

type InstallTemplateData struct {
	ServerURL         string
	ClusterID         string
	RegistrationToken string
	CACert            string
	AgentImage        string
}

func RenderInstallYAML(data InstallTemplateData) string {
	return strings.NewReplacer(
		"{{SERVER_URL}}", data.ServerURL,
		"{{CLUSTER_ID}}", data.ClusterID,
		"{{REGISTRATION_TOKEN}}", data.RegistrationToken,
		"{{CA_CERT}}", data.CACert,
		"{{AGENT_IMAGE}}", data.AgentImage,
	).Replace(installTemplate)
}
