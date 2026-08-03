package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerreproducibility"
)

func runReproducibility(root string, args []string) error {
	fs := flag.NewFlagSet("reproducibility", flag.ContinueOnError)
	managedPath := fs.String("managed", "", "managed factory reproducibility evidence JSON")
	customerPath := fs.String("customer", "", "customer factory reproducibility evidence JSON")
	outputPath := fs.String("output", "", "optional report output path; stdout when omitted")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("reproducibility does not accept positional arguments")
	}
	if strings.TrimSpace(*managedPath) == "" || strings.TrimSpace(*customerPath) == "" {
		return fmt.Errorf("reproducibility requires --managed and --customer evidence files")
	}
	managed, err := scannerreproducibility.LoadFile(resolveReproducibilityPath(root, *managedPath))
	if err != nil {
		return fmt.Errorf("load managed factory evidence: %w", err)
	}
	customer, err := scannerreproducibility.LoadFile(resolveReproducibilityPath(root, *customerPath))
	if err != nil {
		return fmt.Errorf("load customer factory evidence: %w", err)
	}
	report, err := scannerreproducibility.Compare(managed, customer)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if strings.TrimSpace(*outputPath) == "" {
		if _, err := os.Stdout.Write(encoded); err != nil {
			return err
		}
	} else {
		path := resolveReproducibilityPath(root, *outputPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(path, encoded, 0o640); err != nil {
			return err
		}
	}
	if !report.Equivalent {
		return fmt.Errorf("managed and customer factories differ in reproducible properties: %s", strings.Join(report.Mismatches, ", "))
	}
	return nil
}

func resolveReproducibilityPath(root, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(root, filepath.FromSlash(value))
}
