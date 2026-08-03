// Package scannerregistry verifies immutable OCI scanner artifacts without
// requiring the Wolf API process to access a Docker daemon.
package scannerregistry

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var repositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

type Reference struct {
	Registry   string
	Repository string
	Digest     string
}

func ParseReference(value string) (Reference, error) {
	if strings.Contains(value, "://") {
		return Reference{}, errors.New("OCI reference must not contain a URL scheme")
	}
	name, digest, ok := strings.Cut(value, "@")
	if !ok || strings.Contains(digest, "@") {
		return Reference{}, errors.New("OCI reference must contain exactly one immutable digest")
	}
	slash := strings.IndexByte(name, '/')
	if slash <= 0 || slash == len(name)-1 {
		return Reference{}, errors.New("OCI reference must include registry and repository")
	}
	registry, repository := name[:slash], name[slash+1:]
	if err := validateRegistryHost(registry); err != nil {
		return Reference{}, err
	}
	if !repositoryPattern.MatchString(repository) || path.Clean(repository) != repository {
		return Reference{}, fmt.Errorf("invalid OCI repository %q", repository)
	}
	if !validSHA256Digest(digest) {
		return Reference{}, errors.New("OCI reference must use a sha256 digest")
	}
	return Reference{Registry: registry, Repository: repository, Digest: digest}, nil
}

func (r Reference) String() string {
	return r.Registry + "/" + r.Repository + "@" + r.Digest
}

func validateRegistryHost(value string) error {
	if value == "" || strings.ContainsAny(value, `/\?#@`) {
		return fmt.Errorf("invalid OCI registry host %q", value)
	}
	parsed, err := url.Parse("https://" + value)
	if err != nil || parsed.Host != value || parsed.Hostname() == "" {
		return fmt.Errorf("invalid OCI registry host %q", value)
	}
	if port := parsed.Port(); port != "" {
		if _, err := net.LookupPort("tcp", port); err != nil {
			// LookupPort rejects numeric values outside 1..65535 and accepts
			// well-known service names. Registry configuration uses numeric
			// ports, so reject service aliases explicitly.
			return fmt.Errorf("invalid OCI registry port %q", port)
		}
		for _, char := range port {
			if char < '0' || char > '9' {
				return fmt.Errorf("invalid OCI registry port %q", port)
			}
		}
	}
	return nil
}
