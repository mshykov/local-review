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
//	    "input_tokens": N,                  // new (uncached) input
//	    "output_tokens": M,
//	    "cache_read_input_tokens": ...,     // re-used from prompt cache
//	    "cache_creation_input_tokens": ...  // newly added to cache
//	  },
//	  ...
//	}
//
// Falls back to the raw output as text + zero usage when valid JSON
// has an unexpected shape (e.g., a future schema or an error
// envelope). This is NOT a path to support older CLIs lacking
// --output-format — those exit non-zero and never reach this parser;
// see ClaudeInvoker.run for the version-baseline rationale.
//
// **Cache-read and cache-creation tokens are summed into InputTokens.**
// Pre-v0.7.1 we excluded them on the theory that "they represent reuse,
// not new spend" — but in practice that meant the displayed input was
// only the *novel* portion of a cached prompt. On a re-review of the
// same diff the displayed value collapsed to single digits ("9 in /
// 5.2k out"), which read as broken. The user-visible "in" should
// answer "how big was the prompt I sent?" — that's the sum of all
// three input components. Cost accounting (where cache reads are
// discounted ~10x) is not what this number is for; that lives in
// the vendor's billing dashboard.
func parseClaudeJSON(output []byte) (string, TokenUsage) {
	var resp struct {
		Type   string  `json:"type"`
		Result *string `json:"result"`
		Usage  *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(output, &resp); err != nil || resp.Result == nil {
		return string(output), TokenUsage{}
	}
	usage := TokenUsage{}
	if resp.Usage != nil {
		usage.InputTokens = resp.Usage.InputTokens +
			resp.Usage.CacheReadInputTokens +
			resp.Usage.CacheCreationInputTokens
		usage.OutputTokens = resp.Usage.OutputTokens
	}
	return *resp.Result, usage
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
		Response *string `json:"response"`
		Stats    struct {
			Models map[string]struct {
				Tokens struct {
					Prompt     int `json:"prompt"`
					Candidates int `json:"candidates"`
				} `json:"tokens"`
			} `json:"models"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(output, &shapeA); err == nil && shapeA.Response != nil {
		// Sum tokens across all models reported (typically one).
		var in, out int
		for _, m := range shapeA.Stats.Models {
			in += m.Tokens.Prompt
			out += m.Tokens.Candidates
		}
		return *shapeA.Response, TokenUsage{InputTokens: in, OutputTokens: out}
	}

	// Shape B: older Vertex-style usageMetadata.
	var shapeB struct {
		Text          *string `json:"text"`
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(output, &shapeB); err == nil && shapeB.Text != nil {
		usage := TokenUsage{}
		if shapeB.UsageMetadata != nil {
			usage.InputTokens = shapeB.UsageMetadata.PromptTokenCount
			usage.OutputTokens = shapeB.UsageMetadata.CandidatesTokenCount
		}
		return *shapeB.Text, usage
	}

	// Neither shape: not valid JSON or different structure. Fall
	// through to text — at worst the user loses token visibility
	// for gemini, never the review itself.
	return string(output), TokenUsage{}
}

// Codex stdout has used three different token-summary shapes across
// versions. We try each in order and stop at the first match. Each
// pattern is anchored strictly enough that lines like "Total tokens:
// 800" or "Available tokens: 800" elsewhere in the stdout banner
// (context-window indicators, not usage) don't false-positive — the
// v0.7.0 regex was permissive enough to do that, which surfaced
// nonsense like "codex ✓ · 800 total" on real runs.
//
//	"tokens: <in> input, <out> output"   — split shape, hypothetical/future
//	"tokens used\n<total>"               — codex v0.128.0+ (label and
//	                                       number on separate lines, no
//	                                       colon between them)
//	"tokens used: <total>"               — pre-v0.128 single-line legacy
//
// Patterns ordered most-specific → least-specific. The split shape is
// what we'd want long-term but codex v0.128 doesn't actually emit it;
// kept anyway for forward-compat — if/when codex starts splitting,
// we don't lose the signal.
var (
	codexSplitRE   = regexp.MustCompile(`(?i)\btokens:\s*(\d[\d,]*)\s+input\s*,\s*(\d[\d,]*)\s+output`)
	codexNewlineRE = regexp.MustCompile(`(?i)\btokens used\s*\r?\n\s*(\d[\d,]*)`)
	codexLegacyRE  = regexp.MustCompile(`(?i)\btokens used:\s*(\d[\d,]*)`)
)

// parseCodexStdoutTokens scans codex exec's combined stdout/stderr
// for the token-usage summary. Returns TokenUsage{} when no match —
// preferable to inventing numbers when the format changes again.
//
// Split shape populates both Input and Output. The two single-total
// shapes (newline and legacy) fold their value into InputTokens and
// flag TotalOnly so display callers render "Nk total" rather than
// "Nk in / 0 out" — the latter would mislead users into thinking
// the model produced no output when really we just don't have the
// breakdown.
func parseCodexStdoutTokens(combined string) TokenUsage {
	if m := codexSplitRE.FindStringSubmatch(combined); m != nil {
		return TokenUsage{
			InputTokens:  atoiNoCommas(m[1]),
			OutputTokens: atoiNoCommas(m[2]),
		}
	}
	if m := codexNewlineRE.FindStringSubmatch(combined); m != nil {
		return TokenUsage{InputTokens: atoiNoCommas(m[1]), TotalOnly: true}
	}
	if m := codexLegacyRE.FindStringSubmatch(combined); m != nil {
		return TokenUsage{InputTokens: atoiNoCommas(m[1]), TotalOnly: true}
	}
	return TokenUsage{}
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
