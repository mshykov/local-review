package cli

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// parseClaudeJSON unwraps the JSON object claude --output-format json
// returns. Anthropic's documented shape (claude-code v1+):
//
//	{
//	  "type": "result",
//	  "subtype": "success",
//	  "result": "<the assistant's reply>",
//	  "usage": {
//	    "input_tokens": N,
//	    "output_tokens": M,
//	    "cache_read_input_tokens": ...,
//	    "cache_creation_input_tokens": ...
//	  },
//	  ...
//	}
//
// Falls back to the raw output as text + zero usage when:
//   - The output isn't valid JSON (older CLI without --output-format).
//   - The "result" field is missing (different shape).
//
// Cache-read/creation tokens are NOT included in the input count we
// surface — those represent reuse, not new spend. If we're going to
// ever expose them, it should be as a separate "cached" field in
// TokenUsage.
func parseClaudeJSON(output []byte) (string, TokenUsage) {
	var resp struct {
		Type   string `json:"type"`
		Result string `json:"result"`
		Usage  struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(output, &resp); err != nil || resp.Result == "" {
		return string(output), TokenUsage{}
	}
	return resp.Result, TokenUsage{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
	}
}

// parseGeminiJSON unwraps the JSON object gemini -o json returns.
// Google's CLI doesn't document a stable shape across versions, so
// we try the two shapes that have appeared in the wild:
//
//	Shape A (newer): {"response": "...", "stats": {"models": {"<id>": {"tokens": {"prompt": N, "candidates": M, "total": ...}}}}}
//	Shape B (older): {"text": "...", "usageMetadata": {"promptTokenCount": N, "candidatesTokenCount": M}}
//
// First-match wins. If neither shape parses, fall back to raw text +
// zero usage. The variability is the price of using a CLI Google
// hasn't pinned to a stable contract.
func parseGeminiJSON(output []byte) (string, TokenUsage) {
	// Shape A: the structure recent gemini-cli uses.
	var shapeA struct {
		Response string `json:"response"`
		Stats    struct {
			Models map[string]struct {
				Tokens struct {
					Prompt     int `json:"prompt"`
					Candidates int `json:"candidates"`
				} `json:"tokens"`
			} `json:"models"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(output, &shapeA); err == nil && shapeA.Response != "" {
		// Sum tokens across all models reported (typically one).
		var in, out int
		for _, m := range shapeA.Stats.Models {
			in += m.Tokens.Prompt
			out += m.Tokens.Candidates
		}
		return shapeA.Response, TokenUsage{InputTokens: in, OutputTokens: out}
	}

	// Shape B: older Vertex-style usageMetadata.
	var shapeB struct {
		Text          string `json:"text"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(output, &shapeB); err == nil && shapeB.Text != "" {
		return shapeB.Text, TokenUsage{
			InputTokens:  shapeB.UsageMetadata.PromptTokenCount,
			OutputTokens: shapeB.UsageMetadata.CandidatesTokenCount,
		}
	}

	// Neither shape: not valid JSON or different structure. Fall
	// through to text — at worst the user loses token visibility
	// for gemini, never the review itself.
	return string(output), TokenUsage{}
}

// codexTokensRE captures the input/output token counts from the
// session-metadata block codex exec writes to stdout (intermixed
// with the assistant's reply, which is why we route the reply
// through --output-last-message). The pattern handles both shapes
// codex has used across recent versions:
//
//	"tokens used: <total>"            (just total, before v0.120)
//	"tokens: <in> input, <out> output" (split, v0.128+)
//
// Matches case-insensitive because codex's banner capitalisation
// has drifted historically.
var codexTokensRE = regexp.MustCompile(`(?i)tokens(?:\s+used)?:\s*(\d[\d,]*)(?:\s+input(?:\s*,\s*(\d[\d,]*)\s+output)?)?`)

// parseCodexStdoutTokens scans codex exec's combined stdout/stderr
// for the token-usage line. Returns TokenUsage{} when no match —
// preferable to inventing numbers when the format changes.
//
// The split-counts shape (v0.128+) populates both Input and Output;
// the legacy single-total shape (older) populates only Total via
// InputTokens — we don't have enough signal to attribute split, so
// reporting "all input" is a deliberate under-report on the output
// side rather than a fabricated split.
func parseCodexStdoutTokens(combined string) TokenUsage {
	m := codexTokensRE.FindStringSubmatch(combined)
	if m == nil {
		return TokenUsage{}
	}
	first := atoiNoCommas(m[1])
	if len(m) >= 3 && m[2] != "" {
		// Split shape: m[1] = input, m[2] = output.
		return TokenUsage{InputTokens: first, OutputTokens: atoiNoCommas(m[2])}
	}
	// Legacy single-total shape — fold into InputTokens since we
	// can't attribute. Underestimates output but doesn't fabricate.
	return TokenUsage{InputTokens: first}
}

// atoiNoCommas parses an integer that may have thousand separators
// like "12,345". Returns 0 on parse failure so callers don't have
// to thread errors through the regex path.
func atoiNoCommas(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
