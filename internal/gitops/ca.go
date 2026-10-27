package gitops

import (
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// CABundleEnv names an optional PEM bundle of extra certificate authorities
// for self-hosted Git servers that use a private CA. It is applied to both Git
// itself and Wolf's own forge API calls.
const CABundleEnv = "WOLF_GIT_CA_BUNDLE"

func caBundlePath() string { return strings.TrimSpace(os.Getenv(CABundleEnv)) }

// CAEnv returns the environment Git needs to trust the configured bundle.
func CAEnv() []string {
	if path := caBundlePath(); path != "" {
		return []string{"GIT_SSL_CAINFO=" + path}
	}
	return nil
}

// CAPool returns the system roots plus the configured bundle, or nil when no
// bundle is configured (callers then keep Go's default trust store).
func CAPool() (*x509.CertPool, error) {
	path := caBundlePath()
	if path == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(path) // #nosec G304 -- operator-configured path.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", CABundleEnv, err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s has no usable PEM certificates", CABundleEnv)
	}
	return pool, nil
}
