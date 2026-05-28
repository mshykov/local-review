package cli

import (
	"testing"
	"time"
)

// These tests pin the pure clock-driven sunset machinery: the date
// constant, the at-or-after semantics, the days-remaining math, and
// the "agents without a sunset return false / sentinel" no-op shape.
// Higher layers (runner pickAgents, doctor banner) are tested
// separately against their own injected clocks.

func TestGeminiSunsetDate_IsCorrect(t *testing.T) {
	// Hard-coded pin so a future contributor who edits the constant
	// (e.g. Google extends the cutoff) updates this test alongside
	// — keeps the announced date provable from the codebase.
	want := time.Date(2026, time.June, 18, 0, 0, 0, 0, time.UTC)
	if !GeminiSunsetDate.Equal(want) {
		t.Errorf("GeminiSunsetDate = %s, want %s", GeminiSunsetDate, want)
	}
}

func TestAgentSunsetDate(t *testing.T) {
	cases := map[string]time.Time{
		"gemini":      GeminiSunsetDate,
		"claude":      {},
		"codex":       {},
		"copilot":     {},
		"antigravity": {},
		"unknown":     {},
		"":            {},
	}
	for name, want := range cases {
		got := AgentSunsetDate(name)
		if !got.Equal(want) {
			t.Errorf("AgentSunsetDate(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestIsAgentSunset_BeforeAfterAndAtDate(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		// Day before sunset → not sunset yet.
		{"gemini", time.Date(2026, 6, 17, 23, 59, 59, 0, time.UTC), false},
		// Exactly at sunset → sunset (at-or-after semantics).
		{"gemini", time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC), true},
		// Day after sunset → sunset.
		{"gemini", time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC), true},
		// One year in the past → not sunset (sanity).
		{"gemini", time.Date(2025, 6, 18, 0, 0, 0, 0, time.UTC), false},
		// Agents with no sunset always return false.
		{"claude", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{"codex", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{"copilot", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{"unknown", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), false},
	}
	for _, tc := range cases {
		if got := IsAgentSunset(tc.name, tc.now); got != tc.want {
			t.Errorf("IsAgentSunset(%q, %s) = %v, want %v", tc.name, tc.now, got, tc.want)
		}
	}
}

func TestDaysUntilAgentSunset(t *testing.T) {
	// v0.15: positive durations use CEILING semantics so the doctor
	// banner never reads "0 days until sunset" while time still
	// remains. 2026-05-30 12:00 UTC → 2026-06-18 00:00 UTC =
	// 18.5 days; ceil(18.5) = 19.
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	want := 19
	if got := DaysUntilAgentSunset("gemini", now); got != want {
		t.Errorf("DaysUntilAgentSunset(gemini, %s) = %d, want %d (ceiling)", now, got, want)
	}
	// At the sunset instant → 0 (the "today is the sunset" anchor).
	if got := DaysUntilAgentSunset("gemini", GeminiSunsetDate); got != 0 {
		t.Errorf("DaysUntilAgentSunset on sunset midnight = %d, want 0", got)
	}
	// One full day after → -1.
	if got := DaysUntilAgentSunset("gemini", GeminiSunsetDate.Add(24*time.Hour)); got != -1 {
		t.Errorf("DaysUntilAgentSunset day after = %d, want -1", got)
	}
}

func TestDaysUntilAgentSunset_CeilingForPartialFinalDay(t *testing.T) {
	// The bug the v0.15 self-review caught (codex + copilot): pre-
	// fix the math was floor(`hours / 24`), which read "0 days
	// until sunset" for the entire 24 hours before cutoff. That
	// landed like a boundary bug. With ceiling-for-positive
	// semantics, any partial day still pre-sunset must read ≥1.
	cases := []struct {
		label string
		now   time.Time
		want  int
	}{
		// 23:59:59 the day before sunset — under 1 minute remaining.
		{"1 minute before cutoff", time.Date(2026, 6, 17, 23, 59, 0, 0, time.UTC), 1},
		// Half a day before sunset — 12h.
		{"12 hours before cutoff", time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC), 1},
		// One full day before.
		{"24 hours before cutoff", time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC), 1},
		// 25 hours before — ceils to 2.
		{"25 hours before cutoff", time.Date(2026, 6, 16, 23, 0, 0, 0, time.UTC), 2},
	}
	for _, tc := range cases {
		if got := DaysUntilAgentSunset("gemini", tc.now); got != tc.want {
			t.Errorf("%s: DaysUntilAgentSunset(gemini, %s) = %d, want %d", tc.label, tc.now, got, tc.want)
		}
	}
}

func TestDaysUntilAgentSunset_NoSunsetReturnsSentinel(t *testing.T) {
	// Agents with no announced sunset must return the distinct
	// noSunsetSentinel, NOT 0 (which would mean "today is the
	// sunset") and NOT -1 (which would mean "yesterday was").
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := DaysUntilAgentSunset("claude", now)
	if got >= -365 && got <= 365 {
		t.Errorf("DaysUntilAgentSunset for no-sunset agent returned a plausible day count %d — must be the sentinel so callers can distinguish 'no sunset known' from 'sunset in the past/future'", got)
	}
}
