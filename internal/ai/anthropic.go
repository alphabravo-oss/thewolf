package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	anthropicAPIURL = "https://api.anthropic.com/v1/messages"
	anthropicModel  = "claude-sonnet-4-20250514"
)

// AnthropicProvider implements Provider using the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey      string
	model       string
	httpClient  *http.Client
	logCallback LogCallback
}

// SetLogCallback configures the logging callback.
func (p *AnthropicProvider) SetLogCallback(cb LogCallback) {
	p.logCallback = cb
}

// NewAnthropicProvider creates a provider backed by the Anthropic Claude API.
// An optional model ID can be passed to override the default (claude-sonnet-4-20250514).
func NewAnthropicProvider(apiKey string, model ...string) *AnthropicProvider {
	m := anthropicModel
	if len(model) > 0 && model[0] != "" {
		m = model[0]
	}
	return &AnthropicProvider{
		apiKey:     apiKey,
		model:      m,
		httpClient: &http.Client{},
	}
}

func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// ---------- Analyze ----------

func (p *AnthropicProvider) Analyze(ctx context.Context, req AnalyzeRequest) (*AnalyzeResponse, error) {
	prompt := buildAnalyzePrompt(req)

	body, err := p.send(ctx, prompt, 1024)
	if err != nil {
		return nil, fmt.Errorf("anthropic analyze: %w", err)
	}

	var resp AnalyzeResponse
	if err := extractJSON(body, &resp); err != nil {
		return nil, fmt.Errorf("anthropic analyze parse: %w", err)
	}
	return &resp, nil
}

// ---------- Score ----------

func (p *AnthropicProvider) Score(ctx context.Context, req ScoreRequest) (*ScoreResponse, error) {
	prompt := buildScorePrompt(req)

	body, err := p.send(ctx, prompt, 2048)
	if err != nil {
		return nil, fmt.Errorf("anthropic score: %w", err)
	}

	var resp ScoreResponse
	if err := extractJSON(body, &resp); err != nil {
		return nil, fmt.Errorf("anthropic score parse: %w", err)
	}
	return &resp, nil
}

// ---------- Summarize ----------

func (p *AnthropicProvider) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
	prompt := buildSummarizePrompt(req)

	body, err := p.sendWithSystem(ctx, prompt, 2048, systemPromptText)
	if err != nil {
		return "", fmt.Errorf("anthropic summarize: %w", err)
	}
	return body, nil
}

// ---------- Complete ----------

func (p *AnthropicProvider) Complete(ctx context.Context, prompt string) (string, error) {
	body, err := p.sendWithSystem(ctx, prompt, 4096, systemPromptText)
	if err != nil {
		return "", fmt.Errorf("anthropic complete: %w", err)
	}
	return body, nil
}

// ---------- HTTP transport ----------

// anthropicRequest is the payload sent to the Anthropic Messages API.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse is the response from the Anthropic Messages API.
type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

const (
	systemPromptJSON = "You are a security engineer. Analyze findings concisely. Output valid JSON only."
	systemPromptText = "You are a security engineer. Provide concise, actionable analysis."
)

func (p *AnthropicProvider) send(ctx context.Context, prompt string, maxTokens int) (string, error) {
	return p.sendWithSystem(ctx, prompt, maxTokens, systemPromptJSON)
}

func (p *AnthropicProvider) sendWithSystem(ctx context.Context, prompt string, maxTokens int, system string) (string, error) {
	start := time.Now()

	reqBody := anthropicRequest{
		Model:     p.model,
		MaxTokens: maxTokens,
		System:    system,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		p.emitLog(prompt, "", err.Error(), start, 0, 0, 0)
		return "", fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		p.emitLog(prompt, "", err.Error(), start, 0, 0, 0)
		return "", fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("API returned status %d: %s", httpResp.StatusCode, string(respBytes))
		p.emitLog(prompt, "", errMsg, start, 0, 0, 0)
		return "", fmt.Errorf("%s", errMsg)
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		p.emitLog(prompt, string(respBytes), err.Error(), start, 0, 0, 0)
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if apiResp.Error != nil {
		errMsg := fmt.Sprintf("API error (%s): %s", apiResp.Error.Type, apiResp.Error.Message)
		p.emitLog(prompt, "", errMsg, start, 0, 0, 0)
		return "", fmt.Errorf("%s", errMsg)
	}

	var text strings.Builder
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	if text.Len() == 0 {
		p.emitLog(prompt, "", "empty response from Anthropic API", start, 0, 0, 0)
		return "", fmt.Errorf("empty response from Anthropic API")
	}

	result := text.String()

	promptTokens := EstimateTokens(prompt)
	responseTokens := EstimateTokens(result)
	if apiResp.Usage != nil {
		promptTokens = apiResp.Usage.InputTokens
		responseTokens = apiResp.Usage.OutputTokens
	}

	p.emitLog(prompt, result, "", start, promptTokens, responseTokens, 0)
	return result, nil
}

func (p *AnthropicProvider) emitLog(prompt, response, errMsg string, start time.Time, promptTokens, responseTokens int, cost float64) {
	if p.logCallback == nil {
		return
	}
	p.logCallback(AICallLog{
		Provider:       "anthropic",
		Model:          p.model,
		Prompt:         prompt,
		Response:       response,
		Error:          errMsg,
		DurationMs:     time.Since(start).Milliseconds(),
		PromptTokens:   promptTokens,
		ResponseTokens: responseTokens,
		CostUSD:        cost,
	})
}

// ---------- Shared prompt builders ----------

func buildAnalyzePrompt(req AnalyzeRequest) string {
	var b strings.Builder
	b.WriteString("You are a senior application security engineer. Analyze the following security finding and provide a JSON response.\n\n")
	b.WriteString("## Finding\n")
	fmt.Fprintf(&b, "- Tool: %s\n", req.Finding.ToolName)
	fmt.Fprintf(&b, "- Severity: %s\n", req.Finding.Severity)
	fmt.Fprintf(&b, "- Title: %s\n", req.Finding.Title)
	fmt.Fprintf(&b, "- Description: %s\n", req.Finding.Description)
	fmt.Fprintf(&b, "- File: %s (line %d)\n", req.Finding.FilePath, req.Finding.LineStart)
	if req.Finding.CodeSnippet != "" {
		fmt.Fprintf(&b, "- Code:\n```\n%s\n```\n", req.Finding.CodeSnippet)
	}
	if req.RepoContext != "" {
		fmt.Fprintf(&b, "\n## Repository Context\n%s\n", req.RepoContext)
	}
	b.WriteString("\nRespond with ONLY a JSON object (no markdown fences) with these fields:\n")
	b.WriteString(`- "fix_suggestion": a concrete, actionable fix for this finding`)
	b.WriteString("\n")
	b.WriteString(`- "context_score": a float from 0 to 10 rating how critical this finding is in context (10 = most critical)`)
	b.WriteString("\n")
	b.WriteString(`- "explanation": a brief explanation of the score and the finding's real-world impact`)
	b.WriteString("\n")
	return b.String()
}

func buildScorePrompt(req ScoreRequest) string {
	var b strings.Builder
	b.WriteString("You are a senior application security engineer. Score the following security findings by contextual severity.\n\n")
	if req.RepoContext != "" {
		fmt.Fprintf(&b, "## Repository Context\n%s\n\n", req.RepoContext)
	}
	b.WriteString("## Findings\n")
	for i, f := range req.Findings {
		fmt.Fprintf(&b, "\n### Finding %d\n", i)
		fmt.Fprintf(&b, "- Tool: %s\n", f.ToolName)
		fmt.Fprintf(&b, "- Severity: %s\n", f.Severity)
		fmt.Fprintf(&b, "- Title: %s\n", f.Title)
		fmt.Fprintf(&b, "- File: %s (line %d)\n", f.FilePath, f.LineStart)
		if f.Description != "" {
			fmt.Fprintf(&b, "- Description: %s\n", f.Description)
		}
	}
	b.WriteString("\nRespond with ONLY a JSON object (no markdown fences) with a single field:\n")
	b.WriteString(`- "scores": an array of objects, one per finding, each with:`)
	b.WriteString("\n")
	b.WriteString(`  - "index": the finding index (starting at 0)`)
	b.WriteString("\n")
	b.WriteString(`  - "context_score": a float from 0 to 10`)
	b.WriteString("\n")
	b.WriteString(`  - "explanation": brief justification for the score`)
	b.WriteString("\n")
	return b.String()
}

func buildSummarizePrompt(req SummarizeRequest) string {
	var b strings.Builder
	b.WriteString("You are a senior application security engineer. Write a concise executive summary of the following scan results.\n\n")
	fmt.Fprintf(&b, "## Scan: %s\n", req.ScanID)
	fmt.Fprintf(&b, "- Repository: %s\n", req.RepoName)
	fmt.Fprintf(&b, "- Total findings: %d\n", req.TotalFindings)

	if len(req.BySeverity) > 0 {
		b.WriteString("\n### By Severity\n")
		for sev, count := range req.BySeverity {
			fmt.Fprintf(&b, "- %s: %d\n", sev, count)
		}
	}
	if len(req.ByCategory) > 0 {
		b.WriteString("\n### By Category\n")
		for cat, count := range req.ByCategory {
			fmt.Fprintf(&b, "- %s: %d\n", cat, count)
		}
	}
	if len(req.ByTool) > 0 {
		b.WriteString("\n### By Tool\n")
		for tool, count := range req.ByTool {
			fmt.Fprintf(&b, "- %s: %d\n", tool, count)
		}
	}
	if len(req.Languages) > 0 {
		b.WriteString("\n### Languages Detected\n")
		for lang, lines := range req.Languages {
			fmt.Fprintf(&b, "- %s: %d lines\n", lang, lines)
		}
	}
	if len(req.Frameworks) > 0 {
		fmt.Fprintf(&b, "\n### Frameworks: %s\n", strings.Join(req.Frameworks, ", "))
	}
	if len(req.TopFindings) > 0 {
		b.WriteString("\n### Top Findings\n")
		for i, f := range req.TopFindings {
			fmt.Fprintf(&b, "%d. [%s] %s — %s (%s)\n", i+1, f.Severity, f.Title, f.FilePath, f.ToolName)
		}
	}

	b.WriteString("\nWrite a 2-4 paragraph executive summary covering:\n")
	b.WriteString("1. Overall security posture\n")
	b.WriteString("2. Most critical issues requiring immediate attention\n")
	b.WriteString("3. Recommendations for remediation priority\n")
	b.WriteString("Respond in plain text, no JSON.\n")
	return b.String()
}

// ---------- JSON extraction ----------

// extractJSON attempts to parse a JSON object from the AI response text.
// It handles cases where the model wraps JSON in markdown fences.
func extractJSON(text string, dest interface{}) error {
	// Try direct parse first.
	text = strings.TrimSpace(text)
	if err := json.Unmarshal([]byte(text), dest); err == nil {
		return nil
	}

	// Strip markdown code fences if present.
	cleaned := text
	if idx := strings.Index(cleaned, "```json"); idx != -1 {
		cleaned = cleaned[idx+7:]
	} else if idx := strings.Index(cleaned, "```"); idx != -1 {
		cleaned = cleaned[idx+3:]
	}
	if idx := strings.LastIndex(cleaned, "```"); idx != -1 {
		cleaned = cleaned[:idx]
	}
	cleaned = strings.TrimSpace(cleaned)

	if err := json.Unmarshal([]byte(cleaned), dest); err != nil {
		return fmt.Errorf("failed to parse JSON from response: %w\nraw text: %s", err, text)
	}
	return nil
}
