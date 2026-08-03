// Package scannersigning implements the customer signing trust boundary.
// Control-plane profiles contain opaque key/secret references only; adapter
// processes own provider authentication and private-key access.
package scannersigning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	RequestSchema = "wolf.scanner-signing-request/v1"
	ResultSchema  = "wolf.scanner-signing-result/v1"
)

var (
	profileIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	digestPattern    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	commitPattern    = regexp.MustCompile(`^[a-fA-F0-9]{7,128}$`)
	secretRefPattern = regexp.MustCompile(`^(secret|kubernetes|vault|file-ref)://[A-Za-z0-9][A-Za-z0-9_./:@-]{0,511}$`)
)

func ValidateProfile(profile scannerrelease.SignerProfile) error {
	if !profileIDPattern.MatchString(profile.ID) ||
		strings.TrimSpace(profile.Name) == "" ||
		profile.Revision <= 0 {
		return errors.New("signer profile ID, name, and positive revision are required")
	}
	switch profile.State {
	case scannerrelease.SignerActive, scannerrelease.SignerDisabled, scannerrelease.SignerRevoked:
	default:
		return fmt.Errorf("invalid signer state %q", profile.State)
	}
	algorithms := map[string]bool{
		"ed25519": true, "ecdsa-p256-sha256": true,
		"rsa-pss-sha256": true, "cosign-keyless": true,
	}
	if !algorithms[profile.Algorithm] {
		return fmt.Errorf("unsupported signer algorithm %q", profile.Algorithm)
	}
	expectedPrefix := ""
	switch profile.Provider {
	case scannerrelease.SignerAWSKMS:
		expectedPrefix = "aws-kms://"
	case scannerrelease.SignerGCPKMS:
		expectedPrefix = "gcp-kms://"
	case scannerrelease.SignerAzureKeyVault:
		expectedPrefix = "azure-keyvault://"
	case scannerrelease.SignerPKCS11:
		expectedPrefix = "pkcs11:"
	case scannerrelease.SignerKeyless:
		expectedPrefix = "workload://"
	case scannerrelease.SignerOffline:
		expectedPrefix = "offline://"
	case scannerrelease.SignerManagedKeyless:
		expectedPrefix = "managed-keyless://"
	default:
		return fmt.Errorf("unsupported signer provider %q", profile.Provider)
	}
	if !strings.HasPrefix(profile.KeyReference, expectedPrefix) ||
		containsPrivateMaterial(profile.KeyReference) {
		return fmt.Errorf("signer key_reference must be an opaque %s reference", expectedPrefix)
	}
	if profile.SecretReference != "" &&
		(!secretRefPattern.MatchString(profile.SecretReference) ||
			containsPrivateMaterial(profile.SecretReference)) {
		return errors.New("signer secret_reference must be an opaque secret reference")
	}
	if !profile.WorkloadIdentity && profile.SecretReference == "" {
		return errors.New("signer requires workload_identity or secret_reference")
	}
	if strings.TrimSpace(profile.Identity) == "" ||
		strings.TrimSpace(profile.Issuer) == "" ||
		strings.TrimSpace(profile.Subject) == "" {
		return errors.New("signer identity, issuer, and subject constraints are required")
	}
	if _, err := url.ParseRequestURI(profile.Issuer); err != nil {
		return errors.New("signer issuer must be an absolute URI")
	}
	if profile.TrustRootReference == "" ||
		!secretRefPattern.MatchString(profile.TrustRootReference) {
		return errors.New("signer trust_root_reference must be an opaque secret reference")
	}
	if profile.State == scannerrelease.SignerRevoked &&
		(profile.RevokedAt == nil || strings.TrimSpace(profile.RevocationReason) == "") {
		return errors.New("revoked signer requires revoked_at and revocation_reason")
	}
	return nil
}

func containsPrivateMaterial(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "private_key") ||
		strings.Contains(lower, "begin private") ||
		strings.ContainsAny(value, "\r\n\x00")
}

func ProfileDigest(profile scannerrelease.SignerProfile) (string, error) {
	if err := ValidateProfile(profile); err != nil {
		return "", err
	}
	value, err := json.Marshal(struct {
		ID                 string                        `json:"id"`
		Provider           scannerrelease.SignerProvider `json:"provider"`
		Algorithm          string                        `json:"algorithm"`
		KeyReference       string                        `json:"key_reference"`
		SecretReference    string                        `json:"secret_reference,omitempty"`
		WorkloadIdentity   bool                          `json:"workload_identity"`
		Identity           string                        `json:"identity"`
		Issuer             string                        `json:"issuer"`
		Subject            string                        `json:"subject"`
		TrustRootReference string                        `json:"trust_root_reference"`
		Revision           int64                         `json:"revision"`
	}{
		profile.ID, profile.Provider, profile.Algorithm,
		profile.KeyReference, profile.SecretReference, profile.WorkloadIdentity,
		profile.Identity, profile.Issuer, profile.Subject,
		profile.TrustRootReference, profile.Revision,
	})
	if err != nil {
		return "", err
	}
	return digest(value), nil
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ValidDigest(value string) bool { return digestPattern.MatchString(value) }

func DigestValue(value []byte) string { return digest(value) }

func MaskReference(value string) string {
	if value == "" {
		return ""
	}
	index := strings.Index(value, "://")
	if index < 0 {
		index = strings.IndexByte(value, ':')
	}
	if index < 0 {
		return "***"
	}
	prefix := value[:index+1]
	if strings.HasPrefix(value[index:], "://") {
		prefix += "//"
	}
	return prefix + "***"
}
