package main

import (
	"strings"
	"testing"

	"github.com/mshykov/local-review/internal/cli"
)

// selectAuditLLM is the only audit-internal seam users can change
// behaviour through (the rest is delegated to pickAgents +
// internal/audit). These tests pin the contract that PR 4/5 promised:
// `--with` is exact-match, defaults to "first active" on empty, and
// fails closed with an actionable error otherwise — no silent
// fall-through to a different agent than the user asked for.

func TestSelectAuditLLM_EmptyActive_ReturnsHintError(t *testing.T) {
	_, err := selectAuditLLM(nil, "")
	if err == nil {
		t.Fatal("expected error when active set is empty, got nil")
	}
	if !strings.Contains(err.Error(), "doctor") {
		t.Errorf("error should hint at `doctor`; got %q", err)
	}
}

func TestSelectAuditLLM_NoWith_PicksFirst(t *testing.T) {
	active := []cli.LLM{
		{Name: "claude"},
		{Name: "codex"},
	}
	got, err := selectAuditLLM(active, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "claude" {
		t.Errorf("want claude (first), got %q", got.Name)
	}
}

func TestSelectAuditLLM_WithExactMatch_CLIAgent(t *testing.T) {
	active := []cli.LLM{
		{Name: "claude"},
		{Name: "codex"},
	}
	got, err := selectAuditLLM(active, "codex")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "codex" {
		t.Errorf("want codex, got %q", got.Name)
	}
}

func TestSelectAuditLLM_WithExactMatch_ProviderAgent(t *testing.T) {
	// Provider agents carry a BaseURL; the selector must not care
	// about kind — `qwen` is just a name. This test exists so a
	// future selector that grows kind-aware logic doesn't quietly
	// drop providers from `--with`'s reach.
	active := []cli.LLM{
		{Name: "claude"},
		{Name: "qwen", BaseURL: "http://192.0.2.10:11434/v1"},
	}
	got, err := selectAuditLLM(active, "qwen")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "qwen" {
		t.Errorf("want qwen, got %q", got.Name)
	}
	if got.BaseURL == "" {
		t.Error("BaseURL must round-trip — audit dispatcher reads it to route to provider.Invoker")
	}
}

func TestSelectAuditLLM_WithUnknown_ListsCandidates(t *testing.T) {
	active := []cli.LLM{
		{Name: "claude"},
		{Name: "codex"},
	}
	_, err := selectAuditLLM(active, "qwen")
	if err == nil {
		t.Fatal("expected error when --with names an unauthenticated agent")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"qwen"`) {
		t.Errorf("error should echo the bad name; got %q", msg)
	}
	if !strings.Contains(msg, "claude") || !strings.Contains(msg, "codex") {
		t.Errorf("error should list the ready candidates so the user can fix the typo without rerunning doctor; got %q", msg)
	}
}

func TestSelectAuditLLM_WithRespectsOnlyFilter(t *testing.T) {
	// Documents the composition rule: --only filters upstream
	// (pickAgents), --with picks within the filtered set. When
	// --only excludes the target, --with fails with the
	// candidates list — not by silently falling back to the first
	// authenticated agent.
	active := []cli.LLM{{Name: "codex"}} // simulating --only codex
	_, err := selectAuditLLM(active, "claude")
	if err == nil {
		t.Fatal("expected error: claude was excluded by --only")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Errorf("error should show codex as the only candidate; got %q", err)
	}
}
