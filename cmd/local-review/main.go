// local-review: a local, BYOK code reviewer that runs against a git diff.
//
// Usage:
//
//	local-review staged                  # review staged changes (pre-commit hook)
//	local-review commit [<rev>]          # review a single commit (default: HEAD)
//	local-review branch [<base>]         # review the current branch vs <base> (default: main)
//
// Configuration cascades: built-in defaults → ~/.local-review.yml → ./.local-review.yml → CLI flags.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/git"
	"github.com/mshykov/local-review/internal/output"
	"github.com/mshykov/local-review/internal/review"
)

// flags shared across all review subcommands
type sharedFlags struct {
	model       string
	baseURL     string
	minSeverity string
	maxFindings int
	jsonOut     bool
}

func main() {
	var sf sharedFlags

	root := &cobra.Command{
		Use:   "local-review",
		Short: "AI code review for your local diff. BYOK, language-agnostic.",
		Long: `local-review reviews a git diff with an LLM of your choice and reports findings.

It runs entirely on your machine. The only network call is to whichever
chat-completions endpoint you configure. Works with any OpenAI-compatible
provider: OpenAI, Anthropic, Mistral, DeepSeek, Together, Groq, OpenRouter,
Ollama (fully offline), vLLM, etc.

First-time setup:

  local-review init             # interactive — picks a provider, writes .local-review.yml
  export OPENAI_API_KEY=...     # init prints which env var to set
  local-review staged           # review staged changes

Multi-LLM mode (runs installed LLM CLIs in parallel, merges findings):

  local-review doctor           # check which LLM CLIs are installed/authenticated
  local-review multi staged

Configure manually in ~/.local-review.yml or ./.local-review.yml.
See README and https://mshykov.github.io/local-review/ for details.`,
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVar(&sf.model, "model", "", "override provider.model")
	root.PersistentFlags().StringVar(&sf.baseURL, "base-url", "", "override provider.base_url")
	root.PersistentFlags().StringVar(&sf.minSeverity, "min-severity", "", "filter findings: nit|info|warning|major|critical")
	root.PersistentFlags().IntVar(&sf.maxFindings, "max-findings", 0, "cap total findings shown")
	root.PersistentFlags().BoolVar(&sf.jsonOut, "json", false, "emit JSON instead of human-readable text")

	root.AddCommand(stagedCmd(&sf))
	root.AddCommand(commitCmd(&sf))
	root.AddCommand(branchCmd(&sf))
	root.AddCommand(versionCmd())
	root.AddCommand(configCmd())
	root.AddCommand(doctorCmd())
	root.AddCommand(initCmd())
	root.AddCommand(multiCmd(&sf))

	if err := root.Execute(); err != nil {
		// cobra already printed the error; just set the exit code
		os.Exit(1)
	}
}

func stagedCmd(sf *sharedFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "staged",
		Short: "Review what would be committed next (git diff --cached)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReview(cmd.Context(), sf, git.ModeStaged, "")
		},
	}
}

func commitCmd(sf *sharedFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "commit [<rev>]",
		Short: "Review a single commit (default: HEAD)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			return runReview(cmd.Context(), sf, git.ModeCommit, ref)
		},
	}
}

func branchCmd(sf *sharedFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "branch [<base>]",
		Short: "Review the current branch against <base> (default: main)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base := ""
			if len(args) == 1 {
				base = args[0]
			}
			return runReview(cmd.Context(), sf, git.ModeBranch, base)
		},
	}
}

// loadConfig finds + loads the cascade.
func loadConfig() (config.Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return config.Config{}, err
	}
	repoCfg := config.FindRepoConfig(cwd)
	return config.Load(repoCfg)
}

func runReview(ctx context.Context, sf *sharedFlags, mode git.Mode, ref string) error {
	// Trap Ctrl-C so an in-flight LLM call can be cancelled.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	applyFlags(&cfg, sf)

	r := review.New(cfg)
	rep, err := r.Run(ctx, mode, ref)
	if err != nil {
		return err
	}

	if sf.jsonOut {
		return output.WriteJSON(os.Stdout, rep)
	}
	output.WriteText(os.Stdout, rep)

	// Exit non-zero when blocking-severity findings exist, so pre-commit
	// hooks fail the commit. "major" and "critical" block by default.
	if hasBlocking(rep) {
		os.Exit(2)
	}
	return nil
}

func applyFlags(cfg *config.Config, sf *sharedFlags) {
	if sf.model != "" {
		cfg.Provider.Model = sf.model
	}
	if sf.baseURL != "" {
		cfg.Provider.BaseURL = sf.baseURL
	}
	if sf.minSeverity != "" {
		cfg.Review.MinSeverity = sf.minSeverity
	}
	if sf.maxFindings != 0 {
		cfg.Review.MaxFindings = sf.maxFindings
	}
}

func hasBlocking(r review.Report) bool {
	for _, f := range r.Findings {
		if f.Severity >= review.SeverityMajor {
			return true
		}
	}
	return false
}
