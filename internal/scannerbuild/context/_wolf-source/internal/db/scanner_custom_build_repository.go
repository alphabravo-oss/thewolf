package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/scannerbuild"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	customBuildAggregate = "custom_build"
	customBuildMaxLogs   = scannerrelease.CustomBuildMaxLogLines
	customBuildMaxBytes  = scannerrelease.CustomBuildMaxLogBytes
	customBuildMaxLine   = scannerrelease.CustomBuildMaxLogLineBytes
)

var customBuildVariantOrder = map[string]int{
	"default": 0,
	"jvm":     1,
	"rust":    2,
	"codeql":  3,
}

var customBuildPlatforms = map[string]struct{}{
	"linux/amd64": {},
	"linux/arm64": {},
}

func normalizeCustomBuildCreate(
	request scannerrelease.CustomBuildCreateRequest,
) (scannerrelease.CustomBuildCreateRequest, string, error) {
	request.ID = strings.TrimSpace(request.ID)
	request.UserID = strings.TrimSpace(request.UserID)
	request.Actor = strings.TrimSpace(request.Actor)
	request.Reason = strings.TrimSpace(request.Reason)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.Namespace = strings.TrimSpace(request.Namespace)
	request.SecretReference = strings.TrimSpace(request.SecretReference)
	if request.ID == "" {
		request.ID = uuid.NewString()
	}
	if request.UserID == "" || request.Actor == "" || request.Reason == "" ||
		request.IdempotencyKey == "" {
		return request, "", errors.New("custom build requires user, actor, reason, and idempotency key")
	}
	if len(request.Reason) > 2048 || len(request.IdempotencyKey) > 200 ||
		len(request.Namespace) > 255 || len(request.SecretReference) > 255 {
		return request, "", errors.New("custom build request metadata exceeds its bounded size")
	}
	if request.Namespace == "" {
		request.Namespace = "alphabravodevops"
	}
	for _, character := range request.Namespace {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' && character != '.' {
			return request, "", errors.New("custom build namespace is invalid")
		}
	}
	if len(request.Variants) == 0 {
		return request, "", errors.New("custom build requires at least one variant")
	}
	seen := make(map[string]struct{}, len(request.Variants))
	for _, variant := range request.Variants {
		if _, ok := customBuildVariantOrder[variant]; !ok {
			return request, "", fmt.Errorf("unsupported custom build variant %q", variant)
		}
		if _, duplicate := seen[variant]; duplicate {
			return request, "", fmt.Errorf("duplicate custom build variant %q", variant)
		}
		seen[variant] = struct{}{}
	}
	for i := 1; i < len(request.Variants); i++ {
		if customBuildVariantOrder[request.Variants[i-1]] >
			customBuildVariantOrder[request.Variants[i]] {
			return request, "", errors.New("custom build variants must use canonical order")
		}
	}
	platformSeen := make(map[string]struct{}, len(request.Platforms))
	for _, platform := range request.Platforms {
		if _, ok := customBuildPlatforms[platform]; !ok {
			return request, "", fmt.Errorf("unsupported custom build platform %q", platform)
		}
		if _, duplicate := platformSeen[platform]; duplicate {
			return request, "", fmt.Errorf("duplicate custom build platform %q", platform)
		}
		platformSeen[platform] = struct{}{}
	}
	if !request.Push && len(request.Platforms) > 1 {
		return request, "", errors.New("local custom builds support at most one platform")
	}
	if request.Push && request.SecretReference == "" {
		return request, "", errors.New("registry push requires an opaque secret reference")
	}
	if !request.Push && request.SecretReference != "" {
		return request, "", errors.New("local custom builds must not carry a registry secret reference")
	}
	if request.MaxAttempts <= 0 {
		request.MaxAttempts = 3
	}
	if request.MaxAttempts > 10 {
		return request, "", errors.New("custom build max attempts exceeds 10")
	}
	digestPayload := struct {
		UserID          string   `json:"user_id"`
		Variants        []string `json:"variants"`
		Push            bool     `json:"push"`
		Platforms       []string `json:"platforms"`
		Namespace       string   `json:"namespace"`
		SecretReference string   `json:"secret_reference"`
		MaxAttempts     int      `json:"max_attempts"`
	}{
		request.UserID, request.Variants, request.Push, request.Platforms,
		request.Namespace, request.SecretReference, request.MaxAttempts,
	}
	encoded, _ := json.Marshal(digestPayload)
	return request, sha256String(encoded), nil
}

func (r *scannerReleaseRepository) CreateCustomBuild(
	ctx context.Context,
	request scannerrelease.CustomBuildCreateRequest,
) (*scannerrelease.CustomBuildInventory, bool, error) {
	request, requestDigest, err := normalizeCustomBuildCreate(request)
	if err != nil {
		return nil, false, err
	}
	now := utcNow()
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var existing scannerrelease.CustomBuild
	err = tx.GetContext(ctx, &existing, r.db.Rebind(
		`SELECT * FROM scanner_custom_builds WHERE idempotency_key = ?`),
		request.IdempotencyKey)
	if err == nil {
		if existing.RequestDigest != requestDigest {
			return nil, false, scannerrelease.ErrIdempotencyConflict
		}
		var variants []scannerrelease.CustomBuildVariant
		if err := tx.SelectContext(ctx, &variants, r.db.Rebind(
			`SELECT * FROM scanner_custom_build_variants
			 WHERE build_id = ? ORDER BY ordinal`), existing.ID); err != nil {
			return nil, false, err
		}
		return &scannerrelease.CustomBuildInventory{
			Build: existing, Variants: variants,
		}, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	version, publishVersion, err := r.reserveCustomBuildVersionTx(ctx, tx, request.Push)
	if err != nil {
		return nil, false, err
	}
	variantsJSON, _ := json.Marshal(request.Variants)
	platformsJSON, _ := json.Marshal(request.Platforms)
	build := scannerrelease.CustomBuild{
		ID: request.ID, UserID: request.UserID, VariantsJSON: string(variantsJSON),
		Push: request.Push, PlatformsJSON: string(platformsJSON),
		Namespace: request.Namespace, ReservedVersion: version,
		PublishVersion: publishVersion, SecretReference: request.SecretReference,
		State: scannerrelease.CustomBuildQueued, Actor: request.Actor,
		Reason: request.Reason, IdempotencyKey: request.IdempotencyKey,
		RequestDigest: requestDigest, Attempt: 0, MaxAttempts: request.MaxAttempts,
		AvailableAt: now, SummaryJSON: "{}", Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := r.execTx(ctx, tx,
		`INSERT INTO scanner_custom_builds
		 (id, user_id, variants_json, push, platforms_json, namespace,
		  reserved_version, publish_version, secret_reference, state, actor,
		  reason, idempotency_key, request_digest, worker_id, lease_token,
		  lease_expires_at, heartbeat_at, attempt, max_attempts, available_at,
		  cancel_requested_at, error_class, error_detail, summary_json, version,
		  started_at, completed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', NULL,
		         NULL, 0, ?, ?, NULL, '', '', '{}', 1, NULL, NULL, ?, ?)`,
		build.ID, build.UserID, build.VariantsJSON, build.Push,
		build.PlatformsJSON, build.Namespace, build.ReservedVersion,
		build.PublishVersion, build.SecretReference, build.State, build.Actor,
		build.Reason, build.IdempotencyKey, build.RequestDigest,
		build.MaxAttempts, build.AvailableAt, build.CreatedAt, build.UpdatedAt,
	); err != nil {
		return nil, false, err
	}
	var variants []scannerrelease.CustomBuildVariant
	for ordinal, variantName := range request.Variants {
		variant := scannerrelease.CustomBuildVariant{
			ID: uuid.NewString(), BuildID: build.ID, Variant: variantName,
			Ordinal: ordinal, State: scannerrelease.CustomBuildVariantQueued,
			RefsJSON: "[]", Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		if _, err := r.execTx(ctx, tx,
			`INSERT INTO scanner_custom_build_variants
			 (id, build_id, variant, ordinal, state, refs_json, digest,
			  loaded_locally, pushed, error_class, error_detail, version,
			  started_at, completed_at, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, '[]', '', ?, ?, '', '', 1, NULL, NULL, ?, ?)`,
			variant.ID, variant.BuildID, variant.Variant, variant.Ordinal,
			variant.State, false, false, now, now,
		); err != nil {
			return nil, false, err
		}
		variants = append(variants, variant)
	}
	command := scannerrelease.TransitionCommand{
		Actor: request.Actor, Reason: request.Reason,
		IdempotencyKey: request.IdempotencyKey,
		PayloadJSON: fmt.Sprintf(
			`{"requestDigest":%q,"push":%t}`, requestDigest, request.Push,
		),
	}
	if _, err := r.appendEventTx(
		ctx, tx, customBuildAggregate, build.ID, "custom_build.queued",
		"", string(build.State), command, now,
	); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &scannerrelease.CustomBuildInventory{Build: build, Variants: variants}, true, nil
}

func (r *scannerReleaseRepository) reserveCustomBuildVersionTx(
	ctx context.Context,
	tx *sqlx.Tx,
	push bool,
) (string, *string, error) {
	const key = "scanners_image_version"
	const fallback = "2.0.0"
	if _, err := r.execTx(ctx, tx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO NOTHING`, key, fallback); err != nil {
		return "", nil, err
	}
	var current string
	if strings.Contains(strings.ToLower(r.db.DriverName()), "postgres") {
		if err := tx.GetContext(ctx, &current,
			`SELECT value FROM settings WHERE key = $1 FOR UPDATE`, key); err != nil {
			return "", nil, err
		}
	} else {
		if _, err := r.execTx(ctx, tx,
			`UPDATE settings SET value = value WHERE key = ?`, key); err != nil {
			return "", nil, err
		}
		if err := tx.GetContext(ctx, &current, r.db.Rebind(
			`SELECT value FROM settings WHERE key = ?`), key); err != nil {
			return "", nil, err
		}
	}
	current = strings.TrimSpace(current)
	if current == "" {
		current = fallback
	}
	if !push {
		return current, nil, nil
	}
	next := scannerbuild.BumpPatch(current)
	if _, err := r.execTx(ctx, tx,
		`UPDATE settings SET value = ? WHERE key = ? AND value = ?`,
		next, key, current); err != nil {
		return "", nil, err
	}
	return next, &next, nil
}

func (r *scannerReleaseRepository) GetCustomBuild(
	ctx context.Context,
	id string,
) (*scannerrelease.CustomBuildInventory, error) {
	var build scannerrelease.CustomBuild
	if err := r.get(ctx, &build,
		`SELECT * FROM scanner_custom_builds WHERE id = ?`, id); err != nil {
		return nil, err
	}
	var variants []scannerrelease.CustomBuildVariant
	if err := r.selectRows(ctx, &variants,
		`SELECT * FROM scanner_custom_build_variants
		 WHERE build_id = ? ORDER BY ordinal`, id); err != nil {
		return nil, err
	}
	return &scannerrelease.CustomBuildInventory{Build: build, Variants: variants}, nil
}

func (r *scannerReleaseRepository) ListCustomBuilds(
	ctx context.Context,
	filter scannerrelease.CustomBuildFilter,
	page scannerrelease.PageRequest,
) (scannerrelease.CustomBuildPage, error) {
	var query strings.Builder
	query.WriteString(`SELECT * FROM scanner_custom_builds WHERE 1 = 1`)
	var args []any
	if filter.State != "" {
		query.WriteString(` AND state = ?`)
		args = append(args, filter.State)
	}
	if filter.UserID != "" {
		query.WriteString(` AND user_id = ?`)
		args = append(args, filter.UserID)
	}
	if err := appendCursorCondition(&query, &args, page.Cursor); err != nil {
		return scannerrelease.CustomBuildPage{}, err
	}
	limit := pageLimit(page.Limit)
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ?`)
	args = append(args, limit+1)
	var items []scannerrelease.CustomBuild
	if err := r.selectRows(ctx, &items, query.String(), args...); err != nil {
		return scannerrelease.CustomBuildPage{}, err
	}
	items, next := pageCursor(items, limit, func(item scannerrelease.CustomBuild) (time.Time, string) {
		return item.CreatedAt, item.ID
	})
	return scannerrelease.CustomBuildPage{Items: items, NextCursor: next}, nil
}

func (r *scannerReleaseRepository) ClaimNextCustomBuild(
	ctx context.Context,
	workerID string,
	now, leaseUntil time.Time,
) (*scannerrelease.CustomBuild, error) {
	if strings.TrimSpace(workerID) == "" || now.IsZero() || !leaseUntil.After(now) {
		return nil, errors.New("invalid custom build claim")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var queued []scannerrelease.CustomBuild
	if err := tx.SelectContext(ctx, &queued, r.db.Rebind(
		`SELECT * FROM scanner_custom_builds
		 WHERE state = ? AND available_at <= ? AND cancel_requested_at IS NULL
		   AND attempt < max_attempts
		 ORDER BY available_at, created_at, id LIMIT 20`),
		scannerrelease.CustomBuildQueued, now); err != nil {
		return nil, err
	}
	for i := range queued {
		token := uuid.NewString()
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_custom_builds
			 SET state = ?, worker_id = ?, lease_token = ?, lease_expires_at = ?,
			     heartbeat_at = ?, attempt = attempt + 1,
			     error_class = '', error_detail = '', version = version + 1,
			     updated_at = ?
			 WHERE id = ? AND version = ? AND state = ?
			   AND cancel_requested_at IS NULL AND attempt < max_attempts`,
			scannerrelease.CustomBuildClaimed, workerID, token, leaseUntil,
			now, now, queued[i].ID, queued[i].Version,
			scannerrelease.CustomBuildQueued)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		var claimed scannerrelease.CustomBuild
		if err := tx.GetContext(ctx, &claimed, r.db.Rebind(
			`SELECT * FROM scanner_custom_builds WHERE id = ?`),
			queued[i].ID); err != nil {
			return nil, err
		}
		if _, err := r.appendEventTx(
			ctx, tx, customBuildAggregate, claimed.ID, "custom_build.claimed",
			string(scannerrelease.CustomBuildQueued), string(claimed.State),
			scannerrelease.TransitionCommand{
				Actor: workerID, Reason: "custom build worker claimed operation",
				IdempotencyKey: customBuildLeaseEventKey("claim", token, ""),
				PayloadJSON:    `{"lease":"acquired"}`,
			}, now,
		); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &claimed, nil
	}
	return nil, tx.Commit()
}

func (r *scannerReleaseRepository) StartCustomBuild(
	ctx context.Context,
	id, token string,
	at time.Time,
) (*scannerrelease.CustomBuild, error) {
	if id == "" || token == "" {
		return nil, errors.New("invalid custom build start")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.CustomBuild
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT * FROM scanner_custom_builds WHERE id = ?`), id); err != nil {
		return nil, err
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_custom_builds
		 SET state = ?, started_at = COALESCE(started_at, ?),
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND state = ? AND lease_token = ?
		   AND lease_expires_at > ? AND cancel_requested_at IS NULL`,
		scannerrelease.CustomBuildRunning, at, at, id,
		scannerrelease.CustomBuildClaimed, token, at)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	if _, err := r.appendEventTx(
		ctx, tx, customBuildAggregate, id, "custom_build.running",
		string(current.State), string(scannerrelease.CustomBuildRunning),
		scannerrelease.TransitionCommand{
			Actor: current.WorkerID, Reason: "custom build execution started",
			IdempotencyKey: customBuildLeaseEventKey("start", token, ""),
			PayloadJSON:    `{"worker":"claimed"}`,
		}, at,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCustomBuildOnly(ctx, id)
}

func (r *scannerReleaseRepository) GetCustomBuildOnly(
	ctx context.Context,
	id string,
) (*scannerrelease.CustomBuild, error) {
	var build scannerrelease.CustomBuild
	return &build, r.get(ctx, &build,
		`SELECT * FROM scanner_custom_builds WHERE id = ?`, id)
}

func (r *scannerReleaseRepository) HeartbeatCustomBuild(
	ctx context.Context,
	id, token string,
	now, leaseUntil time.Time,
) (scannerrelease.CustomBuildLeaseStatus, error) {
	if !leaseUntil.After(now) {
		return scannerrelease.CustomBuildLeaseStatus{}, errors.New("lease expiration must follow heartbeat")
	}
	// The opaque lease token is the fencing value. Permit its current owner to
	// renew after a scheduler or GC pause as long as a reclaimer has not changed
	// the token/state; ReclaimStaleCustomBuilds rechecks the deadline atomically.
	if _, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_custom_builds
		 SET heartbeat_at = ?, lease_expires_at = ?, updated_at = ?
		 WHERE id = ? AND lease_token = ? AND state IN (?, ?)
		   AND cancel_requested_at IS NULL`),
		now, leaseUntil, now, id, token, scannerrelease.CustomBuildClaimed,
		scannerrelease.CustomBuildRunning); err != nil {
		return scannerrelease.CustomBuildLeaseStatus{}, err
	}
	var status struct {
		State             scannerrelease.CustomBuildState `db:"state"`
		Version           int64                           `db:"version"`
		LeaseToken        string                          `db:"lease_token"`
		LeaseExpiresAt    *time.Time                      `db:"lease_expires_at"`
		CancelRequestedAt *time.Time                      `db:"cancel_requested_at"`
	}
	if err := r.get(ctx, &status,
		`SELECT state, version, lease_token, lease_expires_at, cancel_requested_at
		 FROM scanner_custom_builds WHERE id = ?`, id); err != nil {
		return scannerrelease.CustomBuildLeaseStatus{}, err
	}
	return scannerrelease.CustomBuildLeaseStatus{
		Current: status.LeaseToken == token && status.LeaseExpiresAt != nil &&
			status.LeaseExpiresAt.After(now) &&
			(status.State == scannerrelease.CustomBuildClaimed ||
				status.State == scannerrelease.CustomBuildRunning),
		CancelRequested: status.CancelRequestedAt != nil,
		State:           status.State, Version: status.Version,
	}, nil
}

func (r *scannerReleaseRepository) StartCustomBuildVariant(
	ctx context.Context,
	buildID, variant, token string,
	at time.Time,
) (*scannerrelease.CustomBuildVariant, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireCustomBuildLeaseTx(ctx, r, tx, buildID, token, at); err != nil {
		return nil, err
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_custom_build_variants
		 SET state = ?, started_at = COALESCE(started_at, ?),
		     version = version + 1, updated_at = ?
		 WHERE build_id = ? AND variant = ? AND state = ?`,
		scannerrelease.CustomBuildVariantRunning, at, at, buildID, variant,
		scannerrelease.CustomBuildVariantQueued)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrVersionConflict
	}
	var resultVariant scannerrelease.CustomBuildVariant
	if err := tx.GetContext(ctx, &resultVariant, r.db.Rebind(
		`SELECT * FROM scanner_custom_build_variants
		 WHERE build_id = ? AND variant = ?`), buildID, variant); err != nil {
		return nil, err
	}
	if _, err := r.appendEventTx(
		ctx, tx, customBuildAggregate, buildID, "custom_build.variant_running",
		string(scannerrelease.CustomBuildRunning),
		string(scannerrelease.CustomBuildRunning),
		scannerrelease.TransitionCommand{
			Actor:  "custom-build-worker",
			Reason: "scanner variant build started",
			IdempotencyKey: customBuildLeaseEventKey(
				"variant-start", token, variant,
			),
			PayloadJSON: fmt.Sprintf(`{"variant":%q}`, variant),
		}, at,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &resultVariant, nil
}

func (r *scannerReleaseRepository) CompleteCustomBuildVariant(
	ctx context.Context,
	buildID, variant, token string,
	resultValue scannerrelease.CustomBuildVariantResult,
	at time.Time,
) (*scannerrelease.CustomBuildVariant, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireCustomBuildLeaseTx(ctx, r, tx, buildID, token, at); err != nil {
		return nil, err
	}
	state := scannerrelease.CustomBuildVariantCompleted
	if resultValue.ErrorClass != "" {
		state = scannerrelease.CustomBuildVariantFailed
	}
	refsJSON, _ := json.Marshal(resultValue.Refs)
	errorClass := boundedCustomBuildError(resultValue.ErrorClass, 64)
	errorDetail := boundedCustomBuildError(resultValue.ErrorDetail, 512)
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_custom_build_variants
		 SET state = ?, refs_json = ?, digest = ?, loaded_locally = ?,
		     pushed = ?, error_class = ?, error_detail = ?, completed_at = ?,
		     version = version + 1, updated_at = ?
		 WHERE build_id = ? AND variant = ? AND state = ?`,
		state, string(refsJSON), resultValue.Digest, resultValue.LoadedLocally,
		resultValue.Pushed, errorClass, errorDetail, at, at, buildID, variant,
		scannerrelease.CustomBuildVariantRunning)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrVersionConflict
	}
	var completed scannerrelease.CustomBuildVariant
	if err := tx.GetContext(ctx, &completed, r.db.Rebind(
		`SELECT * FROM scanner_custom_build_variants
		 WHERE build_id = ? AND variant = ?`), buildID, variant); err != nil {
		return nil, err
	}
	if _, err := r.appendEventTx(
		ctx, tx, customBuildAggregate, buildID,
		"custom_build.variant_"+string(state),
		string(scannerrelease.CustomBuildRunning),
		string(scannerrelease.CustomBuildRunning),
		scannerrelease.TransitionCommand{
			Actor:  "custom-build-worker",
			Reason: "scanner variant build finished",
			IdempotencyKey: customBuildLeaseEventKey(
				"variant-complete", token, variant,
			),
			PayloadJSON: fmt.Sprintf(
				`{"variant":%q,"state":%q}`, variant, state,
			),
		}, at,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &completed, nil
}

func requireCustomBuildLeaseTx(
	ctx context.Context,
	repository *scannerReleaseRepository,
	tx *sqlx.Tx,
	id, token string,
	at time.Time,
) error {
	var current struct {
		Token             string                          `db:"lease_token"`
		State             scannerrelease.CustomBuildState `db:"state"`
		Expires           *time.Time                      `db:"lease_expires_at"`
		CancelRequestedAt *time.Time                      `db:"cancel_requested_at"`
	}
	if err := tx.GetContext(ctx, &current, repository.db.Rebind(
		`SELECT lease_token, state, lease_expires_at, cancel_requested_at
		 FROM scanner_custom_builds WHERE id = ?`), id); err != nil {
		return err
	}
	if current.Token != token || current.Expires == nil ||
		!current.Expires.After(at) ||
		(current.State != scannerrelease.CustomBuildClaimed &&
			current.State != scannerrelease.CustomBuildRunning) {
		return scannerrelease.ErrLeaseNotOwned
	}
	if current.CancelRequestedAt != nil {
		return context.Canceled
	}
	return nil
}

func (r *scannerReleaseRepository) AppendCustomBuildLog(
	ctx context.Context,
	buildID, variant, token, line string,
	redacted bool,
	at time.Time,
) (*scannerrelease.CustomBuildLog, error) {
	if len(line) > customBuildMaxLine {
		return nil, errors.New("custom build log line exceeds maximum size")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireCustomBuildLeaseTx(ctx, r, tx, buildID, token, at); err != nil {
		return nil, err
	}
	var usage struct {
		Sequence int64 `db:"sequence"`
		Lines    int   `db:"lines"`
		Bytes    int   `db:"bytes"`
	}
	if err := tx.GetContext(ctx, &usage,
		r.db.Rebind(`SELECT COALESCE(MAX(sequence), 0) AS sequence,
		    COUNT(*) AS lines, COALESCE(SUM(LENGTH(line)), 0) AS bytes
		 FROM scanner_custom_build_logs WHERE build_id = ?`), buildID); err != nil {
		return nil, err
	}
	if usage.Lines >= customBuildMaxLogs ||
		usage.Bytes+len(line) > customBuildMaxBytes {
		return nil, scannerrelease.ErrCustomBuildLogBudget
	}
	log := &scannerrelease.CustomBuildLog{
		BuildID: buildID, Sequence: usage.Sequence + 1, Variant: variant,
		Line: line, Redacted: redacted, CreatedAt: at,
	}
	if _, err := r.execTx(ctx, tx,
		`INSERT INTO scanner_custom_build_logs
		 (build_id, sequence, variant, line, redacted, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		log.BuildID, log.Sequence, log.Variant, log.Line, log.Redacted,
		log.CreatedAt); err != nil {
		return nil, err
	}
	return log, tx.Commit()
}

func (r *scannerReleaseRepository) ListCustomBuildLogs(
	ctx context.Context,
	buildID string,
	after int64,
	limit int,
) ([]scannerrelease.CustomBuildLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var logs []scannerrelease.CustomBuildLog
	return logs, r.selectRows(ctx, &logs,
		`SELECT * FROM scanner_custom_build_logs
		 WHERE build_id = ? AND sequence > ?
		 ORDER BY sequence LIMIT ?`, buildID, after, limit)
}

func (r *scannerReleaseRepository) FinalizeCustomBuild(
	ctx context.Context,
	id, token string,
	at time.Time,
) (*scannerrelease.CustomBuild, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.CustomBuild
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT * FROM scanner_custom_builds WHERE id = ?`), id); err != nil {
		return nil, err
	}
	if current.LeaseToken != token || current.LeaseExpiresAt == nil ||
		!current.LeaseExpiresAt.After(at) {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	var counts struct {
		Completed int `db:"completed"`
		Failed    int `db:"failed"`
		Queued    int `db:"queued"`
		Running   int `db:"running"`
	}
	if err := tx.GetContext(ctx, &counts, r.db.Rebind(
		`SELECT
		  COALESCE(SUM(CASE WHEN state = ? THEN 1 ELSE 0 END), 0) AS completed,
		  COALESCE(SUM(CASE WHEN state = ? THEN 1 ELSE 0 END), 0) AS failed,
		  COALESCE(SUM(CASE WHEN state = ? THEN 1 ELSE 0 END), 0) AS queued,
		  COALESCE(SUM(CASE WHEN state = ? THEN 1 ELSE 0 END), 0) AS running
		 FROM scanner_custom_build_variants WHERE build_id = ?`),
		scannerrelease.CustomBuildVariantCompleted,
		scannerrelease.CustomBuildVariantFailed,
		scannerrelease.CustomBuildVariantQueued,
		scannerrelease.CustomBuildVariantRunning, id); err != nil {
		return nil, err
	}
	state := scannerrelease.CustomBuildCompleted
	errorClass, errorDetail := "", ""
	if current.CancelRequestedAt != nil {
		state = scannerrelease.CustomBuildCancelled
		if _, err := r.execTx(ctx, tx,
			`UPDATE scanner_custom_build_variants
			 SET state = ?, completed_at = ?, version = version + 1, updated_at = ?
			 WHERE build_id = ? AND state IN (?, ?)`,
			scannerrelease.CustomBuildVariantCancelled, at, at, id,
			scannerrelease.CustomBuildVariantQueued,
			scannerrelease.CustomBuildVariantRunning); err != nil {
			return nil, err
		}
	} else if counts.Failed > 0 && counts.Completed > 0 {
		state = scannerrelease.CustomBuildPartial
		errorClass, errorDetail = "variant_failure", "one or more scanner variants failed"
	} else if counts.Failed > 0 {
		state = scannerrelease.CustomBuildFailed
		errorClass, errorDetail = "variant_failure", "scanner image build failed"
	} else if counts.Queued > 0 || counts.Running > 0 {
		return nil, errors.New("custom build variants are not terminal")
	}
	summary, _ := json.Marshal(map[string]int{
		"completed": counts.Completed, "failed": counts.Failed,
	})
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_custom_builds
		 SET state = ?, worker_id = '', lease_token = '', lease_expires_at = NULL,
		     heartbeat_at = NULL, error_class = ?, error_detail = ?,
		     summary_json = ?, completed_at = ?, version = version + 1,
		     updated_at = ?
		 WHERE id = ? AND lease_token = ? AND state IN (?, ?)`,
		state, errorClass, errorDetail, string(summary), at, at, id, token,
		scannerrelease.CustomBuildClaimed, scannerrelease.CustomBuildRunning)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	if _, err := r.appendEventTx(
		ctx, tx, customBuildAggregate, id, "custom_build."+string(state),
		string(current.State), string(state),
		scannerrelease.TransitionCommand{
			Actor: current.WorkerID, Reason: "custom build execution finished",
			IdempotencyKey: customBuildLeaseEventKey("finalize", token, ""),
			PayloadJSON:    string(summary),
		}, at,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCustomBuildOnly(ctx, id)
}

func (r *scannerReleaseRepository) RequestCustomBuildCancellation(
	ctx context.Context,
	id string,
	expectedVersion int64,
	command scannerrelease.TransitionCommand,
	at time.Time,
) (*scannerrelease.CustomBuild, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.CustomBuild
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT * FROM scanner_custom_builds WHERE id = ?`), id); err != nil {
		return nil, err
	}
	intended := string(current.State)
	if current.State == scannerrelease.CustomBuildQueued {
		intended = string(scannerrelease.CustomBuildCancelled)
	}
	if replayed, err := r.existingCommandTx(
		ctx, tx, customBuildAggregate, id, command.IdempotencyKey, intended,
	); err != nil {
		return nil, err
	} else if replayed {
		return &current, tx.Commit()
	}
	if current.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	switch current.State {
	case scannerrelease.CustomBuildCompleted, scannerrelease.CustomBuildPartial,
		scannerrelease.CustomBuildFailed, scannerrelease.CustomBuildCancelled:
		return nil, scannerrelease.ErrInvalidTransition
	}
	state := current.State
	completedAt := current.CompletedAt
	if current.State == scannerrelease.CustomBuildQueued {
		state = scannerrelease.CustomBuildCancelled
		completedAt = &at
		if _, err := r.execTx(ctx, tx,
			`UPDATE scanner_custom_build_variants
			 SET state = ?, completed_at = ?, version = version + 1, updated_at = ?
			 WHERE build_id = ? AND state = ?`,
			scannerrelease.CustomBuildVariantCancelled, at, at, id,
			scannerrelease.CustomBuildVariantQueued); err != nil {
			return nil, err
		}
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_custom_builds
		 SET state = ?, cancel_requested_at = ?, completed_at = ?,
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		state, at, completedAt, at, id, expectedVersion)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrVersionConflict
	}
	if _, err := r.appendEventTx(
		ctx, tx, customBuildAggregate, id, "custom_build.cancellation_requested",
		string(current.State), string(state), command, at,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCustomBuildOnly(ctx, id)
}

func (r *scannerReleaseRepository) RetryCustomBuild(
	ctx context.Context,
	id string,
	expectedVersion int64,
	command scannerrelease.TransitionCommand,
	at time.Time,
) (*scannerrelease.CustomBuild, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.CustomBuild
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT * FROM scanner_custom_builds WHERE id = ?`), id); err != nil {
		return nil, err
	}
	if replayed, err := r.existingCommandTx(
		ctx, tx, customBuildAggregate, id, command.IdempotencyKey,
		string(scannerrelease.CustomBuildQueued),
	); err != nil {
		return nil, err
	} else if replayed {
		return &current, tx.Commit()
	}
	if current.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	if current.State != scannerrelease.CustomBuildFailed &&
		current.State != scannerrelease.CustomBuildPartial {
		return nil, scannerrelease.ErrInvalidTransition
	}
	if current.Attempt >= current.MaxAttempts {
		return nil, errors.New("custom build retry budget exhausted")
	}
	if _, err := r.execTx(ctx, tx,
		`UPDATE scanner_custom_build_variants
		 SET state = ?, refs_json = '[]', digest = '', loaded_locally = ?,
		     pushed = ?, error_class = '', error_detail = '', started_at = NULL,
		     completed_at = NULL, version = version + 1, updated_at = ?
		 WHERE build_id = ? AND state = ?`,
		scannerrelease.CustomBuildVariantQueued, false, false, at, id,
		scannerrelease.CustomBuildVariantFailed); err != nil {
		return nil, err
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_custom_builds
		 SET state = ?, worker_id = '', lease_token = '', lease_expires_at = NULL,
		     heartbeat_at = NULL, available_at = ?, cancel_requested_at = NULL,
		     error_class = '', error_detail = '', completed_at = NULL,
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		scannerrelease.CustomBuildQueued, at, at, id, expectedVersion)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrVersionConflict
	}
	if _, err := r.appendEventTx(
		ctx, tx, customBuildAggregate, id, "custom_build.retried",
		string(current.State), string(scannerrelease.CustomBuildQueued),
		command, at,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCustomBuildOnly(ctx, id)
}

func (r *scannerReleaseRepository) ReclaimStaleCustomBuilds(
	ctx context.Context,
	now time.Time,
) (int, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var stale []scannerrelease.CustomBuild
	if err := tx.SelectContext(ctx, &stale, r.db.Rebind(
		`SELECT * FROM scanner_custom_builds
		 WHERE state IN (?, ?) AND lease_expires_at IS NOT NULL
		   AND lease_expires_at <= ?
		 ORDER BY lease_expires_at, id`),
		scannerrelease.CustomBuildClaimed,
		scannerrelease.CustomBuildRunning, now); err != nil {
		return 0, err
	}
	reclaimed := 0
	for i := range stale {
		state := scannerrelease.CustomBuildQueued
		eventType := "custom_build.requeued_after_worker_loss"
		errorClass, errorDetail := "", ""
		var completedAt *time.Time
		if stale[i].CancelRequestedAt != nil {
			state = scannerrelease.CustomBuildCancelled
			eventType = "custom_build.cancelled_after_worker_loss"
			completedAt = &now
		} else if stale[i].Attempt >= stale[i].MaxAttempts {
			state = scannerrelease.CustomBuildFailed
			eventType = "custom_build.failed_after_worker_loss"
			errorClass, errorDetail = "worker_lost", "custom build retry budget exhausted after worker loss"
			completedAt = &now
		}
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_custom_builds
			 SET state = ?, worker_id = '', lease_token = '',
			     lease_expires_at = NULL, heartbeat_at = NULL,
			     available_at = ?, error_class = ?, error_detail = ?,
			     completed_at = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ? AND lease_token = ?
			   AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?`,
			state, now, errorClass, errorDetail, completedAt, now,
			stale[i].ID, stale[i].Version, stale[i].LeaseToken, now)
		if err != nil {
			return reclaimed, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		if state == scannerrelease.CustomBuildQueued {
			if _, err := r.execTx(ctx, tx,
				`UPDATE scanner_custom_build_variants
				 SET state = ?, started_at = NULL, completed_at = NULL,
				     error_class = '', error_detail = '', version = version + 1,
				     updated_at = ?
				 WHERE build_id = ? AND state = ?`,
				scannerrelease.CustomBuildVariantQueued, now, stale[i].ID,
				scannerrelease.CustomBuildVariantRunning); err != nil {
				return reclaimed, err
			}
		}
		if _, err := r.appendEventTx(
			ctx, tx, customBuildAggregate, stale[i].ID, eventType,
			string(stale[i].State), string(state),
			scannerrelease.TransitionCommand{
				Actor:  "custom-build-reclaimer",
				Reason: "custom build worker lease expired",
				IdempotencyKey: customBuildLeaseEventKey(
					"reclaim", stale[i].LeaseToken, "",
				),
				PayloadJSON: `{"errorClass":"worker_lost"}`,
			}, now,
		); err != nil {
			return reclaimed, err
		}
		reclaimed++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return reclaimed, nil
}

func boundedCustomBuildError(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) > maximum {
		value = value[:maximum]
	}
	return value
}

func customBuildLeaseEventKey(kind, token, suffix string) string {
	key := kind + ":" + sha256String([]byte(token))
	if suffix != "" {
		key += ":" + suffix
	}
	return key
}
