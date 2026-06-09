package audit

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

	// Parallelism caps the number of chunks dispatched to the LLM
	// concurrently. Default (zero or 1) preserves the strict
	// sequential ordering audit shipped with in v0.10-v0.15.0.
	// Setting >1 fans out N chunks at a time to the same agent,
	// which is the right knob for Ollama with `OLLAMA_NUM_PARALLEL`
	// configured (or any backend that serves concurrent requests).
	//
	// Returned PackageReports stay in chunk order regardless of
	// completion order — internal write to a pre-sized slice by
	// index, not append.
	//
	// Constraints:
	//  - Cloud LLM rate limits: claude / codex tier limits may
	//    rate-limit at >1; users on cloud should leave Parallelism=1.
	//  - Local model VRAM: a 7B model on 12GB Apple Silicon can
	//    sustain ~2 concurrent; a 32B on 24GB ~3. The runner
	//    doesn't introspect; if you OOM the server, lower the
	//    flag.
	//
	// v0.15.1: added per the audit-perf patch after user-reported
	// 22-minute runs on a 37-chunk repo via Ollama qwen 7B.
	Parallelism int

	// Invoker is an unexported test seam: when non-nil, Run uses it
	// directly instead of `cli.NewInvoker(opts.LLM)`. Production
	// callers leave it nil; tests inject a fake so parallel-
	// dispatch behaviour (concurrent calls observed, results
	// stay in chunk order) is verifiable without touching real
	// LLM subprocess / HTTP plumbing. Fakes need only satisfy
	// cli.Invoker.Review — RunPrompt isn't exercised by audit.
	Invoker cli.Invoker
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
	invoker := opts.Invoker
	if invoker == nil {
		invoker = cli.NewInvoker(opts.LLM)
		if invoker == nil {
			return Report{}, fmt.Errorf("no invoker for LLM %q", opts.LLM.Name)
		}
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
	parallelism := clampParallelism(opts.Parallelism, len(chunks))

	// Pre-allocate by index so completion order doesn't shuffle the
	// final report. Worker pool reads chunks in walker order; each
	// worker writes to its assigned slot. fillAggregates traverses
	// the slice in order, so users still see packages in the same
	// walker order they would have under sequential dispatch.
	results := make([]PackageReport, len(chunks))
	type job struct {
		idx int
		c   Chunk
	}
	jobs := make(chan job, len(chunks))
	for i, c := range chunks {
		jobs <- job{idx: i, c: c}
	}
	close(jobs)

	// Progress markers fire BEFORE each chunk is dispatched. With
	// parallelism > 1 the dispatch order matches the walker order
	// (we feed jobs sequentially) but completion order doesn't —
	// so the "[N/M] auditing X..." lines describe what's STARTING,
	// not what's FINISHED. The matching summary in the report
	// (chunk-clean / chunk-findings) is the authoritative per-chunk
	// signal; progress is just a liveness indicator.
	var progMu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < parallelism; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				// Stop pulling queued chunks once the run is canceled
				// (Ctrl+C / SIGTERM). Without this, every remaining chunk
				// is dispatched against an already-canceled ctx, fails
				// instantly, and is recorded as "errored" — a "completed"
				// report that silently dropped the tail. Mirrors the
				// review runner, which short-circuits on ctx.Err().
				if ctx.Err() != nil {
					return
				}
				if opts.Progress != nil {
					progMu.Lock()
					// Stderr-shaped progress write: explicitly
					// discard the error. Same policy as the
					// walker's Warn writer — see walker.go for
					// the rationale (aborting an audit because a
					// progress line failed to flush would be the
					// wrong choice).
					_, _ = fmt.Fprintf(opts.Progress, "[%d/%d] auditing %s (%d file%s, %s)...\n",
						j.idx+1, len(chunks), j.c.Package, len(j.c.Files), pluralS(len(j.c.Files)), FormatBytes(j.c.SizeBytes))
					progMu.Unlock()
				}
				results[j.idx] = runOne(ctx, j.c, pack, invoker, timeout)
			}
		}()
	}
	wg.Wait()

	rep.Packages = results
	fillAggregates(&rep)
	// Surface cancellation as an error so the caller exits non-zero rather
	// than emitting a "completed" report whose not-yet-started chunks were
	// silently skipped (CLAUDE.md rule 4: "completed" is wrong if anything
	// was skipped). The partial report is still returned for callers that
	// want to show what did finish.
	if err := ctx.Err(); err != nil {
		done := 0
		for _, pr := range results {
			if pr.Package != "" {
				done++
			}
		}
		return rep, fmt.Errorf("audit canceled after %d/%d chunks: %w", done, len(chunks), err)
	}
	return rep, nil
}

// MaxAuditParallelism is the hard ceiling on the worker pool size,
// regardless of what the user passes via --parallel. Picked to match
// the workflow concurrency cap elsewhere in the codebase:
//
//   - Spawning more workers than chunks is pointless (nothing for
//     the extras to do); we clamp to len(chunks) too.
//   - Beyond ~16 concurrent inference calls, the bottleneck is
//     almost always GPU memory / vendor rate limits on the backend
//     side, not the runner; letting the user spawn 1000 goroutines
//     just degrades reliability without buying throughput.
//   - The 16 ceiling matches min(16, cpu_cores-2) used by the
//     workflow harness — same reasoning, same shape.
//
// A user with a beefier setup who genuinely wants >16 can either
// raise this constant locally or, more reasonably, run multiple
// audits in parallel from a shell.
const MaxAuditParallelism = 16

// clampParallelism bounds the requested Parallelism by both the
// chunk count (no point spawning more workers than work units) and
// MaxAuditParallelism (resource-exhaustion guard, self-review catch
// on PR for v0.15.1). Negative / zero values fall back to 1.
func clampParallelism(requested, numChunks int) int {
	if requested < 1 {
		return 1
	}
	if requested > MaxAuditParallelism {
		requested = MaxAuditParallelism
	}
	if numChunks > 0 && requested > numChunks {
		requested = numChunks
	}
	return requested
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
	if strings.TrimSpace(out) == "" {
		// CLI exited 0 with empty stdout (rate-limited / capacity-
		// exhausted reply, empty --output-last-message, a parser that
		// stripped everything). Not a clean result — record it as an
		// error so it lands in PackagesErrored, never silently inflating
		// PackagesClean. The review path catches the same pathology.
		pr.Error = "LLM exited 0 but returned empty output"
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

// auditSeverityRank orders the audit severity tiers (audit has no nit —
// see the audit packs). Higher = more severe. An unrecognized label ranks
// 0, so it survives only when no --min-severity floor is set.
func auditSeverityRank(sev string) int {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "critical":
		return 4
	case "major":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

// Filtered returns a copy of the report keeping only findings at or above
// minSeverity (when non-empty) and capping the total number of findings at
// maxFindings (when > 0, across packages in report order). Aggregates are
// recomputed on the filtered set. The second return is the number of
// findings hidden, so the caller can disclose the truncation rather than
// dropping it silently (CLAUDE.md rule 4). With no floor and no cap the
// report is returned unchanged and hidden is 0.
func (r Report) Filtered(minSeverity string, maxFindings int) (Report, int) {
	minRank := auditSeverityRank(minSeverity)
	hasFloor := strings.TrimSpace(minSeverity) != ""
	if !hasFloor && maxFindings <= 0 {
		return r, 0
	}

	out := r
	out.FindingsBySeverity = map[string]int{}
	out.TotalFindings = 0
	out.PackagesWithFindings = 0
	out.PackagesClean = 0
	out.PackagesErrored = 0
	out.TotalInputTokens = 0
	out.TotalOutputTokens = 0
	out.Packages = make([]PackageReport, len(r.Packages))

	hidden, kept := 0, 0
	for i, pr := range r.Packages {
		np := pr
		np.Findings = nil
		for _, f := range pr.Findings {
			if hasFloor && auditSeverityRank(f.Severity) < minRank {
				hidden++
				continue
			}
			if maxFindings > 0 && kept >= maxFindings {
				hidden++
				continue
			}
			np.Findings = append(np.Findings, f)
			kept++
		}
		out.Packages[i] = np
	}
	fillAggregates(&out)
	return out, hidden
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
