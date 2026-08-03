package routes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/prompt"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// ---------------------------------------------------------------------------
// Prompt Templates
// ---------------------------------------------------------------------------

// ListPromptTemplates returns prompt templates filtered by scope and scope_id.
// GET /api/ai-prompts?scope=global&scope_id=
func ListPromptTemplates(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "global"
	}
	scopeID := r.URL.Query().Get("scope_id")

	templates, err := h.Store.ListPromptTemplates(r.Context(), scope, scopeID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list prompt templates")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: templates})
}

// upsertPromptRequest is the request body for creating/updating a prompt template.
type upsertPromptRequest struct {
	Scope      string `json:"scope"`
	ScopeID    string `json:"scope_id"`
	PromptType string `json:"prompt_type"`
	Section    string `json:"section"`
	Content    string `json:"content"`
}

// validScopes is the set of allowed scope values.
var validScopes = map[string]bool{
	"global":     true,
	"collection": true,
}

// validPromptTypes is the set of allowed prompt_type values.
var validPromptTypes = map[string]bool{
	"tool_assess":       true,
	"executive_summary": true,
}

// validSections is the set of allowed section values.
var validSections = map[string]bool{
	"system_context":      true,
	"scoring_criteria":    true,
	"output_instructions": true,
}

// UpsertPromptTemplate creates or updates a prompt template.
// PUT /api/ai-prompts
func UpsertPromptTemplate(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var req upsertPromptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	// Validate fields.
	if !validScopes[req.Scope] {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "scope must be \"global\" or \"collection\"")
		return
	}
	if !validPromptTypes[req.PromptType] {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "prompt_type must be \"tool_assess\" or \"executive_summary\"")
		return
	}
	if !validSections[req.Section] {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "section must be \"system_context\", \"scoring_criteria\", or \"output_instructions\"")
		return
	}
	if req.Content == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "content is required")
		return
	}

	ctx := r.Context()

	// Check if a template already exists for this combination.
	existing, err := h.Store.ListPromptTemplates(ctx, req.Scope, req.ScopeID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to query templates")
		return
	}

	var match *models.AIPromptTemplate
	for i := range existing {
		if existing[i].PromptType == req.PromptType && existing[i].Section == req.Section {
			match = &existing[i]
			break
		}
	}

	now := time.Now()

	if match != nil {
		// Update existing template.
		match.Content = req.Content
		match.UpdatedAt = now
		if err := h.Store.UpdatePromptTemplate(ctx, match); err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update prompt template")
			return
		}
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: match})
		return
	}

	// Create new template.
	tmpl := &models.AIPromptTemplate{
		ID:         uuid.New().String(),
		Scope:      req.Scope,
		ScopeID:    req.ScopeID,
		PromptType: req.PromptType,
		Section:    req.Section,
		Content:    req.Content,
		UpdatedAt:  now,
	}
	if err := h.Store.CreatePromptTemplate(ctx, tmpl); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create prompt template")
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: tmpl})
}

// DeletePromptTemplate removes a prompt template by ID.
// DELETE /api/ai-prompts/{id}
func DeletePromptTemplate(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "id is required")
		return
	}

	if err := h.Store.DeletePromptTemplate(r.Context(), id); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to delete prompt template")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]string{"deleted": id},
	})
}

// GetPromptDefaults returns all hardcoded default prompt sections.
// GET /api/ai-prompts/defaults
func GetPromptDefaults(w http.ResponseWriter, _ *http.Request) {
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: prompt.AllDefaults(),
	})
}

// ---------------------------------------------------------------------------
// Prompt Preview
// ---------------------------------------------------------------------------

// previewRequest is the request body for prompt preview.
type previewRequest struct {
	PromptType   string `json:"prompt_type"`
	CollectionID string `json:"collection_id"`
}

// PreviewPrompt resolves all sections for the given prompt type and collection,
// then builds a sample prompt with dummy data.
// POST /api/ai-prompts/preview
func PreviewPrompt(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var req previewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if !validPromptTypes[req.PromptType] {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "prompt_type must be \"tool_assess\" or \"executive_summary\"")
		return
	}

	ctx := r.Context()
	sections := [3]string{}
	sectionNames := [3]string{prompt.SectionSystemCtx, prompt.SectionScoring, prompt.SectionOutputInstr}

	for i, sec := range sectionNames {
		resolved, err := h.Store.ResolvePromptSection(ctx, req.PromptType, sec, req.CollectionID)
		if err != nil || resolved == "" {
			// Fall back to hardcoded default.
			resolved = prompt.GetDefault(req.PromptType, sec)
		}
		sections[i] = resolved
	}

	var assembled string
	switch req.PromptType {
	case prompt.TypeToolAssess:
		assembled = buildSampleToolAssessPrompt(sections[0], sections[1], sections[2])
	case prompt.TypeExecSummary:
		assembled = buildSampleExecSummaryPrompt(sections[0], sections[1], sections[2])
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]string{"prompt": assembled},
	})
}

// buildSampleToolAssessPrompt assembles a preview prompt for tool assessment
// using resolved sections and sample data.
func buildSampleToolAssessPrompt(systemCtx, scoring, outputInstr string) string {
	sampleData := `
--- TOOL: semgrep ---
Repository: example-repo (Go, JavaScript)
Findings (3 total): 1 high, 1 medium, 1 low

[0] HIGH: Potential SQL injection in db/query.go:42
[1] MEDIUM: Hardcoded timeout value in server/config.go:15
[2] LOW: Unused import in utils/helpers.go:3`

	return systemCtx + "\n\n" + scoring + "\n\n" + outputInstr + "\n\n" + sampleData
}

// buildSampleExecSummaryPrompt assembles a preview prompt for executive summary
// using resolved sections and sample data.
func buildSampleExecSummaryPrompt(systemCtx, scoring, outputInstr string) string {
	sampleData := `
--- SCAN SUMMARY ---
Repository: example-repo
Tools run: semgrep, gosec, trivy, gitleaks
Total findings: 12 (2 critical, 3 high, 4 medium, 3 low)

Tool Summaries:
- semgrep: Found 5 issues including potential SQL injection and XSS patterns
- gosec: Found 3 issues including hardcoded credentials and weak crypto
- trivy: Found 2 vulnerable dependencies with known CVEs
- gitleaks: Found 2 potential secrets in configuration files`

	return systemCtx + "\n\n" + scoring + "\n\n" + outputInstr + "\n\n" + sampleData
}

// ---------------------------------------------------------------------------
// AI Providers
// ---------------------------------------------------------------------------

// aiProvider represents an available AI provider and its models.
type aiProvider struct {
	Name      string   `json:"name"`
	Available bool     `json:"available"`
	Models    []string `json:"models"`
}

// ListAIProviders returns the available AI providers and their models.
// Models are fetched live from each provider's API when an API key is configured.
// GET /api/ai-providers
func ListAIProviders(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var providers []aiProvider

	// Check for claude CLI — no model listing API, use static list.
	if _, err := exec.LookPath("claude"); err == nil {
		providers = append(providers, aiProvider{
			Name:      "claude-code",
			Available: true,
			Models:    []string{"claude-sonnet-4-20250514", "claude-haiku-4-5-20251001"},
		})
	}

	// Look up API keys from secrets store, falling back to env vars.
	ctx := r.Context()
	allSecrets, _ := h.Store.ListSecretsByUser(ctx, "")
	secretValue := func(keyType models.KeyType) string {
		for _, s := range allSecrets {
			if s.KeyType == keyType {
				decrypted, _ := secrets.Decrypt(s.EncryptedValue)
				if decrypted != "" {
					return decrypted
				}
			}
		}
		return ""
	}

	anthropicKey := secretValue(models.KeyTypeAnthropicKey)
	if anthropicKey == "" {
		anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if anthropicKey != "" {
		models := fetchAnthropicModels(anthropicKey)
		providers = append(providers, aiProvider{
			Name:      "anthropic",
			Available: true,
			Models:    models,
		})
	}

	openaiKey := secretValue(models.KeyTypeOpenAIKey)
	if openaiKey == "" {
		openaiKey = os.Getenv("OPENAI_API_KEY")
	}
	if openaiKey != "" {
		models := fetchOpenAIModels(openaiKey)
		providers = append(providers, aiProvider{
			Name:      "openai",
			Available: true,
			Models:    models,
		})
	}

	if providers == nil {
		providers = []aiProvider{}
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: providers})
}

// fetchAnthropicModels calls the Anthropic /v1/models endpoint to get available models.
func fetchAnthropicModels(apiKey string) []string {
	fallback := []string{"claude-sonnet-4-20250514", "claude-haiku-4-5-20251001", "claude-opus-4-20250514"}

	req, err := http.NewRequest("GET", "https://api.anthropic.com/v1/models?limit=100", nil)
	if err != nil {
		return fallback
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fallback
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fallback
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fallback
	}

	if len(result.Data) == 0 {
		return fallback
	}

	var models []string
	for _, m := range result.Data {
		models = append(models, m.ID)
	}
	sort.Strings(models)
	return models
}

// openAIChatModelPrefixes are model ID prefixes that indicate chat-capable models.
var openAIChatModelPrefixes = []string{"gpt-4", "gpt-3.5", "o1", "o3", "o4", "chatgpt"}

// fetchOpenAIModels calls the OpenAI /v1/models endpoint and filters to chat models.
func fetchOpenAIModels(apiKey string) []string {
	fallback := []string{"gpt-4o", "gpt-4o-mini", "o3-mini"}

	req, err := http.NewRequest("GET", "https://api.openai.com/v1/models", nil)
	if err != nil {
		return fallback
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fallback
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fallback
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fallback
	}

	if len(result.Data) == 0 {
		return fallback
	}

	var models []string
	for _, m := range result.Data {
		id := m.ID
		for _, prefix := range openAIChatModelPrefixes {
			if strings.HasPrefix(id, prefix) {
				models = append(models, id)
				break
			}
		}
	}
	if len(models) == 0 {
		return fallback
	}
	sort.Strings(models)
	return models
}
