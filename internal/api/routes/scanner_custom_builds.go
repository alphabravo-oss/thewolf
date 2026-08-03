package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannercustombuildworker"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

const scannerCustomBuildBase = "/api/v1/scanners/custom-builds"
const scannerCustomBuildTerminalEventID int64 = scannerrelease.CustomBuildTerminalEventID

type scannerCustomBuildCreateBody struct {
	Variants           []string `json:"variants"`
	Push               bool     `json:"push"`
	Platforms          []string `json:"platforms,omitempty"`
	Namespace          string   `json:"namespace,omitempty"`
	CredentialSecretID string   `json:"credential_secret_id,omitempty"`
	Reason             string   `json:"reason"`
}

type scannerCustomBuildMutationBody struct {
	Reason string `json:"reason"`
}

func CreateScannerCustomBuild(w http.ResponseWriter, r *http.Request) {
	var body scannerCustomBuildCreateBody
	if !scannerDecode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		response.WriteError(w, http.StatusBadRequest, "reason_required", "reason is required")
		return
	}
	if body.Push && strings.TrimSpace(body.CredentialSecretID) == "" {
		response.WriteError(w, http.StatusBadRequest, "credential_secret_id_required", "credential_secret_id is required for registry push")
		return
	}
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	inventory, created, err := enqueueScannerCustomBuild(r, body, key)
	if err != nil {
		scannerCustomBuildError(w, err)
		return
	}
	result := scannerCommandResponse{
		ID: inventory.Build.ID, State: string(inventory.Build.State),
		StatusURL: scannerCustomBuildBase + "/" + inventory.Build.ID,
		EventsURL: scannerCustomBuildBase + "/" + inventory.Build.ID + "/events",
	}
	if !created {
		w.Header().Set("Idempotent-Replay", "true")
	}
	scannerOperationAccepted(w, result)
}

func ListScannerCustomBuilds(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	state := scannerrelease.CustomBuildState(r.URL.Query().Get("state"))
	if state != "" && !validCustomBuildState(state) {
		response.WriteError(w, http.StatusBadRequest, "invalid_custom_build_state", "state is not a supported custom-build state")
		return
	}
	page, err := store.ListCustomBuilds(
		r.Context(),
		scannerrelease.CustomBuildFilter{
			State: state,
		},
		scannerPage(r),
	)
	if err != nil {
		scannerCustomBuildError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, scannerCursorResponse{
		Data: page.Items, Meta: scannerCursorMeta{NextCursor: page.NextCursor},
	})
}

func validCustomBuildState(state scannerrelease.CustomBuildState) bool {
	switch state {
	case scannerrelease.CustomBuildQueued, scannerrelease.CustomBuildClaimed,
		scannerrelease.CustomBuildRunning, scannerrelease.CustomBuildCompleted,
		scannerrelease.CustomBuildPartial, scannerrelease.CustomBuildFailed,
		scannerrelease.CustomBuildCancelled:
		return true
	default:
		return false
	}
}

func GetScannerCustomBuild(w http.ResponseWriter, r *http.Request) {
	inventory, err := getScannerCustomBuild(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerCustomBuildError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, inventory.Build.Version))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: inventory})
}

func CancelScannerCustomBuild(w http.ResponseWriter, r *http.Request) {
	mutateScannerCustomBuild(w, r, true)
}

func RetryScannerCustomBuild(w http.ResponseWriter, r *http.Request) {
	mutateScannerCustomBuild(w, r, false)
}

func mutateScannerCustomBuild(w http.ResponseWriter, r *http.Request, cancel bool) {
	var body scannerCustomBuildMutationBody
	if !scannerDecode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		response.WriteError(w, http.StatusBadRequest, "reason_required", "reason is required")
		return
	}
	version, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	command := scannerrelease.TransitionCommand{
		Actor: scannerActor(r), Reason: body.Reason, IdempotencyKey: key,
		PayloadJSON: `{"source":"api"}`,
	}
	var build *scannerrelease.CustomBuild
	if cancel {
		build, err = store.RequestCustomBuildCancellation(
			r.Context(), chi.URLParam(r, "id"), version, command, time.Now().UTC(),
		)
	} else {
		build, err = store.RetryCustomBuild(
			r.Context(), chi.URLParam(r, "id"), version, command, time.Now().UTC(),
		)
	}
	if err != nil {
		scannerCustomBuildError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: build})
}

func StreamScannerCustomBuildEvents(w http.ResponseWriter, r *http.Request) {
	after, ok := scannerCustomBuildLastEventID(w, r)
	if !ok {
		return
	}
	inventory, err := getScannerCustomBuild(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerCustomBuildError(w, err)
		return
	}
	flusher, ok := beginSSE(w)
	if !ok {
		return
	}
	streamPersistedCustomBuild(
		r.Context(), w, flusher, inventory.Build.ID, after, true,
	)
}

func enqueueScannerCustomBuild(
	r *http.Request,
	body scannerCustomBuildCreateBody,
	idempotencyKey string,
) (*scannerrelease.CustomBuildInventory, bool, error) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		return nil, false, errors.New("not authenticated")
	}
	variants := canonicalCustomBuildVariants(body.Variants)
	secretReference := strings.TrimSpace(body.CredentialSecretID)
	if body.Push {
		secret, err := scannerCustomBuildSecret(
			r.Context(), claims.UserID, secretReference,
		)
		if err != nil {
			return nil, false, err
		}
		secretReference = secret.ID
	}
	store, err := scannerReleaseStore()
	if err != nil {
		return nil, false, err
	}
	return store.CreateCustomBuild(
		r.Context(),
		scannerrelease.CustomBuildCreateRequest{
			ID: uuid.NewString(), UserID: claims.UserID, Variants: variants,
			Push: body.Push, Platforms: body.Platforms,
			Namespace: body.Namespace, SecretReference: secretReference,
			Actor: scannerActor(r), Reason: body.Reason,
			IdempotencyKey: idempotencyKey, MaxAttempts: 3,
		},
	)
}

func canonicalCustomBuildVariants(input []string) []string {
	if len(input) == 1 && input[0] == "all" {
		return []string{"default", "jvm", "rust", "codeql"}
	}
	order := map[string]int{"default": 0, "jvm": 1, "rust": 2, "codeql": 3}
	result := append([]string(nil), input...)
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if order[result[j]] < order[result[i]] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

func scannerCustomBuildSecret(
	ctx context.Context,
	userID, requestedID string,
) (*models.Secret, error) {
	if DefaultHandler == nil || DefaultHandler.Store == nil {
		return nil, errors.New("store is unavailable")
	}
	if requestedID != "" {
		secret, err := DefaultHandler.Store.GetSecretMetadataByID(ctx, requestedID)
		if err != nil {
			return nil, err
		}
		if secret.UserID != userID || secret.KeyType != models.KeyTypeDockerHubToken {
			return nil, sql.ErrNoRows
		}
		return secret, nil
	}
	available, err := DefaultHandler.Store.ListSecretMetadataByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range available {
		if available[i].KeyType == models.KeyTypeDockerHubToken {
			return &available[i], nil
		}
	}
	return nil, sql.ErrNoRows
}

func getScannerCustomBuild(
	ctx context.Context,
	id string,
) (*scannerrelease.CustomBuildInventory, error) {
	store, err := scannerReleaseStore()
	if err != nil {
		return nil, err
	}
	return store.GetCustomBuild(ctx, id)
}

func scannerCustomBuildError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		response.WriteError(w, http.StatusNotFound, "custom_build_not_found", "custom build or registry credential was not found")
	case errors.Is(err, scannerrelease.ErrVersionConflict):
		response.WriteError(w, http.StatusConflict, "stale_revision", "custom build changed; reload and retry")
	case errors.Is(err, scannerrelease.ErrIdempotencyConflict):
		response.WriteError(w, http.StatusConflict, "idempotency_conflict", "idempotency key is already bound to another custom build request")
	case errors.Is(err, scannerrelease.ErrInvalidTransition):
		response.WriteError(w, http.StatusConflict, "invalid_custom_build_state", "custom build state does not allow this operation")
	default:
		message := err.Error()
		if strings.Contains(message, "custom build") ||
			strings.Contains(message, "CodeQL") ||
			strings.Contains(message, "registry push") ||
			strings.Contains(message, "unsupported") ||
			strings.Contains(message, "duplicate") {
			response.WriteError(w, http.StatusUnprocessableEntity, "invalid_custom_build", message)
			return
		}
		response.WriteError(w, http.StatusInternalServerError, "custom_build_error", "custom build operation failed")
	}
}

func scannerCustomBuildLastEventID(
	w http.ResponseWriter,
	r *http.Request,
) (int64, bool) {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		return 0, true
	}
	sequence, err := strconv.ParseInt(value, 10, 64)
	if err != nil || sequence < 0 {
		response.WriteError(w, http.StatusBadRequest, "invalid_last_event_id", "Last-Event-ID must be a non-negative sequence")
		return 0, false
	}
	return sequence, true
}

func streamPersistedCustomBuild(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	buildID string,
	after int64,
	prefixLogs bool,
) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		store, err := scannerReleaseStore()
		if err != nil {
			writeSSEEvent(w, flusher, "error", map[string]string{"error": "custom build store unavailable"})
			return
		}
		logs, err := store.ListCustomBuildLogs(ctx, buildID, after, 200)
		if err != nil {
			return
		}
		for _, log := range logs {
			fmt.Fprintf(
				w, "id: %d\nevent: log\ndata: %s\n\n",
				log.Sequence, sseDataLine(log.Variant, log.Line, prefixLogs),
			) // nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
			flusher.Flush()
			after = log.Sequence
		}
		if len(logs) > 0 {
			// Drain all persisted log pages before emitting the stable terminal
			// event, including when the build completed before this client
			// connected.
			continue
		}
		inventory, err := store.GetCustomBuild(ctx, buildID)
		if err != nil {
			return
		}
		if customBuildTerminal(inventory.Build.State) {
			if after < scannerCustomBuildTerminalEventID {
				event := "done"
				if inventory.Build.State != scannerrelease.CustomBuildCompleted {
					event = "error"
				}
				payload, _ := json.Marshal(map[string]any{
					"id": inventory.Build.ID, "state": inventory.Build.State,
					"variants": inventory.Variants,
				})
				fmt.Fprintf(
					w, "id: %d\nevent: %s\ndata: %s\n\n",
					scannerCustomBuildTerminalEventID, event, payload,
				) // nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
				flusher.Flush()
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func sseDataLine(variant, line string, prefix bool) string {
	line = strings.ReplaceAll(line, "\r", "")
	line = strings.ReplaceAll(line, "\n", " ")
	if !prefix || variant == "" {
		return line
	}
	return "[" + variant + "] " + line
}

func customBuildTerminal(state scannerrelease.CustomBuildState) bool {
	switch state {
	case scannerrelease.CustomBuildCompleted, scannerrelease.CustomBuildPartial,
		scannerrelease.CustomBuildFailed, scannerrelease.CustomBuildCancelled:
		return true
	default:
		return false
	}
}

// executeScannerCustomBuildTestHook preserves the existing Docker-free API
// characterization seam. Production leaves ScannerBuildFn nil, so the API
// process only enqueues and never resolves credentials or executes Docker.
func executeScannerCustomBuildTestHook(
	ctx context.Context,
	inventory *scannerrelease.CustomBuildInventory,
) error {
	if ScannerBuildFn == nil {
		return nil
	}
	worker, err := scannercustombuildworker.New(scannercustombuildworker.Config{
		Store:    DefaultHandler.Store.ScannerReleases(),
		WorkerID: "api-test-hook",
		Once:     true,
		Executor: scannercustombuildworker.ExecutorFunc(ScannerBuildFn),
		Credentials: scannercustombuildworker.CredentialResolverFunc(
			func(ctx context.Context, reference, userID string) (string, string, error) {
				secret, err := DefaultHandler.Store.GetSecretByID(ctx, reference)
				if err != nil {
					return "", "", err
				}
				if secret.UserID != userID ||
					secret.KeyType != models.KeyTypeDockerHubToken {
					return "", "", sql.ErrNoRows
				}
				value, err := secrets.Decrypt(secret.EncryptedValue)
				return secret.KeyName, value, err
			},
		),
		PollInterval: time.Millisecond, HeartbeatInterval: time.Second,
		LeaseDuration: 3 * time.Second, OperationTimeout: time.Minute,
	})
	if err != nil {
		return err
	}
	_, err = worker.Once(ctx)
	return err
}
