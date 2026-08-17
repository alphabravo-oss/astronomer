// Package fluxdistribution exposes the exact verified downstream distribution
// as an offline registration asset. The files remain authoritative in this
// directory; no second generated manifest is maintained by the agent renderer.
package fluxdistribution

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed install.yaml
var installYAML string

//go:embed VERSION
var version string

//go:embed provenance.json
var provenanceJSON []byte

type ControllerImage struct {
	Component string
	Version   string
	Digest    string
	Reference string
}

var (
	controllerOnce sync.Once
	controllers    map[string]ControllerImage
	controllerErr  error
)

func InstallYAML() string {
	return strings.TrimSpace(installYAML) + "\n"
}

func Version() string {
	return strings.TrimSpace(version)
}

func Digest() string {
	digest := sha256.Sum256([]byte(installYAML))
	return fmt.Sprintf("sha256:%x", digest)
}

// ControllerSetDigest is the runtime identity reported by downstream agents.
// It commits to the exact name=image@digest set independently of YAML layout,
// so harmless manifest reformatting does not make healthy controllers appear
// incompatible.
func ControllerSetDigest() (string, error) {
	controllers, err := ControllerImages()
	if err != nil {
		return "", err
	}
	identities := make([]string, 0, len(controllers))
	for name, controller := range controllers {
		identities = append(identities, name+"="+controller.Reference+"@"+controller.Digest)
	}
	sort.Strings(identities)
	digest := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	return fmt.Sprintf("sha256:%x", digest), nil
}

// ControllerImages returns a defensive copy of the controller identities
// bound by the verified provenance. Runtime compatibility code consumes this
// instead of duplicating versions or digests in Go constants.
func ControllerImages() (map[string]ControllerImage, error) {
	controllerOnce.Do(func() {
		var document struct {
			ControllerImages []struct {
				Component string `json:"component"`
				Digest    string `json:"digest"`
				SourceRef string `json:"source_ref"`
			} `json:"controller_images"`
		}
		if err := json.Unmarshal(provenanceJSON, &document); err != nil {
			controllerErr = fmt.Errorf("decode embedded Flux provenance: %w", err)
			return
		}
		controllers = make(map[string]ControllerImage, len(document.ControllerImages))
		for _, entry := range document.ControllerImages {
			separator := strings.LastIndex(entry.SourceRef, ":")
			if entry.Component == "" || separator < strings.LastIndex(entry.SourceRef, "/") || !strings.HasPrefix(entry.Digest, "sha256:") {
				controllerErr = fmt.Errorf("invalid embedded Flux controller identity for %q", entry.Component)
				return
			}
			controllers[entry.Component] = ControllerImage{
				Component: entry.Component, Version: entry.SourceRef[separator+1:],
				Digest: entry.Digest, Reference: entry.SourceRef,
			}
		}
		if len(controllers) != 3 {
			controllerErr = fmt.Errorf("embedded Flux provenance contains %d controllers, want 3", len(controllers))
		}
	})
	if controllerErr != nil {
		return nil, controllerErr
	}
	result := make(map[string]ControllerImage, len(controllers))
	for name, controller := range controllers {
		result[name] = controller
	}
	return result, nil
}
