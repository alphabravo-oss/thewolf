// Package coverage provides static test coverage analysis by detecting test
// files in a repository and mapping them to their corresponding source files.
package coverage

// CoverageReport summarizes static test coverage analysis for a repo.
type CoverageReport struct {
	TotalSourceFiles  int                         `json:"total_source_files"`
	FilesWithTests    int                         `json:"files_with_tests"`
	FilesWithoutTests int                         `json:"files_without_tests"`
	TestFiles         int                         `json:"test_files"`
	CoveragePercent   float64                     `json:"coverage_percent"` // files_with_tests / total_source_files * 100
	UncoveredFiles    []string                    `json:"uncovered_files"`  // source files with no matching test
	ByLanguage        map[string]LanguageCoverage `json:"by_language"`
}

// LanguageCoverage holds coverage data for a single language.
type LanguageCoverage struct {
	Language        string   `json:"language"`
	SourceFiles     int      `json:"source_files"`
	TestFiles       int      `json:"test_files"`
	FilesWithTests  int      `json:"files_with_tests"`
	CoveragePercent float64  `json:"coverage_percent"`
	UncoveredFiles  []string `json:"uncovered_files,omitempty"`
}
