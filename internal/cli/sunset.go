package cli

import (
	"math"
	"time"
)

// GeminiSunsetDate is the day after which Google stops serving the
// Gemini CLI's Pro / Ultra / free-tier requests. Announced 2026-04 as
// part of the Antigravity (`agy`) succession.
//
// Hard-coded constant so the auto-disable behaviour is auditable
// without scraping a remote URL at startup (privacy / offline-first
// constraint) and survives a long-lived binary install across the
// cutoff. When Google moves the date, bump this constant.
//
// UTC midnight. The sunset is treated as "any moment ON or AFTER this
// instant" — i.e. once `time.Now().UTC()` is at-or-past this point,
// gemini is treated as removed (unless force_after_sunset is set; see
// LLMConfig.ForceAfterSunset).
var GeminiSunsetDate = time.Date(2026, time.June, 18, 0, 0, 0, 0, time.UTC)

// AgentSunsetDate returns the manufacturer-announced sunset date for
// an agent, or the zero time when no sunset is known. Today only
// gemini has one; this indirection exists so adding a future sunset
// (or removing gemini's once the cutoff passes) is a one-line edit
// here instead of a grep across detector + doctor + runner.
func AgentSunsetDate(name string) time.Time {
	if name == "gemini" {
		return GeminiSunsetDate
	}
	return time.Time{}
}

// IsAgentSunset reports whether the agent's sunset date has passed
// relative to `now`. `now` is a parameter so tests can inject a
// fixed clock — production callers pass `time.Now().UTC()`.
//
// Returns false for agents with no sunset (every agent except gemini
// today).
func IsAgentSunset(name string, now time.Time) bool {
	sunset := AgentSunsetDate(name)
	if sunset.IsZero() {
		return false
	}
	// at-or-after: a sunset that lands on "today" counts as sunset
	// today, not tomorrow. Matches the user-facing phrasing on the
	// doctor banner ("stops serving 2026-06-18").
	return !now.Before(sunset)
}

// DaysUntilAgentSunset returns the integer number of days between
// `now` and the agent's sunset date, using *ceiling* semantics for
// positive durations: any remaining time before the sunset rounds
// up to at least 1, so the doctor countdown never reads "0 days
// until sunset" while time still remains. Once `now` is at or past
// the sunset (IsAgentSunset == true), the return value is 0 or
// negative — caller uses IsAgentSunset to switch banner modes, this
// function just supplies the magnitude.
//
// Pre-v0.15 the math was a floor (`int(hours / 24)`), which read
// like a boundary bug for the last ~24h before cutoff. Self-review
// caught this on PR 2/4.
//
// Returns noSunsetSentinel (a distinctly impossible value) when the
// agent has no sunset; callers can use IsAgentSunset or
// AgentSunsetDate to distinguish "no sunset known" from
// "sunset just happened" if they need to.
func DaysUntilAgentSunset(name string, now time.Time) int {
	sunset := AgentSunsetDate(name)
	if sunset.IsZero() {
		return noSunsetSentinel
	}
	hoursRemaining := sunset.Sub(now).Hours()
	if hoursRemaining <= 0 {
		// At or past sunset. Go's `int(x)` truncates toward zero, so
		// negative hoursRemaining naturally floors toward zero too —
		// hoursRemaining=-24 → -1, hoursRemaining=0 → 0. That matches
		// the contract ("0 = today is the sunset; -1 = yesterday was").
		return int(hoursRemaining / 24)
	}
	// Pre-sunset: round UP so any remaining time reads as ≥1 day.
	// "1 day until sunset" with 30 min left is unambiguous;
	// "0 days until sunset" while still pre-sunset is what the
	// self-review caught as confusing.
	return int(math.Ceil(hoursRemaining / 24))
}

// noSunsetSentinel is the DaysUntilAgentSunset return value when no
// sunset date applies. A distinct value (not 0, not -1) makes the
// "no sunset known" case unambiguous to callers — 0 means "today is
// the sunset" and -1 means "yesterday was".
const noSunsetSentinel = -1 << 30
