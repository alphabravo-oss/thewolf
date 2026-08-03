package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

type backupStorePair struct {
	source Store
	target Store
}

func TestScannerReleaseBackupRestoreContractSQLite(t *testing.T) {
	runScannerReleaseBackupRestoreContract(t, func(t *testing.T) backupStorePair {
		source, err := NewSQLite(t.TempDir() + "/source.db")
		if err != nil {
			t.Fatal(err)
		}
		target, err := NewSQLite(t.TempDir() + "/target.db")
		if err != nil {
			_ = source.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = source.Close()
			_ = target.Close()
		})
		return backupStorePair{source: source, target: target}
	})
}

func TestScannerReleaseBackupRestoreContractPostgres(t *testing.T) {
	runScannerReleaseBackupRestoreContract(t, newPostgresBackupStorePair)
}

func runScannerReleaseBackupRestoreContract(
	t *testing.T,
	factory func(*testing.T) backupStorePair,
) {
	t.Helper()
	pair := factory(t)
	ctx := context.Background()

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":2},"layers":[]}`)
	sum := sha256.Sum256(manifest)
	manifestDigest := "sha256:" + hex.EncodeToString(sum[:])
	registryServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/manifests/"+manifestDigest) {
			writer.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			writer.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = writer.Write(manifest)
			return
		}
		http.NotFound(writer, request)
	}))
	defer registryServer.Close()
	registryHost := strings.TrimPrefix(registryServer.URL, "http://")

	seed := seedReleaseBackupDomain(t, pair.source, registryHost, manifestDigest)
	beforeScan := seedActiveScanForRestore(t, pair.target, seed.releaseID, manifestDigest)

	exportCommand := scannerrelease.BackupCommand{
		Actor:          "backup-automation@example.test",
		Reason:         "contract backup before deterministic DR exercise",
		IdempotencyKey: "export-" + uuid.NewString(),
	}
	backup, err := pair.source.ScannerReleases().ExportReleaseBackup(ctx, exportCommand)
	if err != nil {
		t.Fatalf("ExportReleaseBackup: %v", err)
	}
	if backup.Format != scannerrelease.BackupFormat ||
		backup.Version != scannerrelease.BackupFormatVersion ||
		!backup.Complete || !strings.HasPrefix(backup.PayloadDigest, "sha256:") {
		t.Fatalf("invalid backup envelope: %#v", backup)
	}
	if _, err := pair.source.ScannerReleases().ExportReleaseBackup(ctx, exportCommand); !errors.Is(err, scannerrelease.ErrIdempotencyConflict) {
		t.Fatalf("completed export idempotency error = %v", err)
	}
	encoded, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"scanner_release_maintenance",
		"scanner_release_backup_operations",
		seed.scheduleLeaseToken,
		seed.customBuildLeaseToken,
		seed.secretValue,
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("backup leaked excluded recovery/capability value %q", forbidden)
		}
	}
	if !strings.Contains(string(encoded), seed.secretReference) {
		t.Fatal("backup lost the opaque registry credential reference")
	}
	assertSanitizedBackupLease(t, backup, "scanner_schedule_leases")
	assertSanitizedBackupLease(t, backup, "scanner_rollout_claims")
	assertSanitizedBackupLease(t, backup, "scanner_custom_builds")
	assertBackupCellKind(t, backup, "scanner_update_policies", "enabled", "bool")

	corrupt := cloneReleaseBackup(t, backup)
	corrupt.Tables[0].Rows[0][0].Value += "-tampered"
	if _, err := pair.target.ScannerReleases().PreflightReleaseRestore(ctx, corrupt); err == nil {
		t.Fatal("corrupt backup passed preflight")
	}
	partial := cloneReleaseBackup(t, backup)
	partial.Complete = false
	if _, err := pair.target.ScannerReleases().PreflightReleaseRestore(ctx, partial); err == nil {
		t.Fatal("partial backup passed preflight")
	}
	unsupported := cloneReleaseBackup(t, backup)
	unsupported.Version++
	if _, err := pair.target.ScannerReleases().PreflightReleaseRestore(ctx, unsupported); err == nil {
		t.Fatal("unsupported backup version passed preflight")
	}
	capability := cloneReleaseBackup(t, backup)
	for tableIndex := range capability.Tables {
		if capability.Tables[tableIndex].Name != "scanner_schedule_leases" {
			continue
		}
		for columnIndex, column := range capability.Tables[tableIndex].Columns {
			if column == "lease_token" {
				capability.Tables[tableIndex].Rows[0][columnIndex] = scannerrelease.BackupCell{
					Kind: "string", Value: "forged-lease-capability",
				}
			}
		}
		capability.Tables[tableIndex].Digest = backupTableDigest(
			capability.Tables[tableIndex],
		)
	}
	capability.PayloadDigest = backupPayloadDigest(capability)
	if _, err := pair.target.ScannerReleases().PreflightReleaseRestore(ctx, capability); err == nil ||
		!strings.Contains(err.Error(), "lease capability") {
		t.Fatalf("crafted lease capability error = %v", err)
	}

	preflight, err := pair.target.ScannerReleases().PreflightReleaseRestore(ctx, backup)
	if err != nil {
		t.Fatalf("PreflightReleaseRestore: %v", err)
	}
	if !preflight.Valid || !preflight.TargetEmpty || !preflight.Restorable ||
		preflight.PayloadDigest != backup.PayloadDigest {
		t.Fatalf("preflight = %#v", preflight)
	}
	restoreCommand := scannerrelease.BackupCommand{
		Actor:                   "dr-operator@example.test",
		Reason:                  "deterministic contract recovery exercise",
		IdempotencyKey:          "restore-" + uuid.NewString(),
		MaintenanceConfirmation: scannerrelease.RestoreConfirmation,
	}
	result, err := pair.target.ScannerReleases().RestoreReleaseBackup(
		ctx, backup, restoreCommand,
	)
	if err != nil {
		t.Fatalf("RestoreReleaseBackup: %v", err)
	}
	if result.State != "completed" || result.PayloadDigest != backup.PayloadDigest ||
		result.Idempotent {
		t.Fatalf("restore result = %#v", result)
	}

	inventory, err := pair.target.ScannerReleases().GetReleaseInventory(ctx, seed.releaseID)
	if err != nil {
		t.Fatalf("GetReleaseInventory after restore: %v", err)
	}
	if inventory.Release.ManifestDigest != manifestDigest ||
		len(inventory.Images) != 1 ||
		inventory.Images[0].Digest != manifestDigest {
		t.Fatalf("restored immutable inventory = %#v", inventory)
	}
	correlation, err := pair.target.ScannerReleases().GetOperationCorrelation(
		ctx, "rollout", seed.rolloutID,
	)
	if err != nil ||
		correlation.TraceID != "0123456789abcdef0123456789abcdef" ||
		correlation.OperationID != "backup-dr-operation" ||
		correlation.ParentOperationID != "backup-dr-parent" {
		t.Fatalf("restored operation correlation = %#v err=%v", correlation, err)
	}
	reference := scannerregistry.Reference{
		Registry: registryHost, Repository: "wolf/scanners", Digest: manifestDigest,
	}
	client := scannerregistry.Client{
		HTTP: registryServer.Client(),
		Endpoints: map[string]scannerregistry.Endpoint{
			registryHost: {BaseURL: registryServer.URL},
		},
	}
	fetched, err := client.FetchManifest(ctx, reference)
	if err != nil || fetched.Digest != inventory.Images[0].Digest {
		t.Fatalf("restored immutable OCI identity did not reconcile: manifest=%#v err=%v", fetched, err)
	}
	claim, err := pair.target.ScannerReleases().ClaimNextRollout(
		ctx, "restored-rollout-worker", time.Now().UTC(),
		time.Now().UTC().Add(time.Minute),
	)
	if err != nil || claim == nil || claim.RolloutID != seed.rolloutID || !claim.Reclaimed {
		t.Fatalf("restored rollout lease was not immediately reclaimable: claim=%#v err=%v", claim, err)
	}
	reclaimedCustomBuilds, err := pair.target.ScannerReleases().
		ReclaimStaleCustomBuilds(ctx, time.Now().UTC())
	if err != nil || reclaimedCustomBuilds != 1 {
		t.Fatalf("restored custom-build reclaim = %d err=%v", reclaimedCustomBuilds, err)
	}
	customBuildClaim, err := pair.target.ScannerReleases().ClaimNextCustomBuild(
		ctx, "restored-custom-build-worker", time.Now().UTC(),
		time.Now().UTC().Add(time.Minute),
	)
	if err != nil || customBuildClaim == nil ||
		customBuildClaim.ID != seed.customBuildID ||
		customBuildClaim.LeaseToken == "" {
		t.Fatalf("restored custom build was not claimable: claim=%#v err=%v", customBuildClaim, err)
	}
	customBuildLogs, err := pair.target.ScannerReleases().ListCustomBuildLogs(
		ctx, seed.customBuildID, 0, 10,
	)
	if err != nil || len(customBuildLogs) != 1 ||
		customBuildLogs[0].Line != "backup-safe-custom-build-log" {
		t.Fatalf("restored custom-build logs = %#v err=%v", customBuildLogs, err)
	}

	afterScan, err := pair.target.GetScanByID(ctx, beforeScan.ID)
	if err != nil {
		t.Fatalf("GetScanByID after restore: %v", err)
	}
	if afterScan.Status != beforeScan.Status ||
		afterScan.Phase != beforeScan.Phase ||
		afterScan.LeaseToken != beforeScan.LeaseToken ||
		afterScan.ScannerReleaseID != beforeScan.ScannerReleaseID ||
		afterScan.UpdatedAt.UTC() != beforeScan.UpdatedAt.UTC() {
		t.Fatalf("restore/reconcile mutated active scan:\nbefore=%#v\nafter=%#v", beforeScan, afterScan)
	}

	replayed, err := pair.target.ScannerReleases().RestoreReleaseBackup(
		ctx, backup, restoreCommand,
	)
	if err != nil || !replayed.Idempotent || replayed.OperationID != result.OperationID {
		t.Fatalf("restore replay = %#v err=%v", replayed, err)
	}
	maintenance, err := pair.target.ScannerReleases().GetReleaseMaintenanceStatus(ctx)
	if err != nil || maintenance.RestoreActive(time.Now()) || maintenance.Mode != "normal" {
		t.Fatalf("maintenance after restore = %#v err=%v", maintenance, err)
	}
	operations, err := pair.target.ScannerReleases().ListBackupOperations(ctx, 10)
	if err != nil || len(operations) != 1 || operations[0].State != "completed" {
		t.Fatalf("restore audit operations = %#v err=%v", operations, err)
	}

	secondRestore := restoreCommand
	secondRestore.IdempotencyKey = "restore-nonempty-" + uuid.NewString()
	if _, err := pair.target.ScannerReleases().RestoreReleaseBackup(
		ctx, backup, secondRestore,
	); err == nil || !strings.Contains(err.Error(), "target_release_domain_not_empty") {
		t.Fatalf("non-empty restore error = %v", err)
	}
	operations, err = pair.target.ScannerReleases().ListBackupOperations(ctx, 10)
	if err != nil || len(operations) != 2 || operations[0].State != "failed" {
		t.Fatalf("failed preflight audit operations = %#v err=%v", operations, err)
	}
}

type backupSeed struct {
	releaseID             string
	rolloutID             string
	scheduleLeaseToken    string
	secretReference       string
	secretValue           string
	customBuildID         string
	customBuildLeaseToken string
}

func seedReleaseBackupDomain(
	t *testing.T,
	store Store,
	registryHost, manifestDigest string,
) backupSeed {
	t.Helper()
	ctx := scannertrace.With(context.Background(), scannertrace.Correlation{
		TraceID:           "0123456789abcdef0123456789abcdef",
		OperationID:       "backup-dr-operation",
		ParentOperationID: "backup-dr-parent",
		Component:         "backup-contract",
	})
	repository := store.ScannerReleases()
	secretOwner := &models.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@example.test",
		PasswordHash: "not-used",
	}
	if err := store.CreateUser(ctx, secretOwner); err != nil {
		t.Fatal(err)
	}
	secretReference := uuid.NewString()
	secretValue := "encrypted-registry-secret-material-" + uuid.NewString()
	if err := store.CreateSecret(ctx, &models.Secret{
		ID: secretReference, UserID: secretOwner.ID, KeyType: models.KeyTypeDockerHubToken,
		KeyName: "registry-user", EncryptedValue: secretValue,
	}); err != nil {
		t.Fatal(err)
	}
	policy := newPolicy("organization:"+uuid.NewString(), 1)
	if err := repository.CreatePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	registry := newRegistry()
	registry.Host = registryHost
	registry.SecretReference = secretReference
	if err := repository.CreateRegistryTarget(ctx, registry); err != nil {
		t.Fatal(err)
	}
	candidate := newCandidate(policy)
	if err := repository.CreateCandidate(ctx, candidate, command("backup-candidate:"+candidate.ID)); err != nil {
		t.Fatal(err)
	}
	releaseID := uuid.NewString()
	inventory := &scannerrelease.ReleaseInventory{
		Release: scannerrelease.Release{
			ID: releaseID, Name: "scanner-set-" + releaseID,
			CandidateID: candidate.ID, LockDigest: "sha256:" + strings.Repeat("b", 64),
			ManifestDigest: manifestDigest,
			ManifestURI:    "oci://" + registryHost + "/wolf/scanners@" + manifestDigest,
			State:          scannerrelease.ReleasePublished, SignerIdentity: "release-signer",
			PolicyID: policy.ID, PolicyRevision: policy.Revision,
			DefinitionCommit: strings.Repeat("c", 40),
			Protected:        true, RollbackEligible: true,
		},
		Images: []scannerrelease.ReleaseImage{{
			ImageKey: "default", RegistryTargetID: registry.ID,
			Repository: "wolf/scanners", Digest: manifestDigest,
			PlatformDigests: `{"linux/amd64":"` + manifestDigest + `"}`,
			SignatureStatus: "verified",
		}},
	}
	if err := repository.CreateRelease(ctx, inventory, command("backup-release:"+releaseID)); err != nil {
		t.Fatal(err)
	}
	rollout := &scannerrelease.Rollout{
		ID: uuid.NewString(), Target: "workers:all", ToReleaseID: releaseID,
		Strategy: "canary_then_stable", PolicySnapshotJSON: `{"automaticRollback":true}`,
		Actor: "operator@example.test",
	}
	if err := repository.CreateRollout(
		ctx, rollout,
		[]scannerrelease.RolloutCohort{{Name: "canary", Ordinal: 0}},
		command("backup-rollout:"+rollout.ID),
	); err != nil {
		t.Fatal(err)
	}
	claim, err := repository.ClaimNextRollout(
		ctx, "source-rollout-worker", time.Now().UTC(),
		time.Now().UTC().Add(time.Hour),
	)
	if err != nil || claim == nil {
		t.Fatalf("ClaimNextRollout: claim=%#v err=%v", claim, err)
	}
	lease, acquired, err := repository.AcquireScheduleLease(
		ctx, "weekly-scanner-release", "2026-W31", "source-scheduler",
		time.Now().UTC(), time.Now().UTC().Add(time.Hour),
	)
	if err != nil || !acquired {
		t.Fatalf("AcquireScheduleLease: lease=%#v acquired=%v err=%v", lease, acquired, err)
	}
	customBuild, _, err := repository.CreateCustomBuild(
		ctx,
		scannerrelease.CustomBuildCreateRequest{
			ID: uuid.NewString(), UserID: secretOwner.ID,
			Variants: []string{"default"}, Push: true,
			Platforms: []string{"linux/amd64"}, Namespace: "backup",
			SecretReference: secretReference,
			Actor:           "backup-operator@example.test",
			Reason:          "exercise custom-build backup recovery",
			IdempotencyKey:  "backup-custom-build-" + uuid.NewString(),
			MaxAttempts:     3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	customBuildClaim, err := repository.ClaimNextCustomBuild(
		ctx, "source-custom-build-worker", time.Now().UTC().Add(time.Second),
		time.Now().UTC().Add(time.Hour),
	)
	if err != nil || customBuildClaim == nil ||
		customBuildClaim.ID != customBuild.Build.ID {
		t.Fatalf("ClaimNextCustomBuild: claim=%#v err=%v", customBuildClaim, err)
	}
	if _, err := repository.StartCustomBuild(
		ctx, customBuildClaim.ID, customBuildClaim.LeaseToken,
		time.Now().UTC().Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartCustomBuildVariant(
		ctx, customBuildClaim.ID, "default", customBuildClaim.LeaseToken,
		time.Now().UTC().Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AppendCustomBuildLog(
		ctx, customBuildClaim.ID, "default", customBuildClaim.LeaseToken,
		"backup-safe-custom-build-log", false,
		time.Now().UTC().Add(4*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	return backupSeed{
		releaseID: releaseID, rolloutID: rollout.ID, scheduleLeaseToken: lease.Token,
		secretReference: secretReference, secretValue: secretValue,
		customBuildID:         customBuild.Build.ID,
		customBuildLeaseToken: customBuildClaim.LeaseToken,
	}
}

func seedActiveScanForRestore(
	t *testing.T,
	store Store,
	releaseID, manifestDigest string,
) *models.Scan {
	t.Helper()
	ctx := context.Background()
	user := &models.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@example.test",
		PasswordHash: "not-used",
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	repository := &models.Repo{
		ID: uuid.NewString(), UserID: user.ID, Name: "active-scan-repository",
		SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main",
	}
	if err := store.CreateRepo(ctx, repository); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	expires := now.Add(time.Minute)
	scan := &models.Scan{
		ID: uuid.NewString(), UserID: user.ID, RepoID: repository.ID,
		Branch: "main", Status: models.ScanStatusRunning, Phase: "scanning",
		ClaimedBy: "scan-worker", LeaseToken: "active-scan-lease",
		LeaseExpiresAt: &expires, ScannerReleaseID: releaseID,
		ReleaseManifestDigest: manifestDigest, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetScanByID(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	return persisted
}

func assertSanitizedBackupLease(
	t *testing.T,
	backup *scannerrelease.ReleaseBackup,
	tableName string,
) {
	t.Helper()
	for _, table := range backup.Tables {
		if table.Name != tableName {
			continue
		}
		if len(table.Rows) != 1 {
			t.Fatalf("%s rows = %d", tableName, len(table.Rows))
		}
		values := make(map[string]scannerrelease.BackupCell, len(table.Columns))
		for index, column := range table.Columns {
			values[column] = table.Rows[0][index]
		}
		if values["lease_token"].Value != "" ||
			values["lease_expires_at"].Value != time.Unix(0, 0).UTC().Format(time.RFC3339Nano) {
			t.Fatalf("%s lease was not safely sanitized: %#v", tableName, values)
		}
		if tableName == "scanner_custom_builds" {
			if worker, exists := values["worker_id"]; exists && worker.Value != "" {
				t.Fatalf("%s worker identity was not sanitized: %#v", tableName, values)
			}
			if heartbeat, exists := values["heartbeat_at"]; exists &&
				heartbeat.Kind != "null" {
				t.Fatalf("%s heartbeat was not sanitized: %#v", tableName, values)
			}
		}
		return
	}
	t.Fatalf("backup omitted table %s", tableName)
}

func assertBackupCellKind(
	t *testing.T,
	backup *scannerrelease.ReleaseBackup,
	tableName, columnName, kind string,
) {
	t.Helper()
	for _, table := range backup.Tables {
		if table.Name != tableName {
			continue
		}
		for columnIndex, column := range table.Columns {
			if column == columnName {
				if len(table.Rows) == 0 || table.Rows[0][columnIndex].Kind != kind {
					t.Fatalf(
						"%s.%s kind = %#v, want %s",
						tableName, columnName, table.Rows, kind,
					)
				}
				return
			}
		}
		t.Fatalf("backup omitted column %s.%s", tableName, columnName)
	}
	t.Fatalf("backup omitted table %s", tableName)
}

func cloneReleaseBackup(
	t *testing.T,
	backup *scannerrelease.ReleaseBackup,
) *scannerrelease.ReleaseBackup {
	t.Helper()
	encoded, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	var clone scannerrelease.ReleaseBackup
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}

func newPostgresBackupStorePair(t *testing.T) backupStorePair {
	t.Helper()
	dsn := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WOLF_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sourceSchema := "backup_source_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	targetSchema := "backup_target_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	var source, target *PostgresStore
	t.Cleanup(func() {
		if source != nil {
			_ = source.Close()
		}
		if target != nil {
			_ = target.Close()
		}
		_, _ = admin.db.Exec(`DROP SCHEMA IF EXISTS "` + sourceSchema + `" CASCADE`)
		_, _ = admin.db.Exec(`DROP SCHEMA IF EXISTS "` + targetSchema + `" CASCADE`)
		_ = admin.Close()
	})
	for _, schema := range []string{sourceSchema, targetSchema} {
		if _, err := admin.db.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
			t.Fatal(err)
		}
	}
	source, err = NewPostgres(postgresDSNWithSearchPath(t, dsn, sourceSchema))
	if err != nil {
		t.Fatal(err)
	}
	target, err = NewPostgres(postgresDSNWithSearchPath(t, dsn, targetSchema))
	if err != nil {
		t.Fatal(err)
	}
	return backupStorePair{source: source, target: target}
}

func postgresDSNWithSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return strings.TrimSpace(dsn) + " search_path=" + schema
}
