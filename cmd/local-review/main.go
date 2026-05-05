// local-review: a local, BYOK code reviewer that runs against a git diff.
//
// Usage:
//
//	local-review review                  # current branch vs main, all active LLMs (default)
//	local-review staged                  # review staged changes
//	local-review commit [<rev>]          # review a single commit (default: HEAD)
//	local-review branch [<base>]         # review the current branch vs <base>
//
// Configuration cascades: built-in defaults → ~/.local-review.yml → ./.local-review.yml → CLI flags.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/git"
)

// banner is the figlet Block-font "LOCAL-REVIEW" art shown atop --help.
// It's ~120 columns wide and only renders in helpHeader() when stdout
// is a real TTY ≥ 100 cols; CI logs and narrow tmux panes get a clean
// text-only header instead.
const banner = `
  _|          _|_|      _|_|_|    _|_|    _|              _|_|_|    _|_|_|_|  _|      _|  _|_|_|  _|_|_|_|  _|          _|
  _|        _|    _|  _|        _|    _|  _|              _|    _|  _|        _|      _|    _|    _|        _|          _|
  _|        _|    _|  _|        _|_|_|_|  _|  _|_|_|_|_|  _|_|_|    _|_|_|    _|      _|    _|    _|_|_|    _|    _|    _|
  _|        _|    _|  _|        _|    _|  _|              _|    _|  _|          _|  _|      _|    _|          _|  _|  _|
  _|_|_|_|    _|_|      _|_|_|  _|    _|  _|_|_|_|        _|    _|  _|_|_|_|      _|      _|_|_|  _|_|_|_|      _|  _|
`

// helpHeader returns the banner when stdout looks like a wide-enough
// terminal, or an empty string otherwise. Cobra hard-wraps Long
// descriptions; the banner is unreadable noise on <100-col terminals
// (CI logs, narrow tmux panes, the editor that opens for `git commit`).
//
// Detection is intentionally stdlib-only: stat the file mode to tell
// terminals from pipes/files, and read $COLUMNS for width. This avoids
// pulling in golang.org/x/term and keeps the Go 1.23 floor.
func helpHeader() string {
	info, err := os.Stdout.Stat()
	if err != nil || (info.Mode()&os.ModeCharDevice) == 0 {
		return ""
	}
	cols, err := strconv.Atoi(os.Getenv("COLUMNS"))
	if err != nil || cols < 100 {
		return ""
	}
	return banner + "\n"
}

// sharedFlags collects every flag accepted by the review-shape commands.
// Single-LLM-fallback flags (--model, --base-url) and multi-LLM flags
// (--only, --<agent>-model) coexist: which one applies depends on
// whether any LLM CLI is active at runtime.
type sharedFlags struct {
	// v0 single-LLM-API fallback flags
	model   string
	baseURL string

	// shared review-tuning flags
	minSeverity string
	maxFindings int
	jsonOut     bool

	// multi-LLM flags
	only        string // comma-separated agent names to restrict the run to
	claudeModel string
	geminiModel string
	codexModel  string
	mergeWith   string
}

func main() {
	var sf sharedFlags

	root := &cobra.Command{
		Use:   "local-review",
		Short: "AI code review for your local diff. BYOK, language-agnostic.",
		Long: helpHeader() + `local-review reviews a git diff with the LLMs you have installed and
runs them in parallel. It runs entirely on your machine; the only
network call is to whichever LLM endpoint you configured.

Quick start:

  local-review init             # interactive — picks a provider, writes .local-review.yml
  local-review doctor           # check which LLM CLIs are installed/authenticated
  local-review review           # review current branch with every active LLM

By default, every LLM CLI that is both installed AND authenticated runs
in parallel and the findings are merged into one report. Use ~/.local-review.yml
or ./.local-review.yml to override; CLI flags override config.

If no LLM CLI is active, falls back to the configured 'provider:' (any
OpenAI-compatible endpoint: OpenAI, Anthropic, Mistral, DeepSeek,
Together, Groq, OpenRouter, Ollama, vLLM, etc.).

See README and https://mshykov.github.io/local-review/ for details.`,
		SilenceUsage: true,
	}

	// review-tuning (apply to all review-shape commands)
	root.PersistentFlags().StringVar(&sf.minSeverity, "min-severity", "", "filter findings: nit|info|warning|major|critical")
	root.PersistentFlags().IntVar(&sf.maxFindings, "max-findings", 0, "cap total findings shown")
	root.PersistentFlags().BoolVar(&sf.jsonOut, "json", false, "emit JSON instead of human-readable text")

	// single-LLM-fallback flags
	root.PersistentFlags().StringVar(&sf.model, "model", "", "override provider.model (single-LLM fallback)")
	root.PersistentFlags().StringVar(&sf.baseURL, "base-url", "", "override provider.base_url (single-LLM fallback)")

	// multi-LLM flags
	root.PersistentFlags().StringVar(&sf.only, "only", "", "comma-separated agents to run (e.g. claude,gemini); overrides config")
	root.PersistentFlags().StringVar(&sf.claudeModel, "claude-model", "", "override claude's model")
	root.PersistentFlags().StringVar(&sf.geminiModel, "gemini-model", "", "override gemini's model")
	root.PersistentFlags().StringVar(&sf.codexModel, "codex-model", "", "override codex's model")
	root.PersistentFlags().StringVar(&sf.mergeWith, "merge-with", "", "agent to use for merging findings (default: auto)")

	root.AddCommand(reviewCmd(&sf))
	root.AddCommand(stagedCmd(&sf))
	root.AddCommand(commitCmd(&sf))
	root.AddCommand(branchCmd(&sf))
	root.AddCommand(versionCmd())
	root.AddCommand(configCmd())
	root.AddCommand(doctorCmd())
	root.AddCommand(initCmd())

	if err := root.Execute(); err != nil {
		// cobra already printed the error; just set the exit code
		os.Exit(1)
	}
}

// reviewCmd is the friendly canonical entry point — equivalent to
// `branch` (current branch vs auto-detected base). Most users land here.
func reviewCmd(sf *sharedFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "review [<base>]",
		Short: "Review the current branch with every active LLM (canonical command)",
		Long: `Review the current branch against <base> (default: main) using every
LLM CLI that is installed AND authenticated, in parallel. Findings are
merged into one consolidated report.

Equivalent to ` + "`local-review branch`" + ` — exists as a friendlier name for
the most common workflow.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base := ""
			if len(args) == 1 {
				base = args[0]
			}
			return runUnifiedReview(cmd.Context(), sf, git.ModeBranch, base)
		},
	}
}

func stagedCmd(sf *sharedFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "staged",
		Short: "Review what would be committed next (git diff --cached)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnifiedReview(cmd.Context(), sf, git.ModeStaged, "")
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
			return runUnifiedReview(cmd.Context(), sf, git.ModeCommit, ref)
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
			return runUnifiedReview(cmd.Context(), sf, git.ModeBranch, base)
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

// runUnifiedReview is the dispatch point: detects active LLMs, applies
// flag overrides, runs multi-LLM if any are active, otherwise falls back
// to the v0 single-LLM API path.
func runUnifiedReview(ctx context.Context, sf *sharedFlags, mode git.Mode, ref string) error {
	// Trap Ctrl-C so an in-flight LLM call can be cancelled.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	applyFlagsToConfig(&cfg, sf)

	active, configDisabled := pickAgents(cfg, sf)
	if len(active) == 0 {
		if len(configDisabled) > 0 {
			fmt.Fprintf(os.Stderr, "All authenticated LLM CLIs are disabled in config: %v\n", configDisabled)
			fmt.Fprintln(os.Stderr, "Pass --only <agent> to override config, or run `local-review doctor` for status.")
			fmt.Fprintln(os.Stderr, "Falling back to single-LLM via the configured provider...")
			fmt.Fprintln(os.Stderr)
		}
		return runSingleLLMFallback(ctx, cfg, sf, mode, ref)
	}
	return runMultiLLMReview(ctx, cfg, sf, active, configDisabled, mode, ref)
}

// applyFlagsToConfig overlays --flag values onto the resolved config.
// Single-LLM flags (--model, --base-url) hit cfg.Provider; per-agent
// overrides (--<agent>-model) hit cfg.LLMs.
func applyFlagsToConfig(cfg *config.Config, sf *sharedFlags) {
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

	// Per-agent model overrides
	if sf.claudeModel != "" {
		setLLMModel(cfg, "claude", sf.claudeModel)
	}
	if sf.geminiModel != "" {
		setLLMModel(cfg, "gemini", sf.geminiModel)
	}
	if sf.codexModel != "" {
		setLLMModel(cfg, "codex", sf.codexModel)
	}
}

func setLLMModel(cfg *config.Config, name, model string) {
	if cfg.LLMs == nil {
		cfg.LLMs = make(map[string]config.LLMConfig)
	}
	llm := cfg.LLMs[name]
	llm.Model = model
	cfg.LLMs[name] = llm
}
