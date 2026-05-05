package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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

			enc := yaml.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent(2)
			defer enc.Close()
			if err := enc.Encode(cfg); err != nil {
				return fmt.Errorf("encode config: %w", err)
			}
			return nil
		},
	}
}
