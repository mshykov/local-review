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
// versions. Each pattern is anchored strictly enough that lines like
// "Total tokens: 800" or "Available tokens: 800" elsewhere in the
// stdout banner (context-window indicators, not usage) don't
// false-positive — the v0.7.0 regex was permissive enough to do that,
// which surfaced nonsense like "codex ✓ · 800 total" on real runs.
//
//	"tokens: <in> input, <out> output"   — split shape, hypothetical/future
//	"tokens used\n<total>"               — codex v0.128.0+ (label and
//	                                       number on separate lines, no
//	                                       colon between them)
//	"tokens used: <total>"               — pre-v0.128 single-line legacy
//
// **Selection logic is latest-position-across-all-three patterns**, not
// first-match. See parseCodexStdoutTokens for the rationale (assistant
// prose can contain pattern-shaped text, so we can't trust the first
// match — only the rightmost match is guaranteed to be the real session
// summary). The split shape is what we'd want long-term but codex v0.128
// doesn't actually emit it; kept anyway for forward-compat.
var (
	codexSplitRE   = regexp.MustCompile(`(?i)\btokens:\s*(\d[\d,]*)\s+input\s*,\s*(\d[\d,]*)\s+output`)
	codexNewlineRE = regexp.MustCompile(`(?i)\btokens used\s*\r?\n\s*(\d[\d,]*)`)
	codexLegacyRE  = regexp.MustCompile(`(?i)\btokens used:\s*(\d[\d,]*)`)
)

// parseCodexStdoutTokens scans codex exec's combined stdout/stderr
// for the token-usage summary. Returns TokenUsage{} when no match —
// preferable to inventing numbers when the format changes again.
//
// `response` is the assistant's reply text (from the
// --output-last-message tempfile). Codex writes its stdout in this
// order: <assistant reply> → <real summary> → <duplicated reply>.
// The duplicated trailing reply is exactly the response-file contents.
// We strip it before scanning so pattern-shaped text inside that
// duplicate (e.g. the assistant quoted "tokens: 100 input, 20 output"
// from a test fixture in the diff) doesn't outrank the real summary
// via latest-position. Pass empty string to skip the strip — useful
// for tests that hand-build a stdout fixture.
//
// Split shape populates both Input and Output. The two single-total
// shapes (newline and legacy) fold their value into InputTokens and
// flag TotalOnly so display callers render "Nk total" rather than
// "Nk in / 0 out" — the latter would mislead users into thinking
// the model produced no output when really we just don't have the
// breakdown.
//
// **Latest match across ALL three patterns wins** (not first-pattern,
// not even last-of-first-matching-pattern). Pre-fix the v0.7.1 latest-
// position logic was the right shape but missed the trailing-duplicate
// case (above). Three failure modes pinned by tests:
//
//  1. "Same-pattern false positive" — assistant prose includes
//     "tokens used\n123" before the real "tokens used\n2,415"
//     summary. Solved by FindAll across each pattern.
//
//  2. "Cross-pattern false positive" — assistant prose includes
//     split-shape text "tokens: 100 input, 20 output" while the
//     real summary is newline-shape "tokens used\n2,415". v0.7.1:
//     collect candidates from all three patterns with positions,
//     pick greatest.
//
//  3. "Trailing-duplicate false positive" — assistant prose
//     containing pattern-shaped text appears BOTH before the real
//     summary AND in the duplicated trailing copy after it. v0.7.2:
//     strip the trailing duplicate using the known response text
//     before scanning, so latest-position lands on the real summary.
//
// The three patterns are mutually exclusive on a per-occurrence
// basis (different prefix words / punctuation), so two patterns
// can't both match the same byte range — position comparison is
// well-defined.
func parseCodexStdoutTokens(combined, response string) TokenUsage {
	combined = stripTrailingDuplicate(combined, response)

	type candidate struct {
		pos   int
		usage TokenUsage
	}
	var candidates []candidate

	for _, idx := range codexSplitRE.FindAllStringSubmatchIndex(combined, -1) {
		candidates = append(candidates, candidate{
			pos: idx[0],
			usage: TokenUsage{
				InputTokens:  atoiNoCommas(combined[idx[2]:idx[3]]),
				OutputTokens: atoiNoCommas(combined[idx[4]:idx[5]]),
			},
		})
	}
	for _, idx := range codexNewlineRE.FindAllStringSubmatchIndex(combined, -1) {
		candidates = append(candidates, candidate{
			pos: idx[0],
			usage: TokenUsage{
				InputTokens: atoiNoCommas(combined[idx[2]:idx[3]]),
				TotalOnly:   true,
			},
		})
	}
	for _, idx := range codexLegacyRE.FindAllStringSubmatchIndex(combined, -1) {
		candidates = append(candidates, candidate{
			pos: idx[0],
			usage: TokenUsage{
				InputTokens: atoiNoCommas(combined[idx[2]:idx[3]]),
				TotalOnly:   true,
			},
		})
	}

	if len(candidates) == 0 {
		return TokenUsage{}
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.pos > best.pos {
			best = c
		}
	}
	return best.usage
}

// stripTrailingDuplicate returns combined with any trailing copy of
// `response` removed. Codex exec writes the assistant reply twice —
// once during streaming and once at the end as the duplicate of the
// tempfile contents — so pattern-shaped text in the reply appears
// BOTH before and after the real "tokens used" summary. Removing the
// trailing duplicate restores the invariant that the real summary is
// the rightmost match.
//
// Empty response → no-op (callers in tests sometimes hand-build
// stdout fixtures without a separate response). Trimmed response
// not found in combined → no-op (codex format change; degrade
// gracefully rather than mangle).
func stripTrailingDuplicate(combined, response string) string {
	resp := strings.TrimSpace(response)
	if resp == "" {
		return combined
	}
	if i := strings.LastIndex(combined, resp); i != -1 {
		return combined[:i]
	}
	return combined
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
