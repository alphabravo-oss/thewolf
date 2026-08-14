package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/alphabravocompany/thewolf/internal/fix/profile"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripANSI(s string) string {
	return strings.TrimSpace(ansiRe.ReplaceAllString(s, ""))
}

// listOpenCodeModels asks the logged-in OpenCode CLI what it can run.
func listOpenCodeModels(ctx context.Context) []profile.Model {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "opencode", "models", "--verbose") // #nosec G204
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		cmd = exec.CommandContext(probeCtx, "opencode", "models") // #nosec G204
		cmd.Stdin = nil
		out, err = cmd.CombinedOutput()
		if err != nil {
			return nil
		}
		return parseOpenCodeModelIDs(out)
	}
	models := parseOpenCodeModelsVerbose(out)
	if len(models) == 0 {
		return parseOpenCodeModelIDs(out)
	}
	return models
}

func parseOpenCodeModelIDs(out []byte) []profile.Model {
	var models []profile.Model
	for _, line := range strings.Split(string(out), "\n") {
		id := strings.TrimSpace(stripANSI(line))
		if !looksLikeModelID(id) {
			continue
		}
		models = append(models, modelFromID(id, "", 0, nil))
	}
	markPreferredOpenCodeDefault(models)
	return models
}

func parseOpenCodeModelsVerbose(out []byte) []profile.Model {
	var models []profile.Model
	lines := strings.Split(string(out), "\n")
	for i := 0; i < len(lines); i++ {
		id := strings.TrimSpace(stripANSI(lines[i]))
		if !looksLikeModelID(id) {
			continue
		}
		// Verbose dumps a JSON object on the following lines.
		var buf strings.Builder
		depth := 0
		started := false
		for j := i + 1; j < len(lines); j++ {
			line := lines[j]
			if !started {
				if strings.TrimSpace(line) != "{" {
					if looksLikeModelID(strings.TrimSpace(stripANSI(line))) {
						break
					}
					continue
				}
				started = true
			}
			buf.WriteString(line)
			buf.WriteByte('\n')
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if started && depth <= 0 {
				i = j
				break
			}
		}
		name, contextK, variants := decodeOpenCodeModelMeta(buf.String())
		models = append(models, modelFromID(id, name, contextK, variants))
	}
	markPreferredOpenCodeDefault(models)
	return models
}

func decodeOpenCodeModelMeta(raw string) (name string, contextK int, variants []string) {
	var meta struct {
		Name  string `json:"name"`
		Limit struct {
			Context int `json:"context"`
		} `json:"limit"`
		Variants map[string]json.RawMessage `json:"variants"`
	}
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return "", 0, nil
	}
	name = strings.TrimSpace(meta.Name)
	if meta.Limit.Context >= 1000 {
		contextK = meta.Limit.Context / 1000
	}
	order := []string{"none", "low", "medium", "high", "xhigh", "max"}
	seen := map[string]bool{}
	for _, id := range order {
		if _, ok := meta.Variants[id]; ok {
			variants = append(variants, id)
			seen[id] = true
		}
	}
	for id := range meta.Variants {
		if !seen[id] {
			variants = append(variants, id)
		}
	}
	return name, contextK, variants
}

func looksLikeModelID(id string) bool {
	if id == "" || strings.ContainsAny(id, " \t") {
		return false
	}
	prov, rest, ok := strings.Cut(id, "/")
	if !ok || prov == "" || rest == "" {
		return false
	}
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '/' || r == '.' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func modelFromID(id, name string, contextK int, variants []string) profile.Model {
	provider, _, _ := strings.Cut(id, "/")
	label := name
	if label == "" {
		label = humanModelLabel(id)
	}
	speed := ""
	if strings.HasSuffix(id, "-fast") || strings.Contains(id, "-mini") {
		speed = "fast"
	}
	m := profile.Model{
		ID:       id,
		Label:    label,
		ContextK: contextK,
		Plan:     "live",
		Speed:    speed,
		Provider: provider,
	}
	if len(variants) > 0 {
		m.Efforts = effortsFromVariants(variants)
	}
	return m
}

func effortsFromVariants(variants []string) []profile.Effort {
	labels := map[string]string{
		"none": "None", "low": "Low", "medium": "Medium",
		"high": "High", "xhigh": "Extra high", "max": "Max",
	}
	out := make([]profile.Effort, 0, len(variants))
	for _, id := range variants {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		label := labels[id]
		if label == "" {
			label = strings.ToUpper(id[:1]) + id[1:]
		}
		out = append(out, profile.Effort{
			ID:    id,
			Label: label,
			Hint:  "OpenCode --variant " + id,
		})
	}
	return out
}

func humanModelLabel(id string) string {
	_, name, ok := strings.Cut(id, "/")
	if !ok {
		name = id
	}
	name = strings.ReplaceAll(name, "-", " ")
	parts := strings.Fields(name)
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func markPreferredOpenCodeDefault(models []profile.Model) {
	if len(models) == 0 {
		return
	}
	prefer := []string{"openai/gpt-5.6-sol", "openai/gpt-5.6-terra", "openai/gpt-5.5"}
	for _, want := range prefer {
		for i := range models {
			if models[i].ID == want {
				models[i].Default = true
				return
			}
		}
	}
	models[0].Default = true
}
