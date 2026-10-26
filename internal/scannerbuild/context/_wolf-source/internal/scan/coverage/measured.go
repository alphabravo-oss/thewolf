package coverage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

const MaxReportBytes = 20 << 20

// Formats are report contracts, not a language allowlist. Any producer that
// emits one of these formats can publish measured coverage to Wolf.
var Formats = []string{"lcov", "cobertura", "jacoco", "clover", "opencover", "go", "istanbul", "simplecov", "sonar"}

type Metric struct {
	Covered int `json:"covered"`
	Total   int `json:"total"`
}

func (m Metric) Percent() float64 {
	if m.Total == 0 {
		return 0
	}
	return 100 * float64(m.Covered) / float64(m.Total)
}

type MeasuredFile struct {
	Path    string            `json:"path"`
	Metrics map[string]Metric `json:"metrics"`
	Lines   map[int]bool      `json:"lines,omitempty"`
}

type ReportInput struct {
	Format  string `json:"format"`
	Content string `json:"content"`
	// StripPrefix maps a CI workspace or Go module prefix to repository paths.
	StripPrefix string `json:"strip_prefix,omitempty"`
	PathPrefix  string `json:"path_prefix,omitempty"`
}

type ReportSource struct {
	Format      string `json:"format"`
	SHA256      string `json:"sha256"`
	StripPrefix string `json:"strip_prefix,omitempty"`
	PathPrefix  string `json:"path_prefix,omitempty"`
}

type MeasuredReport struct {
	Version       int               `json:"version"`
	CommitSHA     string            `json:"commit_sha"`
	ImportedAt    time.Time         `json:"imported_at"`
	ImportedBy    string            `json:"imported_by"`
	Sources       []ReportSource    `json:"sources"`
	Metrics       map[string]Metric `json:"metrics"`
	MetricFiles   map[string]int    `json:"metric_files"`
	Files         []MeasuredFile    `json:"files,omitempty"`
	ExcludedPaths []string          `json:"excluded_paths"`
	Changed       *ChangedCoverage  `json:"changed,omitempty"`
}

// ParseReports combines disjoint components of a repository. Shards covering
// the same file must first be merged by their coverage producer: aggregate
// branch counts cannot be safely unioned without branch identities.
func ParseReports(inputs []ReportInput) (*MeasuredReport, error) {
	if len(inputs) == 0 || len(inputs) > 64 {
		return nil, fmt.Errorf("provide between 1 and 64 coverage reports")
	}
	r := &MeasuredReport{Version: 1, Metrics: map[string]Metric{}, MetricFiles: map[string]int{}, ExcludedPaths: []string{}}
	seen, size := map[string]bool{}, 0
	for _, in := range inputs {
		size += len(in.Content)
		if size > MaxReportBytes {
			return nil, fmt.Errorf("coverage reports exceed 20 MiB")
		}
		files, err := parseReport(in)
		if err != nil {
			return nil, fmt.Errorf("%s report: %w", in.Format, err)
		}
		hash := sha256.Sum256([]byte(in.Content))
		r.Sources = append(r.Sources, ReportSource{in.Format, hex.EncodeToString(hash[:]), in.StripPrefix, in.PathPrefix})
		for _, f := range files {
			if seen[f.Path] {
				return nil, fmt.Errorf("overlapping reports for %s; merge test shards with the coverage producer before uploading", f.Path)
			}
			seen[f.Path] = true
			if len(seen) > 100000 {
				return nil, fmt.Errorf("coverage exceeds 100000 files")
			}
			for name, metric := range f.Metrics {
				total := r.Metrics[name]
				total.Total += metric.Total
				total.Covered += metric.Covered
				r.Metrics[name] = total
				r.MetricFiles[name]++
			}
			r.Files = append(r.Files, f)
		}
	}
	if len(r.Files) == 0 {
		return nil, fmt.Errorf("report contains no measured source files")
	}
	sort.Slice(r.Files, func(i, j int) bool { return r.Files[i].Path < r.Files[j].Path })
	return r, nil
}

func reportPath(raw string, in ReportInput) (string, error) {
	p := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	prefix := strings.TrimSuffix(strings.ReplaceAll(in.StripPrefix, "\\", "/"), "/")
	if prefix != "" {
		if !strings.HasPrefix(p, prefix+"/") {
			return "", fmt.Errorf("path %q does not start with strip_prefix", p)
		}
		p = strings.TrimPrefix(p, prefix+"/")
	}
	p = strings.TrimPrefix(p, "./")
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", fmt.Errorf("parent traversal in report path")
		}
	}
	if p == "" || p == "." || strings.HasPrefix(p, "/") || strings.ContainsAny(p, ":\x00\r\n") || len(p) > 4096 {
		return "", fmt.Errorf("report paths must be repository-relative; use strip_prefix for CI workspace paths")
	}
	if in.PathPrefix != "" {
		prefix, err := reportPath(in.PathPrefix, ReportInput{})
		if err != nil {
			return "", fmt.Errorf("invalid path_prefix: %w", err)
		}
		p = prefix + "/" + p
	}
	return path.Clean(p), nil
}

type fileBuilder struct {
	MeasuredFile
	points map[string]map[string]Metric
}

type reportBuilder struct {
	input ReportInput
	files map[string]*fileBuilder
	err   error
}

func (b *reportBuilder) file(raw string) *fileBuilder {
	p, err := reportPath(raw, b.input)
	if err != nil {
		b.err = err
	}
	if b.files[p] == nil {
		b.files[p] = &fileBuilder{MeasuredFile: MeasuredFile{Path: p, Metrics: map[string]Metric{}, Lines: map[int]bool{}}, points: map[string]map[string]Metric{}}
	}
	return b.files[p]
}

func (b *reportBuilder) point(f *fileBuilder, kind, key string, covered, total int) {
	if covered < 0 || total < 0 || covered > total || total > 10000000 {
		b.err = fmt.Errorf("invalid %s coverage counts", kind)
		return
	}
	if f.points[kind] == nil {
		f.points[kind] = map[string]Metric{}
	}
	value := Metric{covered, total}
	if old, ok := f.points[kind][key]; ok {
		if old.Total != total || (total > 1 && old.Covered != covered) {
			b.err = fmt.Errorf("conflicting coverage for %s; merge shards before uploading", f.Path)
			return
		}
		value.Covered = max(old.Covered, covered)
	}
	f.points[kind][key] = value
}

func (b *reportBuilder) line(f *fileBuilder, line, hits int) {
	if line < 1 || line > 10000000 || hits < 0 {
		b.err = fmt.Errorf("invalid line number or hit count")
		return
	}
	covered := 0
	if hits > 0 {
		covered = 1
	}
	b.point(f, "lines", fmt.Sprint(line), covered, 1)
	f.Lines[line] = f.Lines[line] || hits > 0
}

func (b *reportBuilder) result() ([]MeasuredFile, error) {
	if b.err != nil {
		return nil, b.err
	}
	var files []MeasuredFile
	for _, f := range b.files {
		measured := false
		for name, points := range f.points {
			m := Metric{}
			for _, point := range points {
				m.Covered += point.Covered
				m.Total += point.Total
			}
			f.Metrics[name] = m
			measured = measured || m.Total > 0
		}
		if measured {
			files = append(files, f.MeasuredFile)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("report contains no measured source files")
	}
	return files, nil
}
