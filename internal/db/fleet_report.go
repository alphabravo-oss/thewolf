package db

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"golang.org/x/sync/errgroup"
)

// SecurityReport is the "Security posture" report (GET /fleet/report). The live
// report and saved Enterprise snapshots render from this one shape, so it holds
// summaries only: no per-finding or per-branch rows.
type SecurityReport struct {
	GeneratedAt    time.Time           `json:"generated_at"`
	Collection     *ReportCollection   `json:"collection,omitempty"` // nil = every repository in scope
	OpenBySeverity map[string]int      `json:"open_by_severity"`
	Trend          []FleetTrendEntry   `json:"trend"` // last 30 daily snapshots
	Changes        FleetChanges        `json:"changes"`
	RepoCount      int                 `json:"repo_count"`
	Coverage       FleetCoverage       `json:"coverage"`
	GatesFailing   int                 `json:"gates_failing"`
	FixesPushed    int                 `json:"fixes_pushed"`
	Aging          []ReportAge         `json:"aging"`
	TopRepos       []FindingsByRepoRow `json:"top_repos"`
}

type ReportCollection struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ReportAge counts open critical/high security findings first observed more
// than Days days ago (cumulative: >90 is also in >30).
type ReportAge struct {
	Days     int `json:"days"`
	Critical int `json:"critical"`
	High     int `json:"high"`
}

// BuildSecurityReport composes the report from the same store aggregates the
// Home dashboard reads. A collection the caller cannot see is sql.ErrNoRows.
func BuildSecurityReport(ctx context.Context, s Store, userID string, fleet bool, collectionID string, now time.Time) (*SecurityReport, error) {
	out := &SecurityReport{GeneratedAt: now.UTC()}
	if collectionID != "" {
		col, err := s.GetCollectionByID(ctx, collectionID)
		if err != nil {
			return nil, err
		}
		if col == nil || (!fleet && col.UserID != userID) {
			return nil, sql.ErrNoRows
		}
		out.Collection = &ReportCollection{ID: col.ID, Name: col.Name}
	}
	// The reads are independent and each is one scoped evidence load: run them
	// together so the report costs its slowest part, not their sum.
	var (
		p      *FleetPostureResult
		trend  []FleetTrendEntry
		byRepo []FindingsByRepoRow
		jobs   []models.FixJob
		group  errgroup.Group
	)
	group.Go(func() (err error) { p, err = s.FleetPosture(ctx, userID, fleet, collectionID); return })
	group.Go(func() (err error) { trend, err = s.FleetFindingTrends(ctx, userID, fleet, collectionID); return })
	group.Go(func() (err error) { byRepo, err = s.FindingsByRepo(ctx, userID, fleet, collectionID); return })
	group.Go(func() (err error) {
		if fleet {
			jobs, err = s.ListFixJobs(ctx, "")
		} else {
			jobs, err = s.ListFixJobsByUser(ctx, userID, "")
		}
		return
	})
	if err := group.Wait(); err != nil {
		return nil, err
	}

	inScope := map[string]bool{}
	for _, row := range p.Coverage.Rows {
		inScope[row.RepoID] = true
	}
	for _, j := range jobs {
		if j.Pushed && j.Status != models.FixJobSuperseded && inScope[j.RepoID] {
			out.FixesPushed++
		}
	}
	if len(trend) > 30 {
		trend = trend[len(trend)-30:]
	}
	out.OpenBySeverity = p.OpenFindingsBySeverity
	out.Trend = trend
	out.Changes, out.Changes.Rows = p.Changes, nil
	out.RepoCount = p.RepoCount
	out.Coverage, out.Coverage.Rows = p.Coverage, nil
	out.GatesFailing = p.GatesFailing
	out.Aging = reportAging(p.Priorities, now)
	out.TopRepos = reportTopRepos(byRepo, 10)
	return out, nil
}

// reportAging buckets open critical/high findings by age. A missing first-seen
// time is not "new": it counts as the oldest (same rule as the Home chart).
func reportAging(rows []FleetPriority, now time.Time) []ReportAge {
	out := []ReportAge{{Days: 30}, {Days: 60}, {Days: 90}}
	for _, f := range rows {
		age := now.Sub(f.FirstSeenAt)
		for i := range out {
			if !f.FirstSeenAt.IsZero() && age <= time.Duration(out[i].Days)*24*time.Hour {
				continue
			}
			switch f.Severity {
			case models.SeverityCritical:
				out[i].Critical++
			case models.SeverityHigh:
				out[i].High++
			}
		}
	}
	return out
}

// reportTopRepos: most critical, then high, then total (Home "Riskiest repositories").
func reportTopRepos(rows []FindingsByRepoRow, limit int) []FindingsByRepoRow {
	out := append([]FindingsByRepoRow{}, rows...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Critical != b.Critical {
			return a.Critical > b.Critical
		}
		if a.High != b.High {
			return a.High > b.High
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return a.Name < b.Name
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
