package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const wolfYAML = `profile: pr
exclude:
  - vendor/**
  - node_modules/**
  - dist/**
`

const wolfWorkflow = `name: wolf
on: [push, pull_request]
jobs:
  gate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      # Call your Wolf server:
      #   POST /api/v1/scans  {"profile":"fast", ...}
      #   wolf scan gate --fail-exit-code
`

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write .wolf.yaml and a GitHub Actions workflow in the current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			return writeWolfInit(dir, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	return cmd
}

func writeWolfInit(dir string, force bool) error {
	files := []struct {
		rel, content string
		mode         os.FileMode
	}{
		{".wolf.yaml", wolfYAML, 0o644},
		{filepath.Join(".github", "workflows", "wolf.yml"), wolfWorkflow, 0o644},
	}
	if !force {
		for _, f := range files {
			path := filepath.Join(dir, f.rel)
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists (use --force)", f.rel)
			}
		}
	}
	for _, f := range files {
		path := filepath.Join(dir, f.rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(f.content), f.mode); err != nil {
			return err
		}
	}
	return nil
}
