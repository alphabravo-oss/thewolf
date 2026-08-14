package scannersigning

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const maxProfileDocumentBytes = 1 << 20

// ProfileDocument is the deployment and CLI representation of a signer
// profile. References are opaque identifiers or mounted-file references; this
// type has intentionally no field capable of carrying private-key bytes.
type ProfileDocument struct {
	SchemaVersion      string                        `json:"schema_version"`
	ID                 string                        `json:"id"`
	Name               string                        `json:"name"`
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
}

func (d ProfileDocument) Profile() (scannerrelease.SignerProfile, error) {
	if d.SchemaVersion != "wolf.scanner-signer-profile/v1" {
		return scannerrelease.SignerProfile{}, fmt.Errorf(
			"unsupported signer profile schema %q", d.SchemaVersion,
		)
	}
	profile := scannerrelease.SignerProfile{
		ID: d.ID, Name: d.Name, Provider: d.Provider, Algorithm: d.Algorithm,
		KeyReference: d.KeyReference, SecretReference: d.SecretReference,
		WorkloadIdentity: d.WorkloadIdentity, Identity: d.Identity,
		Issuer: d.Issuer, Subject: d.Subject,
		TrustRootReference: d.TrustRootReference,
		State:              scannerrelease.SignerActive, Revision: d.Revision,
	}
	if err := ValidateProfile(profile); err != nil {
		return scannerrelease.SignerProfile{}, err
	}
	return profile, nil
}

func ReadProfileFile(path string) (scannerrelease.SignerProfile, error) {
	file, err := os.Open(path)
	if err != nil {
		return scannerrelease.SignerProfile{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return scannerrelease.SignerProfile{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxProfileDocumentBytes {
		return scannerrelease.SignerProfile{}, errors.New(
			"signer profile must be a regular file no larger than 1 MiB",
		)
	}
	value, err := io.ReadAll(io.LimitReader(file, maxProfileDocumentBytes+1))
	if err != nil {
		return scannerrelease.SignerProfile{}, err
	}
	if len(value) > maxProfileDocumentBytes {
		return scannerrelease.SignerProfile{}, errors.New("signer profile exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var document ProfileDocument
	if err := decoder.Decode(&document); err != nil {
		return scannerrelease.SignerProfile{}, fmt.Errorf("decode signer profile: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return scannerrelease.SignerProfile{}, errors.New("signer profile has trailing JSON")
	}
	return document.Profile()
}
