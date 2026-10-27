package scantarget

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// ParseGitLabSource splits an https GitLab project URL (gitlab.com or a
// self-hosted instance) into its host, including any non-default port, and the
// full project path such as "group/subgroup/project".
func ParseGitLabSource(raw string) (host, project string, err error) {
	canonical, _, err := CanonicalGitURL(raw)
	if err != nil {
		return "", "", err
	}
	parsed, err := url.Parse(canonical)
	if err != nil || parsed.Scheme != "https" {
		return "", "", fmt.Errorf("GitLab fixing needs an https project URL")
	}
	project = strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git")
	segments := strings.Split(project, "/")
	if len(segments) < 2 {
		return "", "", fmt.Errorf("GitLab project URL must include a namespace and project")
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || segment == "-" {
			return "", "", fmt.Errorf("GitLab project URL has an invalid path")
		}
	}
	return parsed.Host, project, nil
}

// CredentialAllowsHost reports whether the credential is bound to host.
func CredentialAllowsHost(credential *models.Secret, host string) bool {
	return credentialAllowsHost(credential, host)
}
