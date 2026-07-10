package main

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/prompts"
)

func configCmd(sf *sharedFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Show resolved configuration",
		Long: `Display the resolved configuration after applying the cascade:
  1. Built-in defaults
  2. ~/.local-review.yml
  3. ./.local-review.yml (project root)
  4. CLI flags

This is useful for debugging configuration issues and understanding
which settings are being used.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			// Doc claims CLI flags are part of the cascade — apply them
			// before printing so what the user sees here matches what
			// `local-review review` would actually use. Pre-fix the
			// command silently dropped flag overrides on the floor.
			applyFlagsToConfig(&cfg, sf)

			maskSensitiveForDisplay(&cfg)

			enc := yaml.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent(2)
			if err := enc.Encode(cfg); err != nil {
				_ = enc.Close()
				return fmt.Errorf("encode config: %w", err)
			}
			// Close (not deferred) so a flush failure on a buffered
			// stdout (broken pipe, disk full, network FS write error)
			// surfaces as a non-zero exit instead of silently truncating
			// the YAML output and exiting 0.
			if err := enc.Close(); err != nil {
				return fmt.Errorf("flush config output: %w", err)
			}
			// Append the per-language prompt resolution as YAML
			// comments. Issue #55 acceptance: "config shows the
			// resolved prompt source (embedded vs override path)" —
			// a YAML round-trip alone wouldn't reveal that, since
			// PromptsConfig only carries inputs. The block below
			// shows what each language *actually* loaded.
			if err := printPromptResolution(cmd.OutOrStdout(), cfg); err != nil {
				return err
			}
			// And WHICH config files produced the values above — the
			// resolved dump alone can't answer "is my repo-level
			// .local-review.yml even being read, and was it trusted?"
			return printConfigSources(cmd.OutOrStdout())
		},
	}
}

// printPromptResolution writes a comment block listing each available
// language pack and where the resolver actually loaded it from. Lives
// after the main YAML dump so a `config | yq` pipeline can still
// parse the structured part; the comment lines are valid YAML
// (skipped by parsers) and human-readable.
func printPromptResolution(w io.Writer, cfg config.Config) error {
	langs, err := prompts.Available()
	if err != nil {
		// Available() failure means the embedded FS is broken —
		// surface but don't fail the whole command. Check the
		// write error: a broken pipe here is a real failure mode
		// (config | head -5) and silently dropping it would mask
		// truncated output, which codex caught in self-review.
		_, werr := fmt.Fprintf(w, "\n# (could not list available packs: %v)\n", err)
		return werr
	}
	sort.Strings(langs)

	if _, err := fmt.Fprintln(w, "\n# Resolved prompt sources:"); err != nil {
		return err
	}
	opts := prompts.ResolveOptions{
		PackDir: cfg.Prompts.PackDir,
		Prepend: cfg.Prompts.Prepend,
		Append:  cfg.Prompts.Append,
	}
	for _, lang := range langs {
		pack, err := prompts.Resolve(lang, opts)
		// Include the actual error text in the displayed source
		// when Resolve fails — codex flagged the prior "<resolve
		// error>" placeholder as unhelpful for debugging
		// misconfiguration ("which file? what error?").
		source := pack.Source
		if err != nil {
			source = fmt.Sprintf("<resolve error: %v>", err)
		}
		if _, err := fmt.Fprintf(w, "#   %-12s %s\n", lang, source); err != nil {
			return err
		}
	}
	return nil
}

// printConfigSources writes a comment block naming the file-backed
// cascade layers this invocation resolves — the same description
// config.Load merges from (config.DescribeSources), so this output
// cannot drift from real load behavior. Comment lines keep the
// overall output `yq`-parseable, matching printPromptResolution.
func printConfigSources(w io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		_, werr := fmt.Fprintf(w, "\n# (could not resolve config sources: %v)\n", err)
		return werr
	}
	if _, err := fmt.Fprintln(w, "\n# Config sources (cascade order; later layers override earlier):\n#   1. built-in defaults"); err != nil {
		return err
	}
	for i, src := range config.DescribeSources(config.FindRepoConfig(cwd)) {
		var state string
		switch {
		case src.Path == "" && src.Role == config.SourceRoleRepo:
			state = fmt.Sprintf("(none found walking up from %s)", cwd)
		case src.Path == "":
			state = "(could not resolve home directory)"
		case src.SameAsHome:
			state = fmt.Sprintf("%s — same file as the home config, merged once as trusted", src.Path)
		case !src.Found:
			state = fmt.Sprintf("%s (not found)", src.Path)
		case src.Trusted:
			state = fmt.Sprintf("%s loaded (trusted)", src.Path)
		default:
			state = fmt.Sprintf("%s loaded (untrusted: security-sensitive fields stripped; opt in with LOCAL_REVIEW_TRUST_REPO_CONFIG=1)", src.Path)
		}
		if _, err := fmt.Fprintf(w, "#   %d. %s  %s\n", i+2, src.Role, state); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(w, "#   4. CLI flags")
	return err
}

// maskSensitiveForDisplay masks API keys and strips credentials embedded
// in base_url values before printing — config dumps should be shareable.
// SanitizeBaseURLForDisplay covers basic-auth userinfo
// (`https://user:pass@host`) and query-string keys (`?api_key=…`). The v0
// `cfg.Provider` branch was removed in v0.15 along with the top-level
// `provider:` block; all endpoints now flow through `cfg.LLMs[*]`.
func maskSensitiveForDisplay(cfg *config.Config) {
	for name, llmCfg := range cfg.LLMs {
		if llmCfg.APIKey != "" {
			llmCfg.APIKey = "***"
		}
		if llmCfg.BaseURL != "" {
			llmCfg.BaseURL = config.SanitizeBaseURLForDisplay(llmCfg.BaseURL)
		}
		cfg.LLMs[name] = llmCfg
	}
}
