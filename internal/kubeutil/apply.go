package kubeutil

import (
	"net/url"
	"strings"
)

const ApplyPatchContentType = "application/apply-patch+yaml"

type ApplyOptions struct {
	FieldManager string
	Force        bool
	DryRun       bool
}

func ServerSideApplyPath(resourcePath string, opts ApplyOptions) string {
	values := url.Values{}
	if manager := strings.TrimSpace(opts.FieldManager); manager != "" {
		values.Set("fieldManager", manager)
	}
	if opts.Force {
		values.Set("force", "true")
	}
	if opts.DryRun {
		values.Set("dryRun", "All")
	}
	query := values.Encode()
	if query == "" {
		return resourcePath
	}
	separator := "?"
	if strings.Contains(resourcePath, "?") {
		separator = "&"
	}
	return resourcePath + separator + query
}

func ApplyPatchHeaders() map[string]string {
	return map[string]string{
		"Content-Type": ApplyPatchContentType,
		"Accept":       "application/json",
	}
}
