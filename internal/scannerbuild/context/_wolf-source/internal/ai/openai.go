package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	openaiAPIURL = "https://api.openai.com/v1/chat/completions"
	openaiModel  = "gpt-4o"
	xaiAPIURL    = "https://api.x.ai/v1/chat/completions"
	xaiModel     = "grok-4"
)

// OpenAIProvider implements Provider using the OpenAI Chat Completions API.
type OpenAIProvider struct {
	apiKey     string
	httpClient *http.Client
	name       string
	apiURL     string
	model      string
}

// NewOpenAIProvider creates a provider backed by the OpenAI API.
func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		name:       "openai",
		apiURL:     openaiAPIURL,
		model:      openaiModel,
	}
}

// NewXAIProvider creates a Grok provider via the xAI OpenAI-compatible API.
func NewXAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		name:       "xai",
		apiURL:     xaiAPIURL,
		model:      xaiModel,
	}
}

func (p *OpenAIProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return "openai"
}

// ---------- Analyze ----------

func (p *OpenAIProvider) Analyze(ctx context.Context, req AnalyzeRequest) (*AnalyzeResponse, error) {
	prompt := buildAnalyzePrompt(req)

	body, err := p.send(ctx, prompt, 1024)
	if err != nil {
		return nil, fmt.Errorf("openai analyze: %w", err)
	}

	var resp AnalyzeResponse
	if err := extractJSON(body, &resp); err != nil {
		return nil, fmt.Errorf("openai analyze parse: %w", err)
	}
	return &resp, nil
}

// ---------- Score ----------

func (p *OpenAIProvider) Score(ctx context.Context, req ScoreRequest) (*ScoreResponse, error) {
	prompt := buildScorePrompt(req)

	body, err := p.send(ctx, prompt, 2048)
	if err != nil {
		return nil, fmt.Errorf("openai score: %w", err)
	}

	var resp ScoreResponse
	if err := extractJSON(body, &resp); err != nil {
		return nil, fmt.Errorf("openai score parse: %w", err)
	}
	return &resp, nil
}

// ---------- Summarize ----------

func (p *OpenAIProvider) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
	prompt := buildSummarizePrompt(req)

	body, err := p.send(ctx, prompt, 2048)
	if err != nil {
		return "", fmt.Errorf("openai summarize: %w", err)
	}
	return body, nil
}

// ---------- Complete ----------

func (p *OpenAIProvider) Complete(ctx context.Context, prompt string) (string, error) {
	body, err := p.send(ctx, prompt, 4096)
	if err != nil {
		return "", fmt.Errorf("openai complete: %w", err)
	}
	return body, nil
}

// ---------- HTTP transport ----------

// openaiRequest is the payload for the OpenAI Chat Completions API.
type openaiRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []openaiMessage `json:"messages"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openaiResponse is the response from the OpenAI Chat Completions API.
type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (p *OpenAIProvider) send(ctx context.Context, prompt string, maxTokens int) (string, error) {
	model := p.model
	if model == "" {
		model = openaiModel
	}
	apiURL := p.apiURL
	if apiURL == "" {
		apiURL = openaiAPIURL
	}
	reqBody := openaiRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages: []openaiMessage{
			{
				Role:    "system",
				Content: "You are a senior application security engineer. Respond precisely as instructed.",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", httpResp.StatusCode, string(respBytes))
	}

	var apiResp openaiResponse
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if apiResp.Error != nil {
		return "", fmt.Errorf("API error (%s): %s", apiResp.Error.Type, apiResp.Error.Message)
	}

	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("empty response from OpenAI API")
	}

	content := strings.TrimSpace(apiResp.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("empty content in OpenAI response")
	}

	return content, nil
}
