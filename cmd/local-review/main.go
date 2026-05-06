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
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/git"
)

// banner is the single-line "local-review" header shown atop --help on
// a TTY. The previous 5-line figlet ate too much vertical space — for a
// reviewer that runs from `git commit` editors and CI logs, compact
// wins over decorative. helpHeader() still suppresses the banner for
// non-TTY stdout (pipes/files) so machine-readable callers get clean
// text.
const banner = "── local-review · multi-LLM code review ──"

// helpHeader returns the banner when stdout is a TTY, or an empty
// string otherwise. We use term.IsTerminal on the stdout fd; $COLUMNS
// isn't reliable here because shells don't export it to child
// processes by default.
func helpHeader() string {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return ""
	}
	return banner + "\n\n"
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
	// Preserve AddCommand insertion order in --help so the canonical
	// `review` appears first inside the Review group instead of being
	// alphabetised behind `branch`/`commit`. Cobra's default sort hides
	// the most-used command at the bottom.
	cobra.EnableCommandSorting = false

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
		// We print errors ourselves in main() so the blocking-findings
		// sentinel can exit 2 without cobra adding a noisy "Error: ..."
		// line after the user already saw the full review report.
		SilenceErrors: true,
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

	// Group commands so --help reads as three sections (Review / Setup /
	// Other) instead of one alphabetical wall. Cobra renders any command
	// without a GroupID under "Additional Commands:", so we also wire the
	// auto-generated help/completion commands into the "other" group.
	root.AddGroup(
		&cobra.Group{ID: "review", Title: "Review:"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
		&cobra.Group{ID: "other", Title: "Other:"},
	)
	root.SetHelpCommandGroupID("other")
	root.SetCompletionCommandGroupID("other")

	addGrouped := func(group string, cmd *cobra.Command) {
		cmd.GroupID = group
		root.AddCommand(cmd)
	}

	addGrouped("review", reviewCmd(&sf))
	addGrouped("review", stagedCmd(&sf))
	addGrouped("review", commitCmd(&sf))
	addGrouped("review", branchCmd(&sf))
	addGrouped("setup", initCmd())
	addGrouped("setup", doctorCmd())
	addGrouped("setup", configCmd(&sf))
	addGrouped("other", versionCmd())

	if err := root.Execute(); err != nil {
		// errBlockingFindings is a sentinel — review found major/critical
		// findings, gate the pre-commit hook with exit code 2. The user
		// already saw the full review report; no extra "Error:" line.
		if errors.Is(err, errBlockingFindings) {
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
		// `--only` is an explicit allow-list. If the user typed
		// `--only clude` (typo) or named an unauthenticated agent, the
		// safe behavior is to error rather than silently fall back to
		// the configured single-LLM provider — that would send the
		// diff to a different vendor than the one explicitly named,
		// which is a privacy / cost / surprise footgun.
		if sf.only != "" {
			return fmt.Errorf("--only %q matched no ready LLMs (run `local-review doctor` to see what's authenticated; refusing to fall back to single-LLM since --only is an explicit allow-list)", sf.only)
		}
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

	// --merge-with overrides merge.preferred_llm. Without this branch,
	// runtime merge selection honored the flag but `local-review config
	// --merge-with claude` still printed `merge.preferred_llm: auto`,
	// which made the preview misleading.
	if sf.mergeWith != "" {
		cfg.Merge.PreferredLLM = sf.mergeWith
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
