package main

import (
	"fmt"
	"io"
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

			// Mask API keys before printing (config dumps should be shareable)
			if cfg.Provider.APIKey != "" {
				cfg.Provider.APIKey = "***"
			}
			for name, llmCfg := range cfg.LLMs {
				if llmCfg.APIKey != "" {
					llmCfg.APIKey = "***"
					cfg.LLMs[name] = llmCfg
				}
			}

			// Strip credentials embedded in base_url values
			// (`https://user:pass@host` or `?api_key=…`). A config dump
			// is meant to be shareable like the masked api_key above;
			// without this, basic-auth or query-string creds leak
			// verbatim. Same sanitizer as the v0.14 deprecation
			// warning so both surfaces behave identically.
			if cfg.Provider.BaseURL != "" {
				cfg.Provider.BaseURL = config.SanitizeBaseURLForDisplay(cfg.Provider.BaseURL)
			}
			for name, llmCfg := range cfg.LLMs {
				if llmCfg.BaseURL != "" {
					llmCfg.BaseURL = config.SanitizeBaseURLForDisplay(llmCfg.BaseURL)
					cfg.LLMs[name] = llmCfg
				}
			}

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
			return printPromptResolution(cmd.OutOrStdout(), cfg)
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
