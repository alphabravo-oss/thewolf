package scannerproposal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerproposalworker"
	"gopkg.in/yaml.v3"
)

var (
	componentNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	sha256HexPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// SelectedUpdateEditor applies the redacted, server-resolved updates carried
// by the proposal worker protocol. It does not resolve database IDs or accept
// versions from command-line arguments.
type SelectedUpdateEditor struct {
	Runner CommandRunner
	GoPath string
}

var _ CheckoutEditor = SelectedUpdateEditor{}

func (e SelectedUpdateEditor) Edit(
	ctx context.Context,
	root string,
	request scannerproposalworker.Request,
) (CheckoutEdit, error) {
	if len(request.Updates) == 0 {
		return CheckoutEdit{}, errors.New("scanner proposal contains no server-resolved updates")
	}
	if request.SourceDateEpoch < 0 {
		return CheckoutEdit{}, errors.New("scanner proposal source date epoch must not be negative")
	}
	if _, err := canonicalJSONObject(request.RiskSummary); err != nil {
		return CheckoutEdit{}, fmt.Errorf("scanner proposal risk summary: %w", err)
	}
	if len(request.RequiredGates) == 0 {
		return CheckoutEdit{}, errors.New("scanner proposal contains no immutable required gates")
	}
	if e.Runner == nil {
		e.Runner = ExecCommandRunner{}
	}
	if e.GoPath == "" {
		e.GoPath = "go"
	}

	updates := append([]scannerproposalworker.SelectedUpdate(nil), request.Updates...)
	sort.Slice(updates, func(i, j int) bool {
		if updates[i].ComponentType == updates[j].ComponentType {
			return updates[i].ComponentName < updates[j].ComponentName
		}
		return updates[i].ComponentType < updates[j].ComponentType
	})
	annotations := make(map[string]ChangeAnnotation, len(updates))
	var evidence []EvidenceLink
	toolBumps := make(map[string]string)
	var baseUpdates []scannerproposalworker.SelectedUpdate
	var goToolchain *scannerproposalworker.SelectedUpdate

	for index := range updates {
		update := updates[index]
		if err := validateEditorUpdate(update); err != nil {
			return CheckoutEdit{}, err
		}
		annotationKey, err := editorChangeKey(update)
		if err != nil {
			return CheckoutEdit{}, err
		}
		sourceURL, err := selectedUpdateEvidenceURL(update.Evidence)
		if err != nil {
			return CheckoutEdit{}, fmt.Errorf("scanner update %s evidence: %w", update.ID, err)
		}
		annotations[annotationKey] = ChangeAnnotation{
			Risk: update.RiskClass, EvidenceURL: sourceURL,
		}
		if sourceURL != "" {
			evidence = append(evidence, EvidenceLink{
				Label: update.ComponentType + ":" + update.ComponentName,
				URL:   sourceURL,
			})
		}
		switch update.ComponentType {
		case ChangeTool:
			toolBumps[update.ComponentName] = update.AvailableValue
		case "upstream_image":
			// The lock refresh resolves the declared upstream reference again;
			// post-generation validation below binds it to AvailableDigest.
		case ChangeBaseImage:
			baseUpdates = append(baseUpdates, update)
		case ChangeToolchain:
			switch update.ComponentName {
			case "go":
				copy := update
				goToolchain = &copy
			case "rust":
				toolBumps["clippy"] = update.AvailableValue
			default:
				return CheckoutEdit{}, fmt.Errorf(
					"toolchain %q requires manual definition editing", update.ComponentName,
				)
			}
		default:
			return CheckoutEdit{}, fmt.Errorf("unsupported scanner update type %q", update.ComponentType)
		}
	}

	toolNames := make([]string, 0, len(toolBumps))
	for name := range toolBumps {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)
	for _, name := range toolNames {
		if err := e.Runner.Run(
			ctx, root, e.GoPath, "run", "./cmd/scannertools", "bump",
			"--tool", name, "--version", toolBumps[name],
			"--source-date-epoch", strconv.FormatInt(request.SourceDateEpoch, 10),
		); err != nil {
			return CheckoutEdit{}, fmt.Errorf("apply scanner tool update %s: %w", name, err)
		}
	}
	if len(baseUpdates) != 0 {
		if err := applyBaseImageUpdates(root, baseUpdates); err != nil {
			return CheckoutEdit{}, err
		}
	}
	if goToolchain != nil {
		if err := applyGoToolchainUpdate(root, *goToolchain); err != nil {
			return CheckoutEdit{}, err
		}
	}

	return CheckoutEdit{
		RiskSummary:        append(json.RawMessage(nil), request.RiskSummary...),
		RequiredGates:      append([]string(nil), request.RequiredGates...),
		ChangeAnnotations:  annotations,
		Evidence:           uniqueEvidenceLinks(evidence),
		ExpectedBranchHead: request.ExpectedHead,
	}, nil
}

func validateEditorUpdate(update scannerproposalworker.SelectedUpdate) error {
	if strings.TrimSpace(update.ID) == "" ||
		!componentNamePattern.MatchString(update.ComponentName) ||
		strings.TrimSpace(update.CurrentValue) == "" ||
		strings.TrimSpace(update.AvailableValue) == "" {
		return fmt.Errorf("scanner update %q has an incomplete identity", update.ID)
	}
	if update.CurrentValue == update.AvailableValue && update.AvailableDigest == "" {
		return fmt.Errorf("scanner update %q does not change a value or digest", update.ID)
	}
	if update.AvailableDigest != "" && !proposalDigestPattern.MatchString(update.AvailableDigest) {
		return fmt.Errorf("scanner update %q has an invalid available digest", update.ID)
	}
	if _, err := canonicalJSONObject(update.Evidence); err != nil {
		return fmt.Errorf("scanner update %q evidence: %w", update.ID, err)
	}
	if _, err := canonicalJSONObject(update.Compatibility); err != nil {
		return fmt.Errorf("scanner update %q compatibility: %w", update.ID, err)
	}
	return nil
}

func editorChangeKey(update scannerproposalworker.SelectedUpdate) (string, error) {
	switch update.ComponentType {
	case ChangeTool, ChangeBaseImage:
		return update.ComponentType + ":" + update.ComponentName, nil
	case "upstream_image":
		return ChangeTool + ":" + update.ComponentName, nil
	case ChangeToolchain:
		if update.ComponentName == "rust" {
			return ChangeTool + ":clippy", nil
		}
		return ChangeToolchain + ":" + update.ComponentName, nil
	default:
		return "", fmt.Errorf("unsupported scanner update type %q", update.ComponentType)
	}
}

func selectedUpdateEvidenceURL(raw json.RawMessage) (string, error) {
	var evidence struct {
		SourceURL string `json:"source_url"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		// Discovery evidence contains additional allowlisted fields. Decode a
		// generic object only after proving it is one bounded object.
		var object map[string]any
		if _, canonicalErr := canonicalJSONObject(raw); canonicalErr != nil {
			return "", canonicalErr
		}
		if unmarshalErr := json.Unmarshal(raw, &object); unmarshalErr != nil {
			return "", unmarshalErr
		}
		value, _ := object["source_url"].(string)
		evidence.SourceURL = value
	}
	if evidence.SourceURL == "" {
		return "", nil
	}
	parsed, err := url.Parse(evidence.SourceURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("source URL must be credential-free HTTPS without query or fragment")
	}
	return evidence.SourceURL, nil
}

func applyBaseImageUpdates(
	root string,
	updates []scannerproposalworker.SelectedUpdate,
) error {
	path := filepath.Join(root, "scanners", "toolchains.yaml")
	if err := rejectSymlink(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode scanner toolchains: %w", err)
	}
	baseImages := yamlMappingValue(yamlDocumentRoot(&document), "base_images")
	if baseImages == nil || baseImages.Kind != yaml.MappingNode {
		return errors.New("scanner toolchains base_images mapping is missing")
	}
	replacements := make(map[string]string)
	for _, update := range updates {
		if !proposalDigestPattern.MatchString(update.CurrentValue) ||
			!proposalDigestPattern.MatchString(update.AvailableDigest) {
			return fmt.Errorf("base image update %q must contain exact current and available digests", update.ID)
		}
		node := yamlMappingValue(baseImages, update.ComponentName)
		if node == nil || node.Kind != yaml.ScalarNode ||
			!strings.Contains(node.Value, "@"+update.CurrentValue) {
			return fmt.Errorf("base image %q is not pinned to the discovered current digest", update.ComponentName)
		}
		newReference := strings.Split(node.Value, "@")[0] + "@" + update.AvailableDigest
		replacements[node.Value] = newReference
		node.Value = newReference
	}
	if err := writeYAMLDocument(path, &document); err != nil {
		return err
	}

	paths, err := filepath.Glob(filepath.Join(root, "scanners", "Dockerfile*"))
	if err != nil {
		return err
	}
	paths = append(paths, filepath.Join(root, "fixer", "Dockerfile.base"))
	seen := make(map[string]bool, len(replacements))
	for _, dockerfile := range paths {
		if err := rejectSymlink(dockerfile); err != nil {
			return err
		}
		value, err := os.ReadFile(dockerfile)
		if err != nil {
			return err
		}
		updated := string(value)
		for before, after := range replacements {
			if strings.Contains(updated, before) {
				seen[before] = true
				updated = strings.ReplaceAll(updated, before, after)
			}
		}
		if updated != string(value) {
			if err := os.WriteFile(dockerfile, []byte(updated), 0o644); err != nil {
				return err
			}
		}
	}
	for before := range replacements {
		if !seen[before] {
			return fmt.Errorf("base image reference %q was not used by any owned Dockerfile", before)
		}
	}
	return nil
}

func applyGoToolchainUpdate(root string, update scannerproposalworker.SelectedUpdate) error {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(update.AvailableValue) {
		return errors.New("Go toolchain update must contain an exact semantic version")
	}
	var evidence struct {
		Attributes map[string]string `json:"attributes"`
	}
	if err := json.Unmarshal(update.Evidence, &evidence); err != nil {
		return fmt.Errorf("decode Go toolchain evidence: %w", err)
	}
	amd64 := evidence.Attributes["linux_amd64_sha256"]
	arm64 := evidence.Attributes["linux_arm64_sha256"]
	if !sha256HexPattern.MatchString(amd64) || !sha256HexPattern.MatchString(arm64) {
		return errors.New("Go toolchain evidence is missing exact linux archive checksums")
	}
	path := filepath.Join(root, "scanners", "toolchains.yaml")
	if err := rejectSymlink(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode scanner toolchains: %w", err)
	}
	toolchains := yamlMappingValue(yamlDocumentRoot(&document), "toolchains")
	goNode := yamlMappingValue(toolchains, "go")
	if goNode == nil {
		return errors.New("scanner Go toolchain definition is missing")
	}
	setYAMLScalar(goNode, "version", update.AvailableValue)
	setYAMLScalar(goNode, "linux_amd64_sha256", amd64)
	setYAMLScalar(goNode, "linux_arm64_sha256", arm64)
	if err := writeYAMLDocument(path, &document); err != nil {
		return err
	}

	script := filepath.Join(root, "scanners", "install", "go-tools.sh")
	if err := rejectSymlink(script); err != nil {
		return err
	}
	value, err := os.ReadFile(script)
	if err != nil {
		return err
	}
	updated := string(value)
	for name, replacement := range map[string]string{
		"GOTC_VERSION":            update.AvailableValue,
		"GOTC_LINUX_AMD64_SHA256": amd64,
		"GOTC_LINUX_ARM64_SHA256": arm64,
	} {
		var replaceErr error
		updated, replaceErr = replaceShellAssignment(updated, name, replacement)
		if replaceErr != nil {
			return replaceErr
		}
	}
	return os.WriteFile(script, []byte(updated), 0o755)
}

func replaceShellAssignment(value, name, replacement string) (string, error) {
	pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `=[^\r\n]+$`)
	if !pattern.MatchString(value) {
		return "", fmt.Errorf("scanner installer does not define %s", name)
	}
	return pattern.ReplaceAllString(value, name+"="+replacement), nil
}

func yamlDocumentRoot(document *yaml.Node) *yaml.Node {
	if document != nil && document.Kind == yaml.DocumentNode && len(document.Content) == 1 {
		return document.Content[0]
	}
	return document
}

func yamlMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func setYAMLScalar(node *yaml.Node, key, value string) {
	if existing := yamlMappingValue(node, key); existing != nil {
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

func writeYAMLDocument(path string, document *yaml.Node) error {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		_ = encoder.Close()
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, output.Bytes(), 0o644)
}

func uniqueEvidenceLinks(values []EvidenceLink) []EvidenceLink {
	byLabel := make(map[string]EvidenceLink, len(values))
	for _, value := range values {
		byLabel[value.Label] = value
	}
	labels := make([]string, 0, len(byLabel))
	for label := range byLabel {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	out := make([]EvidenceLink, 0, len(labels))
	for _, label := range labels {
		out = append(out, byLabel[label])
	}
	return out
}
