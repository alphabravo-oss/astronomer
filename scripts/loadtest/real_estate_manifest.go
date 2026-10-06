package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
	"github.com/google/uuid"
)

const estateSchema = "astronomer-real-estate-v2"

var estateName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var estateDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type estateManifest struct {
	Schema      string            `json:"schema_version"`
	Tier        int               `json:"assignment_tier_per_member"`
	Environment estateEnvironment `json:"environment"`
	Phases      []estatePhaseSpec `json:"phases"`
	Search      *estateSearchSpec `json:"search,omitempty"`
	Members     []estateMember    `json:"members"`
}
type estateEnvironment struct {
	ID                string         `json:"id"`
	Commit            string         `json:"commit"`
	ImagesSHA256      string         `json:"images_sha256"`
	ValuesSHA256      string         `json:"chart_values_sha256"`
	DatasetSHA256     string         `json:"dataset_sha256"`
	HardwareSHA256    string         `json:"hardware_sha256"`
	KubernetesVersion string         `json:"kubernetes_version"`
	Replicas          map[string]int `json:"replicas"`
}
type estateMember struct {
	Name             string              `json:"name"`
	ClusterID        string              `json:"cluster_id"`
	ProjectID        string              `json:"project_id"`
	Namespace        string              `json:"namespace"`
	PrivilegeProfile string              `json:"privilege_profile"`
	Assignments      []estateAssignment  `json:"assignments"`
	Resources        map[string]int      `json:"namespace_resource_counts"`
	Metrics          estateMetricsTarget `json:"metrics"`
}
type estateAssignment struct {
	ID                string                                 `json:"deployment_id"`
	TargetID          string                                 `json:"target_id"`
	Generation        int64                                  `json:"generation"`
	SpecDigest        string                                 `json:"spec_digest"`
	RenderedResources []astroclient.DeliveryResourceIdentity `json:"rendered_resources,omitempty"`
}
type estateMetricsTarget struct {
	URL        string `json:"url"`
	TokenFile  string `json:"token_file,omitempty"`
	InstanceID string `json:"instance_id"`
}

func loadEstateManifest(path string) (estateManifest, string, error) {
	var m estateManifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return m, "", errors.New("cannot read estate manifest")
	}
	if len(raw) > 1<<20 {
		return m, "", errors.New("estate manifest exceeds 1MiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return m, "", errors.New("invalid estate manifest JSON")
	}
	if d.Decode(new(any)) != io.EOF {
		return m, "", errors.New("trailing estate manifest data")
	}
	return m, fmt.Sprintf("%x", sha256.Sum256(raw)), m.validate()
}
func (m estateManifest) validate() error {
	if m.Schema != estateSchema || (m.Tier != 1 && m.Tier != 10 && m.Tier != 100) || len(m.Members) < 2 || len(m.Members) > 10 {
		return errors.New("estate requires schema v1, tier 1/10/100, and 2–10 members")
	}
	if err := m.validatePhases(); err != nil {
		return err
	}
	e := m.Environment
	if !estateName.MatchString(e.ID) || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(e.Commit) || !regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+[a-zA-Z0-9.+-]*$`).MatchString(e.KubernetesVersion) {
		return errors.New("invalid frozen environment identity")
	}
	for _, digest := range []string{e.ImagesSHA256, e.ValuesSHA256, e.DatasetSHA256, e.HardwareSHA256} {
		if !estateDigest.MatchString(digest) {
			return errors.New("environment requires SHA256 provenance")
		}
	}
	if len(e.Replicas) != 2 || e.Replicas["server"] < 1 || e.Replicas["worker"] < 1 {
		return errors.New("declare positive server and worker replicas")
	}
	seen := map[string]bool{}
	for _, member := range m.Members {
		if err := member.validate(m.Tier); err != nil {
			return err
		}
		for _, identity := range []string{"name:" + member.Name, "cluster:" + member.ClusterID, "metrics:" + member.Metrics.URL} {
			if seen[identity] {
				return errors.New("duplicate estate member identity")
			}
			seen[identity] = true
		}
		for _, a := range member.Assignments {
			if seen["assignment:"+a.ID] {
				return errors.New("duplicate deployment fixture")
			}
			seen["assignment:"+a.ID] = true
		}
	}
	return nil
}
func (m estateMember) validate(tier int) error {
	if !estateName.MatchString(m.Name) || !estateName.MatchString(m.Namespace) || !estateName.MatchString(m.PrivilegeProfile) || !validEstateUUID(m.ClusterID) || !validEstateUUID(m.ProjectID) {
		return errors.New("invalid member identity or scope")
	}
	if len(m.Assignments) != tier {
		return errors.New("assignment count must equal tier per member")
	}
	for _, a := range m.Assignments {
		if !validEstateUUID(a.ID) || !validEstateUUID(a.TargetID) || a.Generation < 1 || !strings.HasPrefix(a.SpecDigest, "sha256:") || !estateDigest.MatchString(strings.TrimPrefix(a.SpecDigest, "sha256:")) || len(a.RenderedResources) > 64 {
			return errors.New("invalid assignment identity, generation or digest")
		}
		seenRefs := map[string]bool{}
		for _, ref := range a.RenderedResources {
			key := estateResourcePath(ref)
			if seenRefs[key] {
				return errors.New("duplicate rendered resource reference")
			}
			seenRefs[key] = true
			if ref.Namespace == nil || *ref.Namespace != m.Namespace || !estateResourceName.MatchString(ref.Name) || estateResourcePath(ref) == "" {
				return errors.New("invalid rendered resource reference")
			}
		}
	}
	if len(m.Resources) != 3 {
		return errors.New("declare pods, deployments, services census")
	}
	for _, k := range []string{"pods", "deployments", "services"} {
		n, ok := m.Resources[k]
		if !ok || n < 1 || n > 100000 {
			return errors.New("resource census must be positive and bounded")
		}
	}
	if !estateName.MatchString(m.Metrics.InstanceID) {
		return errors.New("invalid expected metric instance ID")
	}
	return validateEstateURL(m.Metrics.URL, true)
}
func validEstateUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id != uuid.Nil && id.String() == s
}
func validateEstateURL(s string, metrics bool) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback())) || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return errors.New("estate endpoints require credential-free HTTPS or numeric loopback HTTP URLs without query or fragment")
	}
	if (metrics && u.Path != "/metrics") || (!metrics && u.Path != "" && u.Path != "/") {
		return errors.New("invalid estate endpoint path")
	}
	return nil
}
func validateEstateConfig(c *config) error {
	if c.realEstate == "" {
		return errors.New("check-only requires -real-estate")
	}
	if c.certification || c.validateDrills || c.skipAgents || c.keepFixtures || c.profilePath != "" || c.loginEmail != "" || c.loginPasswordPath != "" || c.auditObserverPath != "" {
		return errors.New("real estate cannot combine synthetic, profile, certification, audit mutation, or login modes")
	}

	if len(c.estateMixedFlags) > 0 {
		return errors.New("synthetic or metrics-server flags are incompatible with real estate")
	}
	return validateEstateURL(c.server, false)
}
