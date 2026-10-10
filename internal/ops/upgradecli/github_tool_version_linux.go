//go:build linux

package upgradecli

import (
	"errors"
	"regexp"
	"strings"
)

var githubVersionLine = regexp.MustCompile(`^gh version ([0-9]+\.[0-9]+\.[0-9]+)(?: \([0-9]{4}-[0-9]{2}-[0-9]{2}\))?$`)

// The official CLI emits one version line and its exact release-note URL.
// Tool.Version uses the same v-prefixed binding as Helm/kubectl. This parser
// validates only actual version output, never authenticity of an unbound tool.
func githubToolVersion(raw string) (string, error) {
	rows := strings.Split(strings.TrimSpace(raw), "\n")
	if len(rows) != 2 {
		return "", errors.New("GitHub CLI version output contract differs")
	}
	match := githubVersionLine.FindStringSubmatch(rows[0])
	if len(match) != 2 || rows[1] != "https://github.com/cli/cli/releases/tag/v"+match[1] {
		return "", errors.New("GitHub CLI version identity differs")
	}
	return match[1], nil
}
