package distribution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// Status contains bounded operational metadata, never URL credentials or keys.
type Status struct {
	Mode                        string    `json:"mode"`
	Release                     string    `json:"release"`
	SourceDigest                string    `json:"source_digest"`
	Location                    string    `json:"location"`
	Verification                string    `json:"verification"`
	DatabasePayloadVerification string    `json:"database_payload_verification,omitempty"`
	DatabaseFreshness           string    `json:"database_freshness,omitempty"`
	InventoryComplete           bool      `json:"inventory_complete"`
	LastSuccessfulSync          time.Time `json:"last_successful_sync"`
	BlockedCategory             string    `json:"blocked_category"`
}
type Options struct {
	Development    bool
	Repository     string
	Digest         string
	Release        string
	CatalogDigest  string
	Profile        string
	Policy         model.TrustPolicy
	TrustDirectory string
	Client         *http.Client
	Network        httpclient.DestinationPolicy
	MirrorPlan     *MirrorPlan
	// UpdateBinding is independently injected build/base/scanner intent. A
	// successful signed catalog must match it before granting base provenance.
	// Its absence preserves ordinary distribution loading without update authority.
	UpdateBinding *DatabaseUpdateBinding
}
type Loader struct {
	mutex         sync.Mutex
	options       Options
	cached        *builtinbundles.Catalog
	status        Status
	fetchMetadata metadataArtifactFetcher
	fetch         artifactFetcher
	// Only VerifyOfflineStore installs this closed in-memory transport. It is
	// never selected by runtime configuration or distribution document fields.
	offline *offlineRegistry
}
type metadataArtifactFetcher func(context.Context, *http.Client, string, string, string, model.TrustPolicy, string, int64) (resolver.ArtifactMetadata, resolver.Verification, error)
type artifactFetcher func(context.Context, *http.Client, string, string, string, model.TrustPolicy, string, int64) ([]byte, resolver.Verification, error)

// New creates one immutable loader identity for the process. Successful bytes
// are cached once within bounds; failures retry without replacing accepted
// assignments. Production never falls back to embedded development fixtures.
func New(options Options) (*Loader, error) {
	options.Network = options.Network.Clone()
	if options.UpdateBinding != nil {
		binding := *options.UpdateBinding
		if options.Development || binding.validate() != nil || binding.BaseRelease != options.Release || binding.BaseDistributionDigest != options.Digest || binding.CatalogDigest != options.CatalogDigest || binding.Profile != options.Profile || options.Policy.Provider != model.SignatureCosignKey && options.Policy.Provider != model.SignatureCosignKeyless {
			return nil, errors.New("database update base differs from distribution constructor")
		}
		options.UpdateBinding = &binding
	}
	if options.MirrorPlan != nil {
		frozen, err := options.MirrorPlan.immutable()
		if err != nil {
			return nil, err
		}
		options.MirrorPlan = frozen
	}
	if options.UpdateBinding != nil {
		expectedMirror := ""
		if options.MirrorPlan != nil {
			expectedMirror = options.MirrorPlan.identity
		}
		if options.UpdateBinding.MirrorEndorsementDigest != expectedMirror {
			return nil, errors.New("database update mirror binding differs from verified endorsement")
		}
	}
	loader := &Loader{options: options, fetch: resolver.FetchVerifiedOCIArtifact, fetchMetadata: resolver.FetchVerifiedOCIArtifactMetadata, status: Status{Mode: "verified-release", Release: options.Release, SourceDigest: options.Digest, Verification: "pending"}}
	if options.Development {
		if options.Repository != "" || options.Digest != "" || options.Network.Restricted {
			return nil, errors.New("development catalog cannot claim release or disconnected identity")
		}
		loader.status.Mode = "source-development"
		loader.status.Verification = "unsigned-development"
		return loader, nil
	}
	if options.Client == nil || !digestPattern.MatchString(options.Digest) || !digestPattern.MatchString(options.CatalogDigest) || options.Release == "" || options.Profile == "" || options.Policy.AllowUnsigned || options.Policy.Validate() != nil {
		return nil, errors.New("verified distribution inputs are incomplete")
	}
	if options.MirrorPlan != nil && (!digestPattern.MatchString(options.MirrorPlan.identity) || options.MirrorPlan.Release != options.Release || options.MirrorPlan.DistributionDigest != options.Digest || options.MirrorPlan.CatalogDigest != options.CatalogDigest) {
		return nil, errors.New("distribution mirror endorsement identity is invalid")
	}
	location, err := url.Parse("https://" + options.Repository)
	if err != nil || location.Hostname() == "" || location.User != nil || location.RawQuery != "" || location.Fragment != "" || location.Opaque != "" {
		return nil, errors.New("distribution repository identity is invalid")
	}
	loader.status.Location = location.Host + location.EscapedPath()
	return loader, nil
}

func (l *Loader) Load(ctx context.Context) (builtinbundles.Catalog, error) {
	if l == nil {
		return builtinbundles.Catalog{}, errors.New("distribution loader is unavailable")
	}
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.options.Development {
		return builtinbundles.Load()
	}
	if err := ctx.Err(); err != nil {
		return builtinbundles.Catalog{}, err
	}
	if l.cached == nil {
		if err := l.validateLocation(ctx, l.options.Repository); err != nil {
			l.status.BlockedCategory = "network"
			return builtinbundles.Catalog{}, errors.New("distribution location is outside operator policy")
		}
		payload, verification, err := l.fetch(ctx, l.options.Client, l.options.Repository, l.options.Digest, MediaType, l.options.Policy, l.options.TrustDirectory, MaximumArchiveBytes)
		if err != nil || verification.Status != "verified" {
			clear(payload)
			l.status.BlockedCategory = "signature_or_subject"
			l.status.Verification = "failed"
			return builtinbundles.Catalog{}, errors.New("distribution authentication or retrieval failed")
		}
		catalog, err := l.prepareCatalog(ctx, payload)
		clear(payload)
		if err != nil {
			l.status.BlockedCategory = blockedCategory(err)
			l.status.Verification = "failed"
			return builtinbundles.Catalog{}, err
		}
		l.cached = &catalog
		l.status.Verification = "verified"
		l.status.InventoryComplete = true
		l.status.LastSuccessfulSync = time.Now().UTC()
		l.status.BlockedCategory = ""
	}
	return cloneCatalog(*l.cached), nil
}

func (l *Loader) prepareCatalog(ctx context.Context, payload []byte) (builtinbundles.Catalog, error) {
	catalog, bom, members, err := readArchiveMembers(payload, l.options.Release, l.options.CatalogDigest, l.options.Profile)
	if err != nil {
		return builtinbundles.Catalog{}, err
	}
	catalog.DistributionIdentity = l.options.Digest
	catalog.VerificationIdentity = l.options.Policy.Identity
	catalog.VerificationIssuer = l.options.Policy.Issuer
	catalog.VerificationProvider = string(l.options.Policy.Provider)
	catalog.VerificationKeyRef = l.options.Policy.KeyRef
	if err := projectArtifactValues(&catalog, bom); err != nil {
		return builtinbundles.Catalog{}, err
	}
	// Project locations first, then authenticate the same immutable child subjects
	// at approved mirrors. Their original vendor signature remains mandatory.
	for index := range catalog.Components {
		component := &catalog.Components[index]
		for _, subject := range bom.Charts {
			if subject.Component == component.Slug {
				component.Source.OriginalURL = component.Source.URL
				component.Source.OriginalArchiveDigest = subject.ArchiveDigest
				component.Source.URL = "oci://" + subject.Repository
				component.Source.Kind = "helm_oci"
				for index, capability := range component.Requirements.Capabilities {
					if capability == "delivery.source.helm_http" {
						component.Requirements.Capabilities[index] = "delivery.source.helm_oci"
					}
				}
				component.Source.ChartDigest = subject.Digest
			}
		}
	}
	catalog, err = projectMirrors(ctx, catalog, l.options.MirrorPlan, l.options.Network)
	if err != nil {
		return builtinbundles.Catalog{}, err
	}
	for _, component := range catalog.Components {
		repository := strings.TrimPrefix(component.Source.URL, "oci://") + "/" + component.Source.Chart
		if err := l.validateLocation(ctx, repository); err != nil {
			return builtinbundles.Catalog{}, errors.New("child chart location is outside operator policy")
		}
		archive, verification, err := l.fetch(ctx, l.options.Client, repository, component.Source.ChartDigest, "application/vnd.cncf.helm.chart.content.v1.tar+gzip", l.options.Policy, l.options.TrustDirectory, MaximumArchiveBytes)
		if err != nil || verification.Status != "verified" {
			clear(archive)
			return builtinbundles.Catalog{}, errors.New("child chart signature is invalid or unavailable")
		}
		actual := contentDigest(archive)
		clear(archive)
		for _, subject := range bom.Charts {
			if subject.Component == component.Slug && subject.ArchiveDigest != actual {
				return builtinbundles.Catalog{}, errors.New("child chart payload differs from signed archive")
			}
		}
	}
	if err := catalog.Validate(); err != nil {
		return builtinbundles.Catalog{}, err
	}
	if err := l.verifyPublishedAssets(ctx, bom, members); err != nil {
		return builtinbundles.Catalog{}, err
	}
	return catalog, nil
}

func (l *Loader) validateLocation(ctx context.Context, repository string) error {
	if l.offline != nil {
		if l.options.Client.Transport != l.offline || !l.offline.repositories[repository] {
			return errors.New("offline subject is outside the closed local graph")
		}
		return ctx.Err()
	}
	return l.options.Network.ValidateURL(ctx, "https://"+repository)
}

func cloneCatalog(catalog builtinbundles.Catalog) builtinbundles.Catalog {
	catalog.Components = append([]builtinbundles.Component(nil), catalog.Components...)
	for index := range catalog.Components {
		component := &catalog.Components[index]
		component.Images = append([]string(nil), component.Images...)
		component.Requirements.Capabilities = append([]string(nil), component.Requirements.Capabilities...)
		data, _ := json.Marshal(component.Values)
		component.Values = nil
		_ = json.Unmarshal(data, &component.Values)
	}
	return catalog
}

func (l *Loader) Status() Status {
	if l == nil {
		return Status{Verification: "unavailable"}
	}
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.status
}
