package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/alphabravocompany/thewolf/internal/scan/coverage"
	"github.com/spf13/cobra"
)

func importCoverageCommand() *cobra.Command {
	var commit, format, strip, prefix, requestFile, diffFile, baseCommit string
	var files, exclusions []string
	cmd := &cobra.Command{Use: "import-coverage <id>", Short: "Replace measured coverage for the exact scanned commit", Args: cobra.ExactArgs(1), Annotations: apiAnno("PUT", "/scans/{}/test-coverage"), RunE: func(cmd *cobra.Command, args []string) error {
		var body any
		if requestFile != "" {
			if len(files) > 0 || commit != "" || format != "" || strip != "" || prefix != "" || len(exclusions) > 0 || diffFile != "" || baseCommit != "" {
				return fmt.Errorf("use --request-file alone or --commit/--format/--file flags")
			}
			data, err := readCoverageFile(requestFile, 40<<20)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, &body); err != nil {
				return fmt.Errorf("invalid coverage request JSON: %w", err)
			}
		} else {
			if commit == "" || format == "" || len(files) == 0 {
				return fmt.Errorf("--commit, --format and at least one --file are required")
			}
			reports := []coverage.ReportInput{}
			size := 0
			for _, file := range files {
				data, err := readCoverageFile(file, coverage.MaxReportBytes-size)
				if err != nil {
					return err
				}
				size += len(data)
				reports = append(reports, coverage.ReportInput{Format: format, Content: string(data), StripPrefix: strip, PathPrefix: prefix})
			}
			if _, err := coverage.ParseReports(reports); err != nil {
				return err
			}
			request := map[string]any{"commit_sha": commit, "reports": reports, "excluded_paths": exclusions}
			if diffFile != "" || baseCommit != "" {
				if diffFile == "" || baseCommit == "" {
					return fmt.Errorf("--diff-cover-file and --base-commit must be provided together")
				}
				data, err := readCoverageFile(diffFile, coverage.MaxReportBytes)
				if err != nil {
					return err
				}
				if !json.Valid(data) {
					return fmt.Errorf("invalid diff-cover JSON")
				}
				request["diff_cover"], request["base_commit_sha"] = json.RawMessage(data), baseCommit
			}
			body = request
		}
		return runRender(cmd, "PUT", "/scans/"+url.PathEscape(args[0])+"/test-coverage", body)
	}}
	cmd.Flags().StringVar(&commit, "commit", "", "full SHA of the commit tested by CI (must match the scan)")
	cmd.Flags().StringVar(&format, "format", "", "lcov, cobertura, jacoco, clover, opencover, go, istanbul, simplecov or sonar")
	cmd.Flags().StringArrayVar(&files, "file", nil, "coverage report path; repeat for disjoint components")
	cmd.Flags().StringVar(&strip, "strip-prefix", "", "CI workspace or Go module prefix to remove from source paths")
	cmd.Flags().StringVar(&prefix, "path-prefix", "", "repository-relative component/source root to add")
	cmd.Flags().StringArrayVar(&exclusions, "excluded-path", nil, "exclusion used by the test producer; repeat to document reported scope")
	cmd.Flags().StringVar(&requestFile, "request-file", "", "complete JSON request for mixed formats and component-specific path mappings")
	cmd.Flags().StringVar(&diffFile, "diff-cover-file", "", "optional diff-cover JSON with changed-line coverage")
	cmd.Flags().StringVar(&baseCommit, "base-commit", "", "full base commit SHA used by diff-cover")
	return cmd
}

func readCoverageFile(name string, limit int) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if limit < 0 {
		return nil, fmt.Errorf("coverage upload exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("coverage upload exceeds size limit")
	}
	return data, nil
}
