package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"fishnet/internal/config"
)

// ─── Types ──────────────────────────────────────────────────────────────────

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// codexCLIResponse is the JSON event format emitted by the openai/codex CLI
type codexCLIResponse struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content,omitempty"`
}

// Anthropic-specific request format
type anthropicRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ─── Client ─────────────────────────────────────────────────────────────────

type Client struct {
	cfg     config.LLMConfig
	limiter *rate.Limiter
	sem     chan struct{}
	http    *http.Client
	mu      sync.Mutex
}

func New(cfg config.LLMConfig) *Client {
	rps := cfg.RateLimit
	if rps <= 0 {
		rps = 10
	}
	concurrency := cfg.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 5
	}

	return &Client{
		cfg:     cfg,
		limiter: rate.NewLimiter(rate.Limit(rps), int(rps)),
		sem:     make(chan struct{}, concurrency),
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

// Chat sends a chat completion request and returns the response text.
func (c *Client) Chat(ctx context.Context, msgs []Message) (string, error) {
	// acquire semaphore
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-c.sem }()

	// rate limit
	if err := c.limiter.Wait(ctx); err != nil {
		return "", err
	}

	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(1<<uint(attempt)) * time.Second
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		resp, err := c.doChat(ctx, msgs)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// Don't retry on non-retryable errors
		if strings.Contains(err.Error(), "invalid_api_key") ||
			strings.Contains(err.Error(), "model_not_found") {
			return "", err
		}
	}
	return "", lastErr
}

// System sends a chat with a system prompt.
func (c *Client) System(ctx context.Context, system, user string) (string, error) {
	return c.Chat(ctx, []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	})
}

// JSON sends a chat and attempts to parse the response as JSON into dst.
// It retries up to maxJSONParseRetries times on JSON parse failure, since
// LLMs can occasionally return malformed JSON. Context cancellation and API
// errors are not retried.
const maxJSONParseRetries = 3

func (c *Client) JSON(ctx context.Context, system, user string, dst interface{}) error {
	var lastParseErr error
	for attempt := 0; attempt < maxJSONParseRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		resp, err := c.System(ctx, system, user)
		if err != nil {
			return err // API errors are not retried here; Chat() already retries
		}

		// Strip markdown code fences
		resp = strings.TrimSpace(resp)
		if strings.HasPrefix(resp, "```") {
			lines := strings.SplitN(resp, "\n", 2)
			if len(lines) == 2 {
				resp = lines[1]
			}
			resp = strings.TrimSuffix(resp, "```")
			resp = strings.TrimSpace(resp)
		}
		// Also strip trailing ``` if present (some models emit it without a newline)
		resp = strings.TrimSuffix(resp, "```")
		resp = strings.TrimSpace(resp)

		if parseErr := json.Unmarshal([]byte(resp), dst); parseErr == nil {
			return nil
		} else {
			lastParseErr = fmt.Errorf("json parse failed: %w\nraw: %s", parseErr, resp)
		}
	}
	return lastParseErr
}

func (c *Client) doChat(ctx context.Context, msgs []Message) (string, error) {
	switch c.cfg.Provider {
	case "anthropic":
		return c.doAnthropic(ctx, msgs)
	case "codex-cli":
		// Try the local codex binary first; fall back to OpenAI API if not found.
		if resp, err := c.doCodexCLI(ctx, msgs); err == nil {
			return resp, nil
		}
		return c.doOpenAI(ctx, msgs)
	case "codex":
		// Codex uses the OpenAI-compatible endpoint with code-focused models (e.g. o4-mini).
		// Honour UseCodexCLI flag if set explicitly in config.
		if c.cfg.UseCodexCLI {
			if resp, err := c.doCodexCLI(ctx, msgs); err == nil {
				return resp, nil
			}
		}
		return c.doOpenAI(ctx, msgs)
	case "clicliproxy":
		// CLIProxyAPI exposes an OpenAI-compatible endpoint; route through the same handler.
		return c.doOpenAI(ctx, msgs)
	case "codex-oauth":
		return c.doCodexOAuth(ctx, msgs)
	default:
		return c.doOpenAI(ctx, msgs)
	}
}

// doCodexCLI runs the local `codex` CLI binary and parses its JSON output.
// It builds a simple prompt from the last user message (system prompt prepended).
func (c *Client) doCodexCLI(ctx context.Context, msgs []Message) (string, error) {
	bin := c.cfg.CodexBin
	if bin == "" {
		var err error
		bin, err = exec.LookPath("codex")
		if err != nil {
			return "", fmt.Errorf("codex binary not found in PATH")
		}
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("codex binary not accessible: %w", err)
	}

	// Compose a single prompt: concatenate system + user messages.
	var sb strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "system":
			sb.WriteString(m.Content)
			sb.WriteString("\n\n")
		case "user":
			sb.WriteString(m.Content)
		}
	}
	prompt := strings.TrimSpace(sb.String())

	cmd := exec.CommandContext(ctx, bin, "--json", "-q", prompt)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("codex cli exec: %w", err)
	}

	// Parse NDJSON — each line is a JSON event; collect output_text from message events.
	var result strings.Builder
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev codexCLIResponse
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "message" && ev.Role == "assistant" {
			for _, c := range ev.Content {
				if c.Type == "output_text" {
					result.WriteString(c.Text)
				}
			}
		}
	}
	text := strings.TrimSpace(result.String())
	if text == "" {
		return "", fmt.Errorf("codex cli returned empty response")
	}
	return text, nil
}

func (c *Client) doOpenAI(ctx context.Context, msgs []Message) (string, error) {
	baseURL := c.cfg.BaseURL
	if baseURL == "" {
		baseURL = config.ProviderBaseURL(c.cfg.Provider)
	}

	model := c.cfg.Model
	// Default model for Codex provider when not explicitly set.
	if model == "" && (c.cfg.Provider == "codex" || c.cfg.Provider == "codex-cli") {
		model = "o4-mini"
	}
	// Default model for CLIProxyAPI — proxies Claude Code, so use a Claude model.
	if model == "" && c.cfg.Provider == "clicliproxy" {
		model = "claude-sonnet-4-5"
	}

	// Prefer CODEX_API_KEY for codex providers; fall back to the configured key.
	apiKey := c.cfg.APIKey
	if apiKey == "" && (c.cfg.Provider == "codex" || c.cfg.Provider == "codex-cli") {
		apiKey = os.Getenv("CODEX_API_KEY")
	}
	if apiKey == "" {
		apiKey = c.cfg.APIKey
	}

	body, _ := json.Marshal(chatRequest{
		Model:     model,
		Messages:  msgs,
		MaxTokens: c.cfg.MaxTokens,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("http: %w", err)
	}
	defer res.Body.Close()

	raw, _ := io.ReadAll(res.Body)
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("decode: %w, body: %s", err, string(raw))
	}
	if cr.Error != nil {
		return "", fmt.Errorf("api error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("empty response from API")
	}
	return cr.Choices[0].Message.Content, nil
}

// CheckProxy checks if a CLIProxyAPI server is running at the given base URL.
// It issues a GET request to baseURL+"/healthz" with a 2-second timeout.
// Returns true if the server responds with HTTP 200.
func CheckProxy(baseURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// CheckCodexCLI checks if the local codex CLI binary is available and functional.
func CheckCodexCLI() bool {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.Output()
	return err == nil && len(out) > 0
}

// CheckAPIKey returns true if an API key is configured for the given provider.
func CheckAPIKey(provider, apiKey string) bool {
	if apiKey != "" {
		return true
	}
	switch provider {
	case "openai", "codex":
		return os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("CODEX_API_KEY") != ""
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY") != ""
	case "ollama":
		return true // no key needed
	}
	return false
}

func (c *Client) doAnthropic(ctx context.Context, msgs []Message) (string, error) {
	body, _ := json.Marshal(anthropicRequest{
		Model:     c.cfg.Model,
		Messages:  msgs,
		MaxTokens: c.cfg.MaxTokens,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("http: %w", err)
	}
	defer res.Body.Close()

	raw, _ := io.ReadAll(res.Body)
	var ar anthropicResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return "", fmt.Errorf("decode: %w, body: %s", err, string(raw))
	}
	if ar.Error != nil {
		return "", fmt.Errorf("api error: %s", ar.Error.Message)
	}
	if len(ar.Content) == 0 {
		return "", fmt.Errorf("empty response")
	}
	return ar.Content[0].Text, nil
}
