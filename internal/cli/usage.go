package cli

import "github.com/mshykov/local-review/internal/agents"

// TokenUsage is an alias for agents.TokenUsage. The type was moved to
// internal/agents in v0.14 so the HTTP provider invoker
// (internal/agents/provider) could share the same shape without
// depending on this CLI-subprocess package. CLI callers continue to
// use cli.TokenUsage unchanged via this alias; the methods
// (IsZero, Total) come along with the alias automatically. See
// agents.TokenUsage for the full doc.
type TokenUsage = agents.TokenUsage
