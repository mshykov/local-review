// Package llm is a minimal HTTP client for the OpenAI chat-completions
// API. We deliberately avoid pulling in any vendor SDK — every major
// provider (OpenAI, Anthropic via /v1/chat/completions, Together,
// Groq, OpenRouter, Ollama, vLLM) speaks this dialect.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// cgnatRange is RFC 6598 Shared Address Space (100.64.0.0/10), the
// carrier-grade-NAT block Tailscale draws tailnet IPs from. Parsed
// once at init; isLocalURL uses it to treat Ollama-over-Tailscale as
// local.
var cgnatRange = mustCIDR("100.64.0.0/10")

// mustCIDR parses a CIDR literal at init, panicking with a clear
// message if it's malformed. Fail-loud: the only caller passes a
// compile-time constant, so a panic here means a typo introduced
// during maintenance — far better caught at startup than as a nil
// *net.IPNet dereference deep inside isLocalURL on the first request.
func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("llm: invalid CIDR literal %q: %v", s, err))
	}
	return n
}

// isLocalURL returns true when raw is a URL whose host points at the
// local machine OR a private LAN range. Used to skip the API-key
// requirement for Ollama / vLLM-style setups where the provider
// doesn't authenticate.
//
// Recognised host shapes:
//   - localhost / ::1 / 0.0.0.0
//   - loopback: 127.0.0.0/8
//   - IPv4 private RFC1918: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
//   - IPv4 RFC6598 CGNAT (Tailscale): 100.64.0.0/10
//   - IPv4 link-local APIPA: 169.254.0.0/16 (rare but legitimate)
//   - IPv6 unique-local: fc00::/7
//   - IPv6 link-local: fe80::/10
//
// Why RFC1918 was added (v0.10.4): users running Ollama on a LAN
// server and pointing local-review at it via `provider.base_url:
// http://192.168.1.50:11434/v1` hit a confusing "no API key" error,
// even though Ollama doesn't authenticate. The pre-v0.10.4 narrow
// scope (loopback only) was a deliberate choice — "corporate API
// gateway at 10.0.0.5 still authenticates and shouldn't bypass the
// check" — but in practice it shadowed the more common Ollama-on-
// LAN use case. The fix preserves the gateway-must-authenticate
// invariant: if the user has explicitly set `provider.api_key` or
// `provider.api_key_env`, the client uses it (the bypass only fires
// when api_key is unset). A corporate gateway operator who needs
// auth-enforcement on RFC1918 hosts therefore just sets api_key as
// they would have for any non-local URL.
//
// Any non-local URL (public IP, FQDN) still requires a key.
func isLocalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	// Named-host fast path; also catches 0.0.0.0 (which parses as
	// IP below but is documented as a "local" placeholder).
	switch host {
	case "localhost", "0.0.0.0":
		return true
	}
	// Strip any IPv6 zone/interface suffix (RFC 6874) before
	// net.ParseIP, which doesn't accept the `%zone` form. Without
	// this strip, `[fe80::1%en0]` would fall through to "remote"
	// and incorrectly require an API key. (Copilot caught this on
	// PR #86.)
	if i := strings.Index(host, "%"); i > 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Non-IP host (FQDN, mDNS .local, etc) that isn't "localhost":
		// treat as remote, require auth. Falls through.
		return false
	}
	// IPv4 loopback (127.0.0.0/8): includes 127.0.0.1 and any
	// 127.x.x.x alias for a local listener.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 127 {
		return true
	}
	if ip.IsLoopback() { // catches ::1
		return true
	}
	if ip.IsPrivate() { // RFC1918 + RFC4193 unique-local
		return true
	}
	if ip.IsLinkLocalUnicast() { // 169.254/16 + fe80::/10
		return true
	}
	// RFC 6598 Shared Address Space (100.64.0.0/10) — the CGNAT range,
	// and notably what Tailscale assigns to tailnet nodes. Running
	// Ollama on another box reachable over Tailscale (a `100.x` IP) is
	// one of the most common remote-Ollama setups; Go's IsPrivate()
	// doesn't cover it, so without this an Ollama-over-Tailscale URL
	// was treated as remote and hard-errored "no API key". A tailnet
	// link is already encrypted + ACL'd, and the c.APIKey == "" guard
	// still lets a gateway operator force auth by setting a key.
	if cgnatRange.Contains(ip) {
		return true
	}
	// Belt-and-braces fallback for 127.* in case the upstream parse
	// changes shape: stays consistent with pre-v0.10.4 behaviour.
	return strings.HasPrefix(host, "127.")
}

// maxResponseBytes caps how much we'll read from a chat-completions
// response. A typical review response is well under 100 KB; 10 MB is
// generous headroom for an unusually verbose model and small enough
// to crash the CLI with a clear error rather than exhaust RAM if a
// provider streams unbounded data, hangs mid-transmission, or is
// spoofed by a man-in-the-middle on the configured base URL.
const maxResponseBytes = 10 * 1024 * 1024

// Client wraps a base URL + API key. Construct once, reuse.
type Client struct {
	BaseURL string
	APIKey  string
	// APIKeyEnv is the env var the key was sourced from; used in the empty-key error.
	APIKeyEnv string
	Model     string
	HTTP      *http.Client
}

// New returns a Client with a sensible default timeout.
func New(baseURL, apiKey, apiKeyEnv, model string, timeoutSec int) *Client {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		APIKey:    apiKey,
		APIKeyEnv: apiKeyEnv,
		Model:     model,
		HTTP:      &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

// Message is one turn in the chat.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request body for /v1/chat/completions.
type request struct {
	Model          string       `json:"model"`
	Messages       []Message    `json:"messages"`
	ResponseFormat *responseFmt `json:"response_format,omitempty"`
	Temperature    float32      `json:"temperature,omitempty"`
}

type responseFmt struct {
	Type string `json:"type"` // "json_object"
}

type response struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	// Usage is the prompt/completion token count every OpenAI-compatible
	// provider populates on a successful response. Surfaced to callers
	// via Complete's return so internal/agents/provider.Invoker can map
	// it to agents.TokenUsage and per-call counts show up in the roster
	// line. Missing or zero on partial/older implementations; treat as
	// unknown (the provider invoker folds total_tokens-only responses
	// into InputTokens with TotalOnly=true so they're not lost).
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Complete sends a chat completion and returns the assistant's text
// plus token usage from the provider's `usage` object (zero values
// when the provider omitted it).
//
// jsonMode requests structured JSON output via response_format. Most
// providers respect this; for those that don't, the system prompt
// should still steer the model to JSON.
func (c *Client) Complete(ctx context.Context, msgs []Message, jsonMode bool) (string, Usage, error) {
	// Scheme guard at the actual exfil sink: Complete POSTs the diff (or,
	// under audit, the source) to BaseURL. The pre-flight probe checks
	// this too, but --no-preflight skips the probe, so validate here as
	// well — a non-http(s) base_url must never reach the HTTP client.
	if u, err := url.Parse(c.BaseURL); err != nil || (strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https") {
		return "", Usage{}, fmt.Errorf("invalid base_url %q: must be an http:// or https:// URL", c.BaseURL)
	}
	if c.APIKey == "" && !isLocalURL(c.BaseURL) {
		// Local-review init's "Ollama" preset writes a config with no
		// api_key_env line because Ollama doesn't authenticate. A blank
		// LOCAL_REVIEW_API_KEY (the legacy default) used to crash the
		// review here despite the provider needing no key. Skip the
		// check when base_url points at a local-or-LAN host (see
		// isLocalURL — loopback + RFC1918 + CGNAT/Tailscale + IPv6
		// unique/link-local);
		// any public URL still requires a key. A user with a LAN
		// gateway that DOES authenticate just sets api_key as usual
		// (the !c.APIKey guard here means the bypass only fires when
		// no key is configured).
		// Name the env var the user actually configured
		// (llms.<name>.api_key_env), threaded through from the provider
		// spec. When it's empty the key was never wired to a var name, so
		// point at the config field rather than the removed-in-v0.15
		// LOCAL_REVIEW_API_KEY default the old fallback named.
		if envName := strings.TrimSpace(c.APIKeyEnv); envName != "" {
			return "", Usage{}, fmt.Errorf("no API key: $%s is unset or empty\n         export %s=... (or run `local-review init`), or use an LLM CLI instead (see `local-review doctor`)", envName, envName)
		}
		return "", Usage{}, fmt.Errorf("no API key configured for this provider\n         set llms.<name>.api_key_env to an env var name and export the key, or run `local-review init`\n         (local/LAN endpoints need no key — see `local-review doctor`)")
	}

	req := request{
		Model:       c.Model,
		Messages:    msgs,
		Temperature: 0.2, // low temp for review consistency
	}
	if jsonMode {
		req.ResponseFormat = &responseFmt{Type: "json_object"}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		// Only set Authorization when we actually have a key. Sending
		// `Authorization: Bearer ` with an empty token breaks some
		// local OpenAI-compatible servers (Ollama, vLLM) that expect
		// the header to be absent in unauthenticated mode. The empty-
		// key case is reachable via the isLocalURL bypass above.
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", Usage{}, fmt.Errorf("call %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	// LimitReader at maxResponseBytes+1 so we can distinguish "exactly
	// at the cap" from "ran past the cap" — if we read >max bytes the
	// provider returned more than we'll trust.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", Usage{}, fmt.Errorf("read response: %w", err)
	}
	if int64(len(respBody)) > maxResponseBytes {
		return "", Usage{}, fmt.Errorf("llm %s: response exceeded %d-byte cap (possible runaway provider or spoofed endpoint)", c.BaseURL, maxResponseBytes)
	}

	if resp.StatusCode >= 400 {
		// Try to extract a useful message from the JSON error envelope
		var parsed response
		_ = json.Unmarshal(respBody, &parsed)
		if parsed.Error != nil {
			return "", Usage{}, fmt.Errorf("llm %s: %s", resp.Status, parsed.Error.Message)
		}
		return "", Usage{}, fmt.Errorf("llm %s: %s", resp.Status, string(respBody))
	}

	var out response
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", Usage{}, fmt.Errorf("parse response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", Usage{}, fmt.Errorf("no choices returned")
	}
	return out.Choices[0].Message.Content, out.Usage, nil
}
