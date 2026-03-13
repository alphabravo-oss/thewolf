package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// renderMarkdown produces the full Markdown report string.
func renderMarkdown(cfg ReportConfig) (string, error) {
	var b strings.Builder

	sevCounts := countBySeverity(cfg.Findings)
	byTool := findingsByTool(cfg.Findings)

	// Header
	fmt.Fprintf(&b, "# Wolf Scan Report — %s\n\n", cfg.RepoName)
	fmt.Fprintf(&b, "**Scan ID:** %s\n", cfg.ScanID)
	fmt.Fprintf(&b, "**Branch:** %s\n", cfg.Branch)
	fmt.Fprintf(&b, "**Date:** %s\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "**Duration:** %s\n\n", cfg.Duration.String())

	// Executive Summary
	b.WriteString("## Executive Summary\n\n")
	if cfg.AISummary != "" {
		fmt.Fprintf(&b, "%s\n\n", cfg.AISummary)
	} else {
		fmt.Fprintf(&b, "Scan completed with **%d findings** across %d tools.\n\n",
			len(cfg.Findings), len(cfg.ToolsRun))
	}

	// Tool Analysis
	if len(cfg.ToolSummaries) > 0 {
		b.WriteString("## Tool Analysis\n\n")
		for _, ts := range cfg.ToolSummaries {
			fmt.Fprintf(&b, "### %s\n\n", ts.ToolName)
			fmt.Fprintf(&b, "%s\n\n", ts.SummaryText)
			fmt.Fprintf(&b, "- **Findings:** %d\n", ts.FindingCount)
			if ts.SeverityCounts != "" {
				var sevMap map[string]int
				if err := json.Unmarshal([]byte(ts.SeverityCounts), &sevMap); err == nil {
					parts := make([]string, 0, len(sevMap))
					for sev, count := range sevMap {
						parts = append(parts, fmt.Sprintf("%s: %d", sev, count))
					}
					sort.Strings(parts)
					fmt.Fprintf(&b, "- **Severity Breakdown:** %s\n", strings.Join(parts, " | "))
				}
			}
			b.WriteString("\n")
		}
	}

	// Recommendations
	if len(cfg.Recommendations) > 0 {
		b.WriteString("## Recommendations\n\n")
		// Sort by priority
		recs := make([]struct {
			priority int
			idx      int
		}, len(cfg.Recommendations))
		for i, rec := range cfg.Recommendations {
			recs[i] = struct {
				priority int
				idx      int
			}{rec.Priority, i}
		}
		sort.Slice(recs, func(i, j int) bool {
			return recs[i].priority < recs[j].priority
		})
		for _, entry := range recs {
			rec := cfg.Recommendations[entry.idx]
			fmt.Fprintf(&b, "### %d. [%s] %s\n\n", rec.Priority, rec.Category, rec.Title)
			fmt.Fprintf(&b, "**Effort:** %s\n\n", rec.EffortEstimate)
			fmt.Fprintf(&b, "%s\n\n", rec.Description)
		}
	}

	// Overview
	b.WriteString("## Overview\n\n")
	fmt.Fprintf(&b, "- **Total Findings:** %d\n", len(cfg.Findings))
	fmt.Fprintf(&b, "- **Critical:** %d | **High:** %d | **Medium:** %d | **Low:** %d | **Info:** %d\n",
		sevCounts["critical"], sevCounts["high"], sevCounts["medium"], sevCounts["low"], sevCounts["info"])

	// Languages
	if len(cfg.Languages) > 0 {
		langs := make([]string, 0, len(cfg.Languages))
		for lang, count := range cfg.Languages {
			langs = append(langs, fmt.Sprintf("%s (%d)", lang, count))
		}
		sort.Strings(langs)
		fmt.Fprintf(&b, "- **Languages:** %s\n", strings.Join(langs, ", "))
	}

	// Frameworks
	if len(cfg.Frameworks) > 0 {
		sorted := make([]string, len(cfg.Frameworks))
		copy(sorted, cfg.Frameworks)
		sort.Strings(sorted)
		fmt.Fprintf(&b, "- **Frameworks:** %s\n", strings.Join(sorted, ", "))
	}

	// Tools Run
	if len(cfg.ToolsRun) > 0 {
		sorted := make([]string, len(cfg.ToolsRun))
		copy(sorted, cfg.ToolsRun)
		sort.Strings(sorted)
		fmt.Fprintf(&b, "- **Tools Run:** %s\n", strings.Join(sorted, ", "))
	}

	b.WriteString("\n")

	// Findings by Severity
	b.WriteString("## Findings by Severity\n\n")
	for _, sev := range severityOrder {
		filtered := filterBySeverity(cfg.Findings, sev)
		title := strings.Title(string(sev)) //nolint:staticcheck
		fmt.Fprintf(&b, "### %s (%d)\n\n", title, len(filtered))

		if len(filtered) == 0 {
			b.WriteString("No findings.\n\n")
			continue
		}

		b.WriteString("| # | Tool | Title | File | Score |\n")
		b.WriteString("|---|------|-------|------|-------|\n")
		for i, f := range filtered {
			file := f.FilePath
			if f.LineStart > 0 {
				file = fmt.Sprintf("%s:%d", f.FilePath, f.LineStart)
			}
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %.1f |\n",
				i+1, f.ToolName, f.Title, file, f.CompositeScore)
		}
		b.WriteString("\n")
	}

	// Tool Breakdown
	b.WriteString("## Tool Breakdown\n\n")
	b.WriteString("| Tool | Findings | Status |\n")
	b.WriteString("|------|----------|--------|\n")

	toolNames := make([]string, len(cfg.ToolsRun))
	copy(toolNames, cfg.ToolsRun)
	sort.Strings(toolNames)

	for _, name := range toolNames {
		status := "OK"
		if cfg.ToolsFailed != nil {
			if err, failed := cfg.ToolsFailed[name]; failed {
				status = fmt.Sprintf("Failed: %v", err)
			} else {
				_ = err
			}
		}
		count := len(byTool[name])
		fmt.Fprintf(&b, "| %s | %d | %s |\n", name, count, status)
	}

	b.WriteString("\n")

	return b.String(), nil
}
