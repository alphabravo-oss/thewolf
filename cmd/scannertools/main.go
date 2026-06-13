package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannertools/docs"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/scannertools/validate"
	_ "github.com/alphabravocompany/thewolf/plugins"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	root, err := manifest.FindRepoRoot("")
	if err != nil {
		return err
	}
	switch args[0] {
	case "validate":
		result, err := validate.Run(root)
		if err != nil {
			return err
		}
		fmt.Printf("scanner metadata OK: %d tools (%d default, %d bucket, %d upstream), %d version pins\n",
			result.ToolCount, result.DefaultCount, result.BucketCount, result.UpstreamCount, result.VersionVarCount)
		return nil
	case "docs":
		fs := flag.NewFlagSet("docs", flag.ContinueOnError)
		check := fs.Bool("check", false, "fail if scanners/TOOLS.md is stale")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return renderDocs(root, *check)
	case "upstream-images":
		fs := flag.NewFlagSet("upstream-images", flag.ContinueOnError)
		platforms := fs.String("platforms", "linux/amd64,linux/arm64", "comma-separated platforms to require")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return checkUpstreamImages(root, splitCSV(*platforms))
	case "bump":
		fs := flag.NewFlagSet("bump", flag.ContinueOnError)
		tool := fs.String("tool", "", "scanner tool name")
		version := fs.String("version", "", "new pinned version")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return bumpTool(root, *tool, *version)
	default:
		return usage()
	}
}

func usage() error {
	return fmt.Errorf("usage: scannertools validate | docs [--check] | upstream-images [--platforms linux/amd64,linux/arm64] | bump --tool NAME --version VERSION")
}

func renderDocs(root string, check bool) error {
	m, err := manifest.LoadFile(filepath.Join(root, "scanners", "tools.yaml"))
	if err != nil {
		return err
	}
	path := filepath.Join(root, "scanners", "TOOLS.md")
	generated := docs.Markdown(m)
	if check {
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, generated) {
			return fmt.Errorf("%s is stale; run `make scanners-docs`", path)
		}
		fmt.Println("scanner docs OK")
		return nil
	}
	if err := os.WriteFile(path, generated, 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

func checkUpstreamImages(root string, platforms []string) error {
	m, err := manifest.LoadFile(filepath.Join(root, "scanners", "tools.yaml"))
	if err != nil {
		return err
	}
	for _, name := range m.Names() {
		tool := m.Tools[name]
		if tool.IntegrationTier != manifest.TierUpstream {
			continue
		}
		required := platforms
		if len(tool.Image.Platforms) > 0 {
			required = tool.Image.Platforms
		}
		if err := inspectImage(tool.Image.PinnedReference, required); err != nil {
			return fmt.Errorf("%s (%s): %w", name, tool.Image.PinnedReference, err)
		}
		fmt.Printf("OK %s %s\n", name, tool.Image.PinnedReference)
	}
	return nil
}

func inspectImage(ref string, platforms []string) error {
	out, err := exec.Command("docker", "manifest", "inspect", "--verbose", ref).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("docker manifest inspect failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return err
	}
	available, err := manifestPlatforms(out)
	if err != nil {
		return err
	}
	for _, platform := range platforms {
		if _, ok := available[platform]; !ok {
			return fmt.Errorf("missing platform %s (available: %s)", platform, strings.Join(sortedKeys(available), ", "))
		}
	}
	return nil
}

func manifestPlatforms(data []byte) (map[string]struct{}, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			if platform, ok := x["Platform"].(map[string]any); ok {
				if osName, _ := platform["os"].(string); osName != "" {
					if arch, _ := platform["architecture"].(string); arch != "" {
						out[osName+"/"+arch] = struct{}{}
					}
				}
			}
			if descriptor, ok := x["Descriptor"].(map[string]any); ok {
				if platform, ok := descriptor["platform"].(map[string]any); ok {
					if osName, _ := platform["os"].(string); osName != "" {
						if arch, _ := platform["architecture"].(string); arch != "" {
							out[osName+"/"+arch] = struct{}{}
						}
					}
				}
			}
		}
	}
	walk(raw)
	if len(out) == 0 {
		return nil, fmt.Errorf("docker manifest output did not include platform metadata")
	}
	return out, nil
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
