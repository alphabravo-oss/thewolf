package routes

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
)

type scannerSignerInput struct {
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
}

// scannerSignerView deliberately exposes only masked references. The full
// opaque references are write-only API input and adapter-only execution data.
type scannerSignerView struct {
	ID                        string                        `json:"id"`
	Name                      string                        `json:"name"`
	Provider                  scannerrelease.SignerProvider `json:"provider"`
	Algorithm                 string                        `json:"algorithm"`
	KeyReference              string                        `json:"key_reference"`
	SecretReference           string                        `json:"secret_reference,omitempty"`
	SecretReferenceConfigured bool                          `json:"secret_reference_configured"`
	WorkloadIdentity          bool                          `json:"workload_identity"`
	Identity                  string                        `json:"identity"`
	Issuer                    string                        `json:"issuer"`
	Subject                   string                        `json:"subject"`
	TrustRootReference        string                        `json:"trust_root_reference"`
	State                     scannerrelease.SignerState    `json:"state"`
	Revision                  int64                         `json:"revision"`
	RotatedFromID             string                        `json:"rotated_from_id,omitempty"`
	RevocationReason          string                        `json:"revocation_reason,omitempty"`
	RevokedBy                 string                        `json:"revoked_by,omitempty"`
	RevokedAt                 *time.Time                    `json:"revoked_at,omitempty"`
	CreatedBy                 string                        `json:"created_by"`
	CreatedAt                 time.Time                     `json:"created_at"`
	UpdatedAt                 time.Time                     `json:"updated_at"`
}

func signerProfileFromInput(
	input scannerSignerInput,
	id, actor string,
	revision int64,
) (*scannerrelease.SignerProfile, error) {
	profile := &scannerrelease.SignerProfile{
		ID: id, Name: strings.TrimSpace(input.Name), Provider: input.Provider,
		Algorithm:          strings.TrimSpace(input.Algorithm),
		KeyReference:       strings.TrimSpace(input.KeyReference),
		SecretReference:    strings.TrimSpace(input.SecretReference),
		WorkloadIdentity:   input.WorkloadIdentity,
		Identity:           strings.TrimSpace(input.Identity),
		Issuer:             strings.TrimSpace(input.Issuer),
		Subject:            strings.TrimSpace(input.Subject),
		TrustRootReference: strings.TrimSpace(input.TrustRootReference),
		State:              scannerrelease.SignerActive, Revision: revision, CreatedBy: actor,
	}
	if profile.Provider == scannerrelease.SignerManagedKeyless {
		return nil, fmt.Errorf("managed_keyless is deployment-owned and cannot be replaced by a customer signer profile")
	}
	if err := scannersigning.ValidateProfile(*profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func scannerSignerResponse(profile scannerrelease.SignerProfile) scannerSignerView {
	return scannerSignerView{
		ID: profile.ID, Name: profile.Name, Provider: profile.Provider,
		Algorithm:                 profile.Algorithm,
		KeyReference:              scannersigning.MaskReference(profile.KeyReference),
		SecretReference:           scannersigning.MaskReference(profile.SecretReference),
		SecretReferenceConfigured: profile.SecretReference != "",
		WorkloadIdentity:          profile.WorkloadIdentity,
		Identity:                  profile.Identity, Issuer: profile.Issuer, Subject: profile.Subject,
		TrustRootReference: scannersigning.MaskReference(profile.TrustRootReference),
		State:              profile.State, Revision: profile.Revision,
		RotatedFromID:    profile.RotatedFromID,
		RevocationReason: profile.RevocationReason, RevokedBy: profile.RevokedBy,
		RevokedAt: profile.RevokedAt, CreatedBy: profile.CreatedBy,
		CreatedAt: profile.CreatedAt, UpdatedAt: profile.UpdatedAt,
	}
}

func ScannerSupplyChainListSigners(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	profiles, err := store.ListSignerProfiles(
		r.Context(), r.URL.Query().Get("include_inactive") != "true",
	)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	views := make([]scannerSignerView, 0, len(profiles))
	for _, profile := range profiles {
		views = append(views, scannerSignerResponse(profile))
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: views})
}

func ScannerSupplyChainCreateSigner(w http.ResponseWriter, r *http.Request) {
	var input scannerSignerInput
	if !scannerDecode(w, r, &input) {
		return
	}
	profile, err := signerProfileFromInput(
		input, uuid.NewString(), scannerActor(r), 1,
	)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "signer_profile_invalid", err.Error())
		return
	}
	store, err := scannerReleaseStore()
	if err == nil {
		err = store.CreateSignerProfile(r.Context(), profile)
	}
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", `"1"`)
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{
		Data: scannerSignerResponse(*profile),
	})
}

func ScannerSupplyChainGetSigner(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	profile, err := store.GetSignerProfile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, profile.Revision))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: scannerSignerResponse(*profile),
	})
}

func ScannerSupplyChainRotateSigner(w http.ResponseWriter, r *http.Request) {
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	var input scannerSignerInput
	if !scannerDecode(w, r, &input) {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	current, err := store.GetSignerProfile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	if input.Name == "" {
		input.Name = current.Name
	}
	replacement, err := signerProfileFromInput(
		input, uuid.NewString(), scannerActor(r), expected+1,
	)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "signer_profile_invalid", err.Error())
		return
	}
	if err := store.RotateSignerProfile(
		r.Context(), current.ID, expected, replacement,
	); err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, replacement.Revision))
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{
		Data: scannerSignerResponse(*replacement),
	})
}

func ScannerSupplyChainRevokeSigner(w http.ResponseWriter, r *http.Request) {
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		response.WriteError(w, http.StatusUnprocessableEntity, "signer_revocation_invalid", "reason is required and must be at most 500 characters")
		return
	}
	store, err := scannerReleaseStore()
	if err == nil {
		err = store.RevokeSignerProfile(
			r.Context(), chi.URLParam(r, "id"), expected, request.Reason,
			scannerActor(r), time.Now().UTC(),
		)
	}
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	profile, err := store.GetSignerProfile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, profile.Revision))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: scannerSignerResponse(*profile),
	})
}
