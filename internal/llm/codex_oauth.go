package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ─── Constants ───────────────────────────────────────────────────────────────

const (
	codexClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	codexTokenURL    = "https://auth.openai.com/oauth/token"
	codexRedirectURI = "http://localhost:1455/auth/callback"
	codexScope       = "openid profile email offline_access"
	codexAPIURL      = "https://chatgpt.com/backend-api/codex/responses"
)

// ─── CodexTokens ─────────────────────────────────────────────────────────────

// CodexTokens holds OAuth tokens for the Codex API.
type CodexTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiresAtMs  int64  `json:"expires_at_ms"` // Unix milliseconds
}

// NeedsRefresh returns true if the access token is missing or within 60s of expiry.
func (t *CodexTokens) NeedsRefresh() bool {
	return t.AccessToken == "" || time.Now().UnixMilli() >= t.ExpiresAtMs-60000
}

// ─── Token file helpers ───────────────────────────────────────────────────────

// CodexTokenPath returns the path to the stored OAuth token file.
func CodexTokenPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "fishnet", "codex-oauth.json")
}

// LoadCodexTokens reads the token file from disk.
func LoadCodexTokens() (*CodexTokens, error) {
	data, err := os.ReadFile(CodexTokenPath())
	if err != nil {
		return nil, err
	}
	var t CodexTokens
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse codex tokens: %w", err)
	}
	return &t, nil
}

// SaveCodexTokens writes the token file to disk with mode 0600.
func SaveCodexTokens(t *CodexTokens) error {
	p := CodexTokenPath()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// RemoveCodexTokens deletes the token file.
func RemoveCodexTokens() error {
	err := os.Remove(CodexTokenPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// CodexLoggedIn returns true if the token file exists and has a non-empty AccessToken.
func CodexLoggedIn() bool {
	t, err := LoadCodexTokens()
	if err != nil {
		return false
	}
	return t != nil && t.AccessToken != ""
}

// NowMs returns the current time in Unix milliseconds.
func NowMs() int64 {
	return time.Now().UnixMilli()
}

// ─── PKCE helpers ─────────────────────────────────────────────────────────────

func pkceS256() (verifier, challenge string) {
	b := make([]byte, 32)
	rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

// ─── CodexLogin ──────────────────────────────────────────────────────────────

// CodexLogin launches the PKCE OAuth flow in the user's browser and waits for the callback.
func CodexLogin() (*CodexTokens, error) {
	verifier, challenge := pkceS256()

	// Random state
	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	state := fmt.Sprintf("%x", stateBytes)

	// Build auth URL
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", codexClientID)
	params.Set("redirect_uri", codexRedirectURI)
	params.Set("scope", codexScope)
	params.Set("state", state)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("prompt", "login")
	params.Set("id_token_add_organizations", "true")
	params.Set("originator", "codex_cli_rs")
	params.Set("codex_cli_simplified_flow", "true")
	authURL := codexAuthorizeURL + "?" + params.Encode()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	// Start local callback server
	listener, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return nil, fmt.Errorf("start callback server: %w", err)
	}

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}

	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		// Surface any error OpenAI sends back (e.g. invalid_auth_step, access_denied).
		if oauthErr := q.Get("error"); oauthErr != "" {
			desc := q.Get("error_description")
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<!DOCTYPE html><html><body style="font-family:sans-serif;text-align:center;padding:60px">
<h2>Authentication failed</h2><p>%s: %s</p><p>You can close this tab.</p>
</body></html>`, oauthErr, desc)
			errCh <- fmt.Errorf("codex auth error: %s — %s", oauthErr, desc)
			return
		}

		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- fmt.Errorf("OAuth state mismatch")
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("no authorization code in OAuth callback")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body style="font-family:sans-serif;text-align:center;padding:60px">
<h2>Authentication successful!</h2>
<p>You can close this tab and return to the terminal.</p>
</body></html>`)
		codeCh <- code
	})

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("callback server: %w", err)
		}
	}()

	fmt.Println("Opening browser for Codex OAuth...")
	fmt.Println("Auth URL:", authURL)
	openBrowser(authURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	defer srv.Shutdown(context.Background())

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("timed out waiting for OAuth callback (5 min)")
	}

	return codexExchangeCode(code, verifier)
}

func openBrowser(u string) {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	default:
		cmd = "xdg-open"
	}
	exec.Command(cmd, u).Start()
}

// ─── Token exchange ───────────────────────────────────────────────────────────

func codexExchangeCode(code, verifier string) (*CodexTokens, error) {
	params := url.Values{}
	params.Set("grant_type", "authorization_code")
	params.Set("code", code)
	params.Set("code_verifier", verifier)
	params.Set("client_id", codexClientID)
	params.Set("redirect_uri", codexRedirectURI)

	resp, err := http.PostForm(codexTokenURL, params)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("token exchange decode: %w", err)
	}

	if errMsg, ok := raw["error"]; ok {
		return nil, fmt.Errorf("token exchange error: %s", string(errMsg))
	}

	var accessToken, refreshToken, idToken string
	var expiresIn int64

	if v, ok := raw["access_token"]; ok {
		json.Unmarshal(v, &accessToken)
	}
	if v, ok := raw["refresh_token"]; ok {
		json.Unmarshal(v, &refreshToken)
	}
	if v, ok := raw["id_token"]; ok {
		json.Unmarshal(v, &idToken)
	}
	if v, ok := raw["expires_in"]; ok {
		json.Unmarshal(v, &expiresIn)
	}

	return &CodexTokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		IDToken:      idToken,
		ExpiresAtMs:  time.Now().UnixMilli() + expiresIn*1000,
	}, nil
}

// RefreshCodexTokens uses the refresh_token to obtain a new access_token.
func RefreshCodexTokens(t *CodexTokens) (*CodexTokens, error) {
	params := url.Values{}
	params.Set("grant_type", "refresh_token")
	params.Set("refresh_token", t.RefreshToken)
	params.Set("client_id", codexClientID)

	resp, err := http.PostForm(codexTokenURL, params)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	defer resp.Body.Close()

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("refresh token decode: %w", err)
	}

	if errMsg, ok := raw["error"]; ok {
		return nil, fmt.Errorf("refresh token error: %s", string(errMsg))
	}

	var accessToken, refreshToken string
	var expiresIn int64

	if v, ok := raw["access_token"]; ok {
		json.Unmarshal(v, &accessToken)
	}
	// Some servers return a new refresh token, others don't.
	if v, ok := raw["refresh_token"]; ok {
		json.Unmarshal(v, &refreshToken)
	} else {
		refreshToken = t.RefreshToken
	}
	if v, ok := raw["expires_in"]; ok {
		json.Unmarshal(v, &expiresIn)
	}

	return &CodexTokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAtMs:  time.Now().UnixMilli() + expiresIn*1000,
	}, nil
}

// loadAndRefreshCodexTokens loads tokens, refreshes if needed, saves, and returns them.
func loadAndRefreshCodexTokens() (*CodexTokens, error) {
	t, err := LoadCodexTokens()
	if err != nil {
		return nil, fmt.Errorf("load codex tokens: %w", err)
	}
	if t.NeedsRefresh() {
		t, err = RefreshCodexTokens(t)
		if err != nil {
			return nil, fmt.Errorf("refresh codex tokens: %w", err)
		}
		if err := SaveCodexTokens(t); err != nil {
			return nil, fmt.Errorf("save refreshed tokens: %w", err)
		}
	}
	return t, nil
}

// ─── JWT helper ───────────────────────────────────────────────────────────────

// accountIDFromJWT extracts the chatgpt_account_id from the JWT's middle (payload) segment.
func accountIDFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	authRaw, ok := claims["https://api.openai.com/auth"]
	if !ok {
		return ""
	}
	var auth map[string]string
	if err := json.Unmarshal(authRaw, &auth); err != nil {
		return ""
	}
	return auth["chatgpt_account_id"]
}

// ─── doCodexOAuth ─────────────────────────────────────────────────────────────

// doCodexOAuth sends a request to the Codex Responses API using OAuth tokens.
func (c *Client) doCodexOAuth(ctx context.Context, msgs []Message) (string, error) {
	tokens, err := loadAndRefreshCodexTokens()
	if err != nil {
		return "", fmt.Errorf("codex oauth: %w", err)
	}

	// Prefer id_token for account ID (matches CLIProxyAPI reference); fall back to access_token.
	idSrc := tokens.IDToken
	if idSrc == "" {
		idSrc = tokens.AccessToken
	}
	accountID := accountIDFromJWT(idSrc)

	// Build request body
	model := c.cfg.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	type inputMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	var instructions string
	var input []inputMsg
	for _, m := range msgs {
		if m.Role == "system" {
			instructions = m.Content
		} else {
			input = append(input, inputMsg{Role: m.Role, Content: m.Content})
		}
	}
	// Codex Responses API requires instructions even when there is no system prompt.
	if instructions == "" {
		instructions = "You are a helpful assistant."
	}

	bodyMap := map[string]interface{}{
		"model":        model,
		"stream":       true,
		"store":        false,
		"input":        input,
		"instructions": instructions,
	}

	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return "", fmt.Errorf("codex oauth marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", codexAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("Accept", "text/event-stream")
	if accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("codex oauth request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read body to get the actual error message from the server
		bodyBuf := make([]byte, 512)
		n, _ := resp.Body.Read(bodyBuf)
		return "", fmt.Errorf("codex oauth: status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBuf[:n])))
	}

	// Parse SSE stream
	var result strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		// Try response.output_text.delta
		if typeRaw, ok := event["type"]; ok {
			var eventType string
			json.Unmarshal(typeRaw, &eventType)

			if eventType == "response.output_text.delta" {
				if deltaRaw, ok := event["delta"]; ok {
					var delta string
					if json.Unmarshal(deltaRaw, &delta) == nil {
						result.WriteString(delta)
					}
				}
			} else if eventType == "response.output_item.done" {
				// fallback: extract text from item
				if itemRaw, ok := event["item"]; ok {
					var item map[string]json.RawMessage
					if json.Unmarshal(itemRaw, &item) == nil {
						if contentRaw, ok := item["content"]; ok {
							var content []map[string]json.RawMessage
							if json.Unmarshal(contentRaw, &content) == nil {
								for _, c := range content {
									if textRaw, ok := c["text"]; ok {
										var text string
										if json.Unmarshal(textRaw, &text) == nil && result.Len() == 0 {
											result.WriteString(text)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("codex oauth stream: %w", err)
	}

	text := strings.TrimSpace(result.String())
	if text == "" {
		return "", fmt.Errorf("codex oauth: empty response")
	}
	return text, nil
}

// CheckCodexOAuth returns true if Codex OAuth tokens are stored and valid.
func CheckCodexOAuth() bool {
	return CodexLoggedIn()
}
