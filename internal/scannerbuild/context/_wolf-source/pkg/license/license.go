// Package license is the public commercial-license contract.
// Community never reports a valid commercial license and never stores a blob.
// Overlay registers a Verifier; expired or invalid licenses must not conceal data.
package license

import "errors"

const (
	ReasonCommunity = "This Community binary cannot verify or activate commercial licenses. Use Wolf Enterprise."
	ReasonUnsigned  = "license is not a signed envelope"
	ReasonNoKey     = "issuance public key is not configured"
	ReasonBadSig    = "license signature is invalid"
	ReasonExpired   = "license is expired or not yet valid"
	ReasonInvalid   = "license envelope is invalid"
)

var (
	ErrCommunityBinary = errors.New("community binary cannot install a commercial license")
	ErrUnconfigured    = errors.New("license verifier is not configured")
)

type Status struct {
	Valid             bool   `json:"valid"`
	Edition           string `json:"edition"`
	Product           string `json:"product"`
	CommercialLicense bool   `json:"commercial_license"`
	Reason            string `json:"reason"`
	DataIntact        bool   `json:"data_intact"`
	CommunityFallback bool   `json:"community_fallback"`
}

type Verifier interface {
	Inspect(blob string) Status
	Install(blob string) error
}

// Community is the public binary: always invalid, never stores, never conceals.
type Community struct {
	Edition string
}

func (c Community) Inspect(string) Status {
	ed := c.Edition
	if ed == "" {
		ed = "community"
	}
	return Status{
		Valid:             false,
		Edition:           ed,
		Product:           "Wolf Community",
		CommercialLicense: false,
		Reason:            ReasonCommunity,
		DataIntact:        true,
		CommunityFallback: true,
	}
}

func (Community) Install(string) error { return ErrCommunityBinary }
