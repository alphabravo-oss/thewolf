package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/finding/diff"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

func (s *PostgresStore) PersistFindingBaseline(ctx context.Context, scanID, leaseToken string, at time.Time) error {
	return persistFindingBaseline(ctx, s.db, scanID, leaseToken, at)
}

func persistFindingBaseline(ctx context.Context, database *sqlx.DB, scanID, leaseToken string, at time.Time) error {
	if at.IsZero() {
		return errors.New("baseline observation time is required")
	}
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current models.Scan
	if err := tx.GetContext(ctx, &current, tx.Rebind("SELECT * FROM scans WHERE id = ?"), scanID); err != nil {
		return err
	}
	if err := lockVulnerabilityRepo(ctx, tx, current.RepoID); err != nil {
		return err
	}
	expectedRepo, expectedBranch := current.RepoID, current.Branch
	scans, err := lockBaselineScans(ctx, tx, current.RepoID, map[string]time.Time{scanID: at})
	if err != nil {
		return err
	}
	for _, scan := range scans {
		if scan.ID == scanID {
			current = scan
		}
	}
	if current.RepoID != expectedRepo || current.Branch != expectedBranch {
		return ErrStaleFindings
	}
	if current.CancelRequestedAt != nil || current.LeaseToken != leaseToken || (current.Status == models.ScanStatusCompleted && current.FailureCode != "") ||
		(current.Status != models.ScanStatusRunning && !(current.Status == models.ScanStatusCompleted && leaseToken == "")) {
		return ErrStaleScanLease
	}
	// A concurrent completed invocation may already have pinned the observation.
	if current.BaselineResolvedAt != nil {
		at = *current.BaselineResolvedAt
	}
	changed, err := deriveFindingBaseline(ctx, tx, current, scans, at)
	if err != nil {
		return err
	}
	if changed {
		if err := advanceFindingsRevision(ctx, tx, scanID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Callers hold the repository lock, then acquire affected observations and
// their preceding history in one sorted order. This avoids taking an earlier
// history lock after a later scan lock during a multi-scan manual operation.
// ponytail: history discovery scans repository scan metadata and locks relevant
// branch observations; a history index is the upgrade path for large fleets.
func lockBaselineScans(ctx context.Context, tx *sqlx.Tx, repoID string, targets map[string]time.Time) ([]models.Scan, error) {
	var scans []models.Scan
	if err := tx.SelectContext(ctx, &scans, tx.Rebind("SELECT * FROM scans WHERE repo_id = ? ORDER BY id"), repoID); err != nil {
		return nil, err
	}
	cutoffs := map[string]time.Time{}
	found := 0
	for _, s := range scans {
		at, wanted := targets[s.ID]
		if !wanted {
			continue
		}
		found++
		if s.BaselineResolvedAt != nil {
			at = *s.BaselineResolvedAt
		}
		if at.After(cutoffs[s.Branch]) {
			cutoffs[s.Branch] = at
		}
	}
	if found != len(targets) {
		return nil, sql.ErrNoRows
	}
	var locked []models.Scan
	for _, s := range scans {
		_, wanted := targets[s.ID]
		if !wanted && !(s.Status == models.ScanStatusCompleted && s.CompletedAt != nil && s.CompletedAt.Before(cutoffs[s.Branch])) {
			continue
		}
		current, err := lockClusterScan(ctx, tx, repoID, s.ID)
		if err != nil {
			return nil, err
		}
		locked = append(locked, *current)
	}
	return locked, nil
}

// Derivation participates in the caller's transaction; that caller advances
// the revision once alongside its other mutations. Stored history is reusable
// only for the identity it describes, never an unrelated manually joined member.
func deriveFindingBaseline(ctx context.Context, tx *sqlx.Tx, current models.Scan, scans []models.Scan, at time.Time) (bool, error) {
	scanID := current.ID
	prior := map[string]models.Scan{}
	for _, s := range scans {
		if s.ID != scanID && validBaselineScan(s) && s.CompletedAt.Before(at) && s.Branch == current.Branch && baselineSourceCompatible(s, current) {
			prior[s.ID] = s
		}
	}
	var latest *models.Scan
	for _, s := range prior {
		if latest == nil || s.CompletedAt.After(*latest.CompletedAt) || (s.CompletedAt.Equal(*latest.CompletedAt) && s.ID < latest.ID) {
			copy := s
			latest = &copy
		}
	}
	baselineID := current.BaselineScanID
	savedAuto := false
	if current.BaselineResolvedAt == nil {
		// The auto baseline is the saved branch backlog. Named baselines are used
		// through explicit comparisons; merely creating one does not select it.
		err := tx.GetContext(ctx, &baselineID, tx.Rebind("SELECT scan_id FROM scan_baselines WHERE repo_id = ? AND branch = ? AND name = 'auto'"), current.RepoID, current.Branch)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		savedAuto = err == nil
		if errors.Is(err, sql.ErrNoRows) {
			baselineID = ""
			if latest != nil {
				baselineID = latest.ID
			}
		}
	}
	if baselineID != "" && baselineID != scanID {
		if _, ok := prior[baselineID]; !ok {
			if !savedAuto {
				return false, errors.New("saved baseline is not a prior successful compatible branch scan")
			}
			// An automatic baseline saved by older rules (e.g. from a scan with failed
			// tools) must not block publication: use the latest qualifying scan, exactly
			// as when none is saved. Completion replaces the stale record.
			baselineID = ""
			if latest != nil {
				baselineID = latest.ID
			}
		}
	}
	var evidence []models.Finding
	if err := tx.SelectContext(ctx, &evidence, tx.Rebind("SELECT * FROM findings WHERE scan_id = ? ORDER BY id"), scanID); err != nil {
		return false, err
	}
	primaries := map[string]models.Finding{}
	keys := map[string]bool{}
	for _, f := range evidence {
		if f.RepoID != current.RepoID {
			return false, errors.New("baseline evidence crosses repository scope")
		}
		if f.ClusterID == "" || f.ClusterID == f.ID {
			primaries[f.ID] = f
			keys[diff.Key(f)] = true
		}
	}
	type firstObservation struct {
		scanID string
		at     time.Time
	}
	first := map[string]firstObservation{}
	inBaseline, inLatest := map[string]bool{}, map[string]bool{}
	// Stream only identity/history columns; avoid retaining every historical payload.
	rows, err := tx.QueryxContext(ctx, tx.Rebind(`SELECT f.scan_id, f.stable_fingerprint, f.fingerprint,
 f.introduced_in_scan_id, f.first_seen_at, f.baseline_identity_key FROM findings f JOIN scans s ON s.id = f.scan_id
 WHERE s.repo_id = ? AND s.branch = ? AND s.status = ? AND s.completed_at < ?`), current.RepoID, current.Branch, models.ScanStatusCompleted, at)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var f models.Finding
		if err := rows.StructScan(&f); err != nil {
			_ = rows.Close()
			return false, err
		}
		historical, ok := prior[f.ScanID]
		key := diff.Key(f)
		if !ok || !keys[key] {
			continue
		}
		if f.ScanID == baselineID {
			inBaseline[key] = true
		}
		if latest != nil && f.ScanID == latest.ID {
			inLatest[key] = true
		}
		seen := *historical.CompletedAt
		introduced := f.ScanID
		if f.BaselineIdentityKey == key && f.FirstSeenAt != nil && f.FirstSeenAt.Before(seen) {
			seen = *f.FirstSeenAt
			if f.IntroducedInScanID != "" {
				introduced = f.IntroducedInScanID
			}
		}
		existing, ok := first[key]
		if !ok || seen.Before(existing.at) || (seen.Equal(existing.at) && introduced < existing.scanID) {
			first[key] = firstObservation{scanID: introduced, at: seen}
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, err
	}
	changed := current.BaselineResolvedAt == nil
	// Members share the primary's classification without changing native evidence,
	// membership, suppression, status, score, or any foreign reference.
	for _, f := range evidence {
		primaryID := f.ClusterID
		if primaryID == "" {
			primaryID = f.ID
		}
		primary, ok := primaries[primaryID]
		if !ok {
			return false, errors.New("baseline cluster has no persisted primary")
		}
		key := diff.Key(primary)
		state := diff.StateNew
		introduction, observed := first[key]
		if observed && !inLatest[key] {
			state = diff.StateResurfaced
		} else if inBaseline[key] || baselineID == scanID {
			state = diff.StateExisting
		}
		if !observed {
			introduction = firstObservation{scanID: scanID, at: at}
		}
		if primary.BaselineIdentityKey == key && primary.FirstSeenAt != nil && primary.FirstSeenAt.Before(introduction.at) && primary.IntroducedInScanID != "" {
			introduction = firstObservation{scanID: primary.IntroducedInScanID, at: *primary.FirstSeenAt}
		}
		if f.BaselineIdentityKey == key && f.BaselineState == state && f.IntroducedInScanID == introduction.scanID && f.ResolvedInScanID == "" && f.FirstSeenAt != nil && f.FirstSeenAt.Equal(introduction.at) && f.LastSeenAt != nil && f.LastSeenAt.Equal(at) {
			continue
		}
		if _, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE findings SET baseline_identity_key = ?, baseline_state = ?, introduced_in_scan_id = ?, resolved_in_scan_id = '', first_seen_at = ?, last_seen_at = ?, updated_at = ? WHERE id = ?`), key, state, introduction.scanID, introduction.at, at, time.Now().UTC(), f.ID); err != nil {
			return false, err
		}
		changed = true
	}
	if changed {
		if _, err := tx.ExecContext(ctx, tx.Rebind("UPDATE scans SET baseline_scan_id = ?, baseline_resolved_at = ? WHERE id = ?"), baselineID, at, scanID); err != nil {
			return false, err
		}
	}
	return changed, nil
}

func baselineSourceCompatible(a, b models.Scan) bool {
	kind := func(s models.Scan) models.SourceType {
		if s.SourceType == "" {
			return models.SourceTypeLocal
		}
		return s.SourceType
	}
	if kind(a) != kind(b) {
		return false
	}
	if kind(a) != models.SourceTypeSSH {
		return true
	}
	node := func(s models.Scan) string {
		if s.RemoteNodeID == nil {
			return ""
		}
		return *s.RemoteNodeID
	}
	return node(a) == node(b) && a.SourcePath == b.SourcePath
}
