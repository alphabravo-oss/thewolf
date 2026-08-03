package routes

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	maxScannerDiffArtifactBytes = int64(8 << 20)
	maxScannerDiffResponseBytes = 256 << 10
)

var scannerDiffMediaTypes = map[string]struct{}{
	"text/plain":          {},
	"text/x-diff":         {},
	"text/x-patch":        {},
	"text/x-unified-diff": {},
	"application/vnd.wolf.scanner-diff.v1+text": {},
}

type scannerArtifactDiffResponse struct {
	OwnerType     string `json:"owner_type"`
	OwnerID       string `json:"owner_id"`
	Kind          string `json:"kind"`
	Format        string `json:"format"`
	Available     bool   `json:"available"`
	Content       string `json:"content"`
	Truncated     bool   `json:"truncated"`
	TotalBytes    int64  `json:"total_bytes"`
	ReturnedBytes int    `json:"returned_bytes"`
	TotalLines    int    `json:"total_lines"`
	ReturnedLines int    `json:"returned_lines"`
	Digest        string `json:"digest,omitempty"`
	MediaType     string `json:"media_type,omitempty"`
}

type scannerDiffReadError struct {
	status  int
	code    string
	message string
}

func (e *scannerDiffReadError) Error() string { return e.message }

func ScannerSupplyChainGetCandidateDiff(w http.ResponseWriter, r *http.Request) {
	scannerSupplyChainGetArtifactDiff(w, r, "candidate")
}

func ScannerSupplyChainGetReleaseDiff(w http.ResponseWriter, r *http.Request) {
	scannerSupplyChainGetArtifactDiff(w, r, "release")
}

func scannerSupplyChainGetArtifactDiff(
	w http.ResponseWriter,
	r *http.Request,
	ownerType string,
) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	ownerID := chi.URLParam(r, "id")
	switch ownerType {
	case "candidate":
		_, err = store.GetCandidate(r.Context(), ownerID)
	case "release":
		_, err = store.GetRelease(r.Context(), ownerID)
	default:
		err = errors.New("unsupported scanner diff owner")
	}
	if err != nil {
		scannerWriteError(w, err)
		return
	}

	kind := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "kind")))
	artifactType := ""
	switch kind {
	case "manifest":
		artifactType = "manifest_diff"
	case "lock":
		artifactType = "lock_diff"
	default:
		response.WriteError(
			w,
			http.StatusBadRequest,
			"scanner_diff_kind_invalid",
			"diff kind must be manifest or lock",
		)
		return
	}

	releaseID, candidateID := "", ""
	if ownerType == "candidate" {
		candidateID = ownerID
	} else {
		releaseID = ownerID
	}
	records, err := store.ListArtifacts(r.Context(), releaseID, candidateID)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	var selected *scannerrelease.ReleaseArtifact
	for index := range records {
		if records[index].ArtifactType == artifactType {
			selected = &records[index]
		}
	}

	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if selected == nil {
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
			Data: scannerArtifactDiffResponse{
				OwnerType: ownerType,
				OwnerID:   ownerID,
				Kind:      kind,
				Format:    "unified",
			},
		})
		return
	}

	diff, err := readScannerArtifactDiff(*selected)
	if err != nil {
		var readError *scannerDiffReadError
		if errors.As(err, &readError) {
			response.WriteError(w, readError.status, readError.code, readError.message)
			return
		}
		response.WriteError(
			w,
			http.StatusInternalServerError,
			"scanner_diff_unavailable",
			"scanner diff content could not be loaded",
		)
		return
	}
	diff.OwnerType = ownerType
	diff.OwnerID = ownerID
	diff.Kind = kind
	w.Header().Set("ETag", `"`+strings.TrimPrefix(diff.Digest, "sha256:")+`"`)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: diff})
}

func readScannerArtifactDiff(
	artifact scannerrelease.ReleaseArtifact,
) (scannerArtifactDiffResponse, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(artifact.MediaType))
	if err != nil {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusUnsupportedMediaType,
			"scanner_diff_media_type_invalid",
			"scanner diff artifact has an invalid media type",
		)
	}
	if _, allowed := scannerDiffMediaTypes[strings.ToLower(mediaType)]; !allowed {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusUnsupportedMediaType,
			"scanner_diff_media_type_unsupported",
			"scanner diff artifact is not stored as an approved text diff type",
		)
	}
	path, err := scannerDiffArtifactPath(artifact.URI)
	if err != nil {
		return scannerArtifactDiffResponse{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_artifact_unavailable",
			"scanner diff artifact is missing from durable storage",
		)
	}
	if !info.Mode().IsRegular() {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_artifact_invalid",
			"scanner diff artifact is not a regular file",
		)
	}
	if info.Size() != artifact.SizeBytes {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusConflict,
			"scanner_diff_artifact_changed",
			"scanner diff artifact size no longer matches its immutable record",
		)
	}
	if info.Size() > maxScannerDiffArtifactBytes {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusRequestEntityTooLarge,
			"scanner_diff_artifact_too_large",
			"scanner diff artifact exceeds the safe viewer processing limit",
		)
	}

	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_artifact_invalid",
			"scanner diff artifact path could not be verified",
		)
	}
	file, err := os.Open(resolvedPath)
	if err != nil {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_artifact_unavailable",
			"scanner diff artifact could not be opened",
		)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusConflict,
			"scanner_diff_artifact_changed",
			"scanner diff artifact changed while it was being opened",
		)
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxScannerDiffArtifactBytes+1))
	if err != nil {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_artifact_unavailable",
			"scanner diff artifact could not be read",
		)
	}
	if int64(len(payload)) != info.Size() {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusConflict,
			"scanner_diff_artifact_changed",
			"scanner diff artifact changed while it was being read",
		)
	}
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if !strings.EqualFold(digest, strings.TrimSpace(artifact.Digest)) {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusConflict,
			"scanner_diff_artifact_digest_mismatch",
			"scanner diff artifact digest no longer matches its immutable record",
		)
	}
	if !utf8.Valid(payload) || bytes.IndexByte(payload, 0) >= 0 {
		return scannerArtifactDiffResponse{}, scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_artifact_not_text",
			"scanner diff artifact is not valid UTF-8 text",
		)
	}

	returned := payload
	truncated := len(returned) > maxScannerDiffResponseBytes
	if truncated {
		returned = returned[:maxScannerDiffResponseBytes]
		for len(returned) > 0 && !utf8.Valid(returned) {
			returned = returned[:len(returned)-1]
		}
	}
	return scannerArtifactDiffResponse{
		Format:        "unified",
		Available:     true,
		Content:       string(returned),
		Truncated:     truncated,
		TotalBytes:    int64(len(payload)),
		ReturnedBytes: len(returned),
		TotalLines:    scannerDiffLineCount(payload),
		ReturnedLines: scannerDiffLineCount(returned),
		Digest:        digest,
		MediaType:     mediaType,
	}, nil
}

func scannerDiffArtifactPath(uri string) (string, error) {
	if artifacts.Global == nil || strings.TrimSpace(artifacts.Global.Root()) == "" {
		return "", scannerDiffError(
			http.StatusServiceUnavailable,
			"scanner_diff_storage_unavailable",
			"artifact storage is not configured",
		)
	}
	root, err := filepath.Abs(artifacts.Global.Root())
	if err != nil {
		return "", scannerDiffError(
			http.StatusServiceUnavailable,
			"scanner_diff_storage_unavailable",
			"artifact storage root could not be resolved",
		)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", scannerDiffError(
			http.StatusServiceUnavailable,
			"scanner_diff_storage_unavailable",
			"artifact storage root could not be verified",
		)
	}
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.User != nil || parsed.Opaque != "" {
		return "", scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_uri_invalid",
			"scanner diff artifact URI is invalid",
		)
	}
	var candidate string
	switch strings.ToLower(parsed.Scheme) {
	case "":
		if parsed.Host != "" {
			return "", scannerDiffError(
				http.StatusUnprocessableEntity,
				"scanner_diff_uri_invalid",
				"scanner diff artifact URI is invalid",
			)
		}
		candidate = parsed.Path
	case "file":
		if parsed.Host != "" {
			return "", scannerDiffError(
				http.StatusUnprocessableEntity,
				"scanner_diff_uri_invalid",
				"scanner diff file URI must not contain a host",
			)
		}
		candidate = parsed.Path
	default:
		return "", scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_uri_unsupported",
			"scanner diff artifact URI does not reference local durable storage",
		)
	}
	if candidate == "" || strings.IndexByte(candidate, 0) >= 0 {
		return "", scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_uri_invalid",
			"scanner diff artifact URI is empty or invalid",
		)
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, filepath.FromSlash(candidate))
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_uri_invalid",
			"scanner diff artifact URI could not be resolved",
		)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		// Preserve the generic missing-artifact response from the read step.
		resolvedCandidate = candidate
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return "", scannerDiffError(
			http.StatusUnprocessableEntity,
			"scanner_diff_uri_outside_storage",
			"scanner diff artifact URI is outside durable artifact storage",
		)
	}
	return candidate, nil
}

func scannerDiffLineCount(value []byte) int {
	if len(value) == 0 {
		return 0
	}
	lines := bytes.Count(value, []byte{'\n'})
	if value[len(value)-1] != '\n' {
		lines++
	}
	return lines
}

func scannerDiffError(status int, code, message string) error {
	return &scannerDiffReadError{status: status, code: code, message: message}
}
