package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/scannertools/validate"
	"gopkg.in/yaml.v3"
)

func bumpTool(root, toolName, newVersion string) error {
	toolName = strings.TrimSpace(toolName)
	newVersion = strings.TrimSpace(newVersion)
	if toolName == "" || newVersion == "" {
		return fmt.Errorf("bump requires --tool and --version")
	}

	manifestPath := filepath.Join(root, "scanners", "tools.yaml")
	m, err := manifest.LoadFile(manifestPath)
	if err != nil {
		return err
	}
	tool, ok := m.Tools[toolName]
	if !ok {
		return fmt.Errorf("scanner tool %q not found", toolName)
	}
	if tool.PinnedVersion == "" || tool.VersionVariable == "" {
		return fmt.Errorf("scanner tool %q does not have a manifest-managed pinned version", toolName)
	}
	oldVersion := tool.PinnedVersion
	if oldVersion == newVersion {
		return fmt.Errorf("scanner tool %q is already pinned to %s", toolName, newVersion)
	}

	if err := bumpManifestFile(manifestPath, toolName, oldVersion, newVersion, tool); err != nil {
		return err
	}
	if err := bumpVersionsEnv(filepath.Join(root, "scanners", "versions.env"), tool.VersionVariable, newVersion); err != nil {
		return err
	}
	if err := renderDocs(root, false); err != nil {
		return err
	}
	changelogPath, err := writeBumpChangelog(root, toolName, oldVersion, newVersion)
	if err != nil {
		return err
	}
	result, err := validate.Run(root)
	if err != nil {
		return err
	}
	fmt.Printf("bumped %s from %s to %s\n", toolName, oldVersion, newVersion)
	fmt.Printf("wrote %s\n", changelogPath)
	fmt.Printf("scanner metadata OK: %d tools (%d default, %d bucket, %d upstream), %d version pins\n",
		result.ToolCount, result.DefaultCount, result.BucketCount, result.UpstreamCount, result.VersionVarCount)
	return nil
}

func bumpManifestFile(path, toolName, oldVersion, newVersion string, tool manifest.Tool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("decode scanner manifest %s: %w", path, err)
	}
	toolNode := yamlToolNode(&root, toolName)
	if toolNode == nil {
		return fmt.Errorf("scanner tool %q not found in %s", toolName, path)
	}
	setMappingScalar(toolNode, "pinned_version", newVersion)
	if smoke := mappingValue(toolNode, "smoke"); smoke != nil {
		if expected := mappingValue(smoke, "expected_pattern"); expected != nil && expected.Value == oldVersion {
			expected.Value = newVersion
		}
	}
	if image := mappingValue(toolNode, "image"); image != nil {
		if ref := mappingValue(image, "pinned_reference"); ref != nil && ref.Value != "" {
			ref.Value = bumpedImageReference(ref.Value, oldVersion, newVersion, tool)
		}
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		_ = enc.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func yamlToolNode(root *yaml.Node, toolName string) *yaml.Node {
	doc := root
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	}
	tools := mappingValue(doc, "tools")
	if tools == nil {
		return nil
	}
	return mappingValue(tools, toolName)
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func setMappingScalar(node *yaml.Node, key, value string) {
	if existing := mappingValue(node, key); existing != nil {
		existing.Kind = yaml.ScalarNode
		existing.Tag = "!!str"
		existing.Value = value
		existing.Style = yaml.DoubleQuotedStyle
		return
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle},
	)
}

func bumpedImageReference(current, oldVersion, newVersion string, tool manifest.Tool) string {
	tag := renderTagTemplate(tool.Image.TagTemplate, newVersion)
	if tag != "" && tool.Image.Repository != "" {
		return tool.Image.Repository + ":" + tag
	}
	if strings.Contains(current, oldVersion) {
		return strings.ReplaceAll(current, oldVersion, newVersion)
	}
	return current
}

func renderTagTemplate(template, version string) string {
	switch template {
	case "{{ version }}", "{{version}}":
		return version
	case "v{{ version }}", "v{{version}}":
		return "v" + version
	default:
		return ""
	}
}

func bumpVersionsEnv(path, variable, newVersion string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	prefix := variable + "="
	updated := false
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = prefix + newVersion
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("%s does not contain %s", path, variable)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

func writeBumpChangelog(root, toolName, oldVersion, newVersion string) (string, error) {
	dir := filepath.Join(root, "docs", "superpowers", "changelog", "scanner-bumps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	date := time.Now().UTC().Format("2006-01-02")
	filename := fmt.Sprintf("%s-%s-%s-to-%s.md", date, toolName, oldVersion, newVersion)
	path := filepath.Join(dir, filename)
	body := fmt.Sprintf(
		"# Scanner Bump: %s\n\n"+
			"- Tool: %s\n"+
			"- Previous version: %s\n"+
			"- New version: %s\n"+
			"- Generated by: make scanners-bump\n\n"+
			"Validation:\n\n"+
			"- Run `make scanners-validate`.\n"+
			"- Run `make scanners-build && make scanners-smoke` for release-affecting bumps.\n",
		toolName, toolName, oldVersion, newVersion,
	)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
