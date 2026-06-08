package cli

import (
	"context"
	"sync"
	"time"

	"github.com/mshykov/local-review/internal/agents/provider"
)

// ProviderSpec is the minimal config-derived shape DetectProviders
// needs per agent. The caller (cmd/local-review's pickAgents) builds
// these from cfg.LLMs[name] entries that have a BaseURL — that's the
// kind discriminator. APIKey is the already-resolved key (from
// config.resolveAPIKeys' env-var lookup); empty means "no auth
// configured" which the HTTP layer's isLocalURL bypass tolerates for
// local-or-LAN endpoints.
//
// Why a separate spec type (vs taking config.Config directly): keeps
// this package free of an import on internal/config and matches the
// shape DetectAllWithOverrides already uses — caller owns the config
// vocabulary, this package owns the runtime-agent vocabulary.
type ProviderSpec struct {
	Name    string
	BaseURL string
	Model   string
	APIKey  string
	// APIKeyEnv is the NAME of the env var APIKey was resolved from
	// (cfg.LLMs[name].APIKeyEnv). Carried through so an auth-miss error
	// can name the variable the user configured. Empty for keyless
	// (local/LAN) providers.
	APIKeyEnv  string
	TimeoutSec int
}

// providerProbeTimeout is the per-endpoint cap when DetectProviders
// fans out. Short — readiness should be a sub-second answer for a
// reachable endpoint, and a too-long cap means doctor stalls on a
// down provider. Independent of the per-call review timeout.
const providerProbeTimeout = 3 * time.Second

// DetectProviders runs an HTTP readiness probe per provider spec and
// returns one LLM per spec with Available set according to the probe
// outcome. Probes run in parallel — the same way DetectAll fans out
// the CLI version probes — so N providers don't serialise into N
// timeouts.
//
// Available reflects ONLY endpoint reachability + auth-acceptance at
// /v1/models. It does NOT confirm the configured Model is loaded —
// that's the job of the strict probe (PR 3 wires --strict-probe), the
// pre-flight in the runner, or, ultimately, the real review call.
//
// Returns an empty slice when specs is empty; never returns nil.
func DetectProviders(ctx context.Context, specs []ProviderSpec) []LLM {
	out := make([]LLM, len(specs))
	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Add(1)
		go func(idx int, spec ProviderSpec) {
			defer wg.Done()
			available := provider.Probe(ctx, spec.BaseURL, spec.APIKey, providerProbeTimeout) == nil
			out[idx] = LLM{
				Name:       spec.Name,
				BaseURL:    spec.BaseURL,
				Model:      spec.Model,
				APIKey:     spec.APIKey,
				APIKeyEnv:  spec.APIKeyEnv,
				TimeoutSec: spec.TimeoutSec,
				Available:  available,
				// Version stays empty for providers — there's no
				// equivalent of "claude-code v2.1.x" here; the Model
				// id is the closest analogue and lives in the dedicated
				// Model field, rendered separately by doctor.
			}
		}(i, s)
	}
	wg.Wait()
	return out
}
