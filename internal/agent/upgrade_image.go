package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// Image-reference validation for self-upgrade. This is the FIRST of two gates:
// it decides whether a target image is even worth attempting. The second gate
// (upgrade_preflight.go) proves the kubelet can actually pull it. Neither gate
// says anything about what is INSIDE the image — see verifyImagePullable's doc
// comment for the explicit list of what "verified" does and does not cover.
const maxImageReferenceLength = 512

var (
	// A single path segment of a repository: no leading/trailing separator, no
	// uppercase restriction (registries differ), no path traversal.
	imageSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	imagePortPattern    = regexp.MustCompile(`^[0-9]{1,5}$`)
	imageTagPattern     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	imageDigestPattern  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// mutableImageTags are tags whose contents can change under a fixed reference.
// A self-upgrade to one of these is unverifiable and — worse — unrollbackable,
// because the rollback image would be the same string resolving to different
// bytes. Rejected unless the operator opts in explicitly.
var mutableImageTags = map[string]bool{
	"latest":  true,
	"stable":  true,
	"edge":    true,
	"main":    true,
	"master":  true,
	"dev":     true,
	"devel":   true,
	"nightly": true,
	"test":    true,
}

// imageReference is the parsed form of an image string. Repository is the
// registry+path with no tag or digest, and is what the allow-list matches on.
type imageReference struct {
	Repository string
	Tag        string
	Digest     string
}

func (r imageReference) String() string {
	out := r.Repository
	if r.Tag != "" {
		out += ":" + r.Tag
	}
	if r.Digest != "" {
		out += "@" + r.Digest
	}
	return out
}

// imagePolicy is the agent's self-upgrade image allow-list.
//
// AllowedRepository is an EXACT repository match, not a prefix: a prefix test
// would let "example.com/astronomer-agent-attacker" through the allow-list for
// "example.com/astronomer-agent". Empty means "no repository could be
// determined", which is a hard refusal — never an allow-all.
type imagePolicy struct {
	AllowedRepository string
	AllowMutableTag   bool
}

// parseImageReference accepts the subset of the OCI reference grammar the agent
// is willing to write into its own Deployment:
//
//	[host[:port]/]path[/path...]  (:tag | @sha256:<64 hex>){1,2}
//
// A bare repository with neither tag nor digest is rejected: it would mean
// ":latest" to the kubelet, which is exactly the floating reference this gate
// exists to keep out.
func parseImageReference(raw string) (imageReference, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return imageReference{}, fmt.Errorf("image reference is empty")
	}
	if len(s) > maxImageReferenceLength {
		return imageReference{}, fmt.Errorf("image reference is longer than %d bytes", maxImageReferenceLength)
	}
	for _, r := range s {
		if r <= 0x20 || r == 0x7f || r > 0x7e {
			return imageReference{}, fmt.Errorf("image reference contains a non-printable or non-ASCII character")
		}
	}

	rest := s
	digest := ""
	if i := strings.Index(rest, "@"); i >= 0 {
		digest = rest[i+1:]
		rest = rest[:i]
		if !imageDigestPattern.MatchString(digest) {
			return imageReference{}, fmt.Errorf("unsupported image digest %q (want sha256:<64 hex>)", digest)
		}
	}
	tag := ""
	// The tag separator is the last ':' that appears after the last '/', so a
	// registry port ("host:5000/repo") is not mistaken for a tag.
	if i := strings.LastIndex(rest, ":"); i >= 0 && i > strings.LastIndex(rest, "/") {
		tag = rest[i+1:]
		rest = rest[:i]
		if !imageTagPattern.MatchString(tag) {
			return imageReference{}, fmt.Errorf("invalid image tag %q", tag)
		}
	}
	if rest == "" {
		return imageReference{}, fmt.Errorf("image reference has no repository")
	}
	if strings.Contains(rest, "..") {
		return imageReference{}, fmt.Errorf("image repository %q contains %q", rest, "..")
	}
	for i, segment := range strings.Split(rest, "/") {
		if segment == "" {
			return imageReference{}, fmt.Errorf("image repository %q has an empty path segment", rest)
		}
		if i == 0 && strings.Contains(segment, ":") {
			host, port, _ := strings.Cut(segment, ":")
			if !imagePortPattern.MatchString(port) {
				return imageReference{}, fmt.Errorf("invalid registry port in %q", segment)
			}
			segment = host
		}
		if !imageSegmentPattern.MatchString(segment) {
			return imageReference{}, fmt.Errorf("invalid image repository segment %q", segment)
		}
	}
	if tag == "" && digest == "" {
		return imageReference{}, fmt.Errorf("image reference %q must be tag- or digest-qualified", s)
	}
	return imageReference{Repository: rest, Tag: tag, Digest: digest}, nil
}

// validateAgentImage is the pre-mutation gate: it must return nil before ANY
// write to the agent Deployment. It fails closed — an unknown allowed
// repository is a refusal, not a wildcard.
func validateAgentImage(raw string, policy imagePolicy) (imageReference, error) {
	ref, err := parseImageReference(raw)
	if err != nil {
		return imageReference{}, err
	}
	if policy.AllowedRepository == "" {
		return ref, fmt.Errorf("no permitted agent image repository is known (set ASTRONOMER_AGENT_IMAGE_REPOSITORY); refusing to change the agent image")
	}
	if ref.Repository != policy.AllowedRepository {
		return ref, fmt.Errorf("image repository %q is not the permitted agent image repository %q", ref.Repository, policy.AllowedRepository)
	}
	if ref.Digest == "" && !policy.AllowMutableTag && mutableImageTags[strings.ToLower(ref.Tag)] {
		return ref, fmt.Errorf("refusing floating image tag %q: pin a version tag or an @sha256 digest (set ASTRONOMER_AGENT_ALLOW_MUTABLE_TAG=true to override)", ref.Tag)
	}
	return ref, nil
}

// imageRepositoryOf returns the repository portion of an image string, or ""
// when it does not parse. Used to derive the default allow-list from the image
// the agent is CURRENTLY running: that image is trusted by construction, and
// deriving from it keeps already-deployed manifests (which carry no
// ASTRONOMER_AGENT_IMAGE_REPOSITORY) upgradable without opening the allow-list.
func imageRepositoryOf(image string) string {
	s := strings.TrimSpace(image)
	if s != "" && !strings.Contains(s, "@") {
		if i := strings.LastIndex(s, ":"); i < 0 || i < strings.LastIndex(s, "/") {
			// Unqualified reference. Supply the kubelet's own default so the
			// repository is still readable; the allow-list only ever compares
			// the repository, never this synthetic tag.
			s += ":latest"
		}
	}
	ref, err := parseImageReference(s)
	if err != nil {
		return ""
	}
	return ref.Repository
}
