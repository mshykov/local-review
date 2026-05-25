package audit

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/prompts"
)

// Options configure one audit Run.
type Options struct {
	// Topic is the audit pack id ("security", "tech-debt", …).
	// Required; the runner refuses an empty value (rather than
	// falling back to a default) because the choice of topic is
	// the whole point of audit mode.
	Topic string

	// LLM is the agent the runner invokes per chunk. Single-LLM by
	// design in v1 — multi-LLM audit would multiply the per-chunk
	// cost without obvious quality return; deferred until we see
	// real usage. Caller is responsible for picking an authenticated
	// LLM via the existing cli.DetectAll / config flow.
	LLM cli.LLM

	// Timeout per chunk. Zero falls back to LLM.TimeoutSec, then
	// to 300 seconds (5 min — audit chunks can be larger than
	// review diffs so we give them more headroom).
	Timeout time.Duration

	// Progress, when non-nil, receives a one-line message after
	// each chunk completes. Hooked up by the CLI to print live
	// progress to stderr so an audit on a large repo doesn't look
	// hung. Pass nil from tests.
	Progress io.Writer
}

// Run executes the audit: for each chunk, invoke the LLM with the
// topic pack as system prompt and the chunk body as input. Per-
// chunk errors are recorded on the PackageReport (not propagated)
// so one transient LLM failure on package N doesn't abort the
// other packages.
//
// Returns a Report with aggregate counts filled in. The caller
// renders to text / markdown / JSON via internal/audit/report.go.
func Run(ctx context.Context, chunks []Chunk, opts Options) (Report, error) {
	if opts.Topic == "" {
		return Report{}, fmt.Errorf("audit topic is required (use --topic security or --topic tech-debt)")
	}
	if opts.LLM.Name == "" {
		return Report{}, fmt.Errorf("audit requires an LLM (none authenticated; run `local-review doctor`)")
	}
	pack, err := prompts.GetAuditPack(opts.Topic)
	if err != nil {
		return Report{}, err
	}
	invoker := cli.NewInvoker(opts.LLM)
	if invoker == nil {
		return Report{}, fmt.Errorf("no invoker for LLM %q", opts.LLM.Name)
	}

	rep := Report{
		Topic:              opts.Topic,
		Generated:          time.Now().UTC(),
		LLM:                opts.LLM.Name,
		Version:            opts.LLM.Version,
		FindingsBySeverity: map[string]int{},
		Packages:           make([]PackageReport, 0, len(chunks)),
	}

	timeout := resolveTimeout(opts.Timeout, opts.LLM)
	for i, c := range chunks {
		if opts.Progress != nil {
			// Stderr-shaped progress write: explicitly discard.
			// Same policy as the walker's Warn writer — see
			// walker.go for the rationale (aborting an audit
			// because a progress line failed to flush would
			// be the wrong choice).
			_, _ = fmt.Fprintf(opts.Progress, "[%d/%d] auditing %s (%d file%s, %s)...\n",
				i+1, len(chunks), c.Package, len(c.Files), pluralS(len(c.Files)), FormatBytes(c.SizeBytes))
		}
		pr := runOne(ctx, c, pack, invoker, timeout)
		rep.Packages = append(rep.Packages, pr)
	}
	fillAggregates(&rep)
	return rep, nil
}

// runOne audits a single chunk. Builds the per-chunk PackageReport
// including parsed findings and timing.
func runOne(ctx context.Context, c Chunk, pack string, invoker cli.Invoker, timeout time.Duration) PackageReport {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, usage, err := invoker.Review(cctx, pack, c.Body)
	dur := time.Since(start)

	pr := PackageReport{
		Package:      c.Package,
		Files:        c.Files,
		DurationMs:   dur.Milliseconds(),
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
	}
	if err != nil {
		pr.Error = err.Error()
		return pr
	}
	pr.Raw = out
	if isCleanSentinel(out) {
		pr.Clean = true
		return pr
	}
	pr.Findings = parseFindings(out)
	return pr
}

// resolveTimeout mirrors the bench package's helper — caller-
// supplied wins, then LLM-configured, then a hardcoded fallback.
// Audit's fallback is intentionally longer than review's (300s vs
// 120s) because audit chunks are larger than review diffs.
func resolveTimeout(provided time.Duration, llm cli.LLM) time.Duration {
	if provided > 0 {
		return provided
	}
	if llm.TimeoutSec > 0 {
		return time.Duration(llm.TimeoutSec) * time.Second
	}
	return 300 * time.Second
}

// cleanSentinelRE matches the audit-pack-mandated "[clean] no
// findings" lines. Both packs end with "no security findings in
// this package" / "no tech-debt findings in this package"; the
// regex is permissive on the descriptor so future audit packs
// don't need to match the literal text.
var cleanSentinelRE = regexp.MustCompile(`(?i)^\s*\[clean\]\s+no\s+\S.*findings\s+in\s+this\s+package\s*$`)

// isCleanSentinel returns true when the LLM output contains a
// "[clean]" sentinel line. Tolerant of leading commentary the LLM
// might add before the sentinel — scan each line.
func isCleanSentinel(out string) bool {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if cleanSentinelRE.MatchString(line) {
			return true
		}
	}
	return false
}

// findingHeaderRE matches the audit-pack-mandated finding header:
//
//	[severity] path/to/file.ext:LINE
//	[severity] path/to/file.ext:LINE-RANGE_END
//	[severity] path/to/file.ext       (line elided)
//
// captures: 1=severity, 2=path, 3=start line (or empty), 4=end
// line (or empty when not a range). The PR #73 review caught the
// prior regex dropping the range tail, which would have made
// `:12-18` parse as Line=12 silently. Now the range is captured
// even though the v1 Finding only stores Line; future LineEnd
// support has the data available on the regex match.
var findingHeaderRE = regexp.MustCompile(`^\s*\[(critical|major|warning|info)\]\s+([^\s:]+)(?::(\d+)(?:-(\d+))?)?`)

// parseFindings walks the LLM output and extracts every header-
// shaped finding plus the body lines that follow (until the next
// header or end of output). The header regex requires a non-empty
// path token, so every emitted Finding has Path set; no fallback
// path needed. (Earlier draft carried a chunk-package fallback
// argument and post-pass; Copilot flagged it as unreachable on
// PR #73.)
func parseFindings(out string) []Finding {
	var findings []Finding
	lines := strings.Split(out, "\n")
	var current *Finding
	var bodyBuf strings.Builder
	flush := func() {
		if current != nil {
			current.Body = strings.TrimSpace(bodyBuf.String())
			findings = append(findings, *current)
		}
		current = nil
		bodyBuf.Reset()
	}
	for _, line := range lines {
		if m := findingHeaderRE.FindStringSubmatch(line); m != nil {
			flush()
			lineNum, lineEnd := 0, 0
			if m[3] != "" {
				if n, err := strconv.Atoi(m[3]); err == nil {
					lineNum = n
				}
			}
			if m[4] != "" {
				if n, err := strconv.Atoi(m[4]); err == nil {
					lineEnd = n
				}
			}
			current = &Finding{
				Severity: m[1],
				Path:     m[2],
				Line:     lineNum,
				LineEnd:  lineEnd,
			}
			continue
		}
		if current != nil {
			bodyBuf.WriteString(line)
			bodyBuf.WriteByte('\n')
		}
	}
	flush()
	return findings
}

// fillAggregates sums TotalFindings, FindingsBySeverity, the
// per-package status counts, and total tokens across rep.Packages.
// Called once at the end of Run; not exported.
func fillAggregates(rep *Report) {
	for _, pr := range rep.Packages {
		rep.TotalInputTokens += pr.InputTokens
		rep.TotalOutputTokens += pr.OutputTokens
		if pr.Error != "" {
			rep.PackagesErrored++
			continue
		}
		if pr.Clean {
			rep.PackagesClean++
			continue
		}
		if len(pr.Findings) > 0 {
			rep.PackagesWithFindings++
			rep.TotalFindings += len(pr.Findings)
			for _, f := range pr.Findings {
				rep.FindingsBySeverity[f.Severity]++
			}
		} else {
			// LLM didn't error and didn't produce the clean
			// sentinel and we parsed no findings — count as
			// clean for the summary (the most common cause is
			// the LLM phrasing "looks clean" instead of using
			// the sentinel). Raw output is on the package
			// report for the user to verify.
			rep.PackagesClean++
		}
	}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// FormatBytes renders a byte count in human-readable units (B /
// KiB / MiB). Exported so the cmd-layer dry-run preview can
// render chunk sizes in the same units the runner's progress
// output uses during a real audit — single source of truth, no
// drift between preview and run. Inputs above ~10 MiB stay in
// MiB rather than escalating to GiB (audit chunks shouldn't
// reach a GiB; if they do, the soft-cap warning is the bigger
// problem).
func FormatBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1fKiB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1fMiB", float64(n)/(1024*1024))
}
