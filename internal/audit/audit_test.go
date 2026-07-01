package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mshykov/local-review/internal/agents"
	"github.com/mshykov/local-review/internal/cli"
)

// TestParseFindings_HeaderRowsAndBodies covers the audit-pack-
// mandated finding shape: `[severity] path:line` header followed
// by body lines until the next header or EOF.
func TestParseFindings_HeaderRowsAndBodies(t *testing.T) {
	out := `[critical] internal/cli/parsers.go:42
SQL built by string concatenation in search query.
Why: turns user input into executable SQL — textbook SQLi.
Suggest: parameterize the query.

[warning] internal/cli/parsers.go:120
Generic Exception catch swallows real errors.
Why: hides programmer bugs behind the retry loop.
Suggest: catch specific error types only.
`
	findings := parseFindings(out)
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings; got %d: %+v", len(findings), findings)
	}
	if findings[0].Severity != "critical" || findings[0].Path != "internal/cli/parsers.go" || findings[0].Line != 42 {
		t.Errorf("first finding shape wrong: %+v", findings[0])
	}
	if !strings.Contains(findings[0].Body, "SQL built by string concatenation") {
		t.Errorf("first finding body missing the lead line: %q", findings[0].Body)
	}
	if findings[1].Severity != "warning" || findings[1].Line != 120 {
		t.Errorf("second finding shape wrong: %+v", findings[1])
	}
}

// TestParseFindings_LineRangeCaptured covers the v0.10.0-c PR-
// review fix: the header regex accepts `:LINE-LINE` ranges in
// addition to plain `:LINE`. Audit packs document the range
// shape; without the regex update, `file.go:12-18` parsed as
// Line=12 and silently dropped the end-line.
func TestParseFindings_LineRangeCaptured(t *testing.T) {
	out := `[warning] foo.go:12-18
duplicated try/except chain across function body
Why: divergence in 3 of the 6 copies; bug fixes only land in one.
Suggest: extract a shared helper.
`
	findings := parseFindings(out)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(findings))
	}
	if findings[0].Line != 12 || findings[0].LineEnd != 18 {
		t.Errorf("expected Line=12 LineEnd=18; got Line=%d LineEnd=%d", findings[0].Line, findings[0].LineEnd)
	}
}

// TestFormatLocation_HandlesSingleLineAndRange covers the
// renderer-side rendering of Finding.Line / Finding.LineEnd.
// Single-line stays "path:line"; range becomes "path:start-end";
// elided line drops the suffix entirely.
func TestFormatLocation_HandlesSingleLineAndRange(t *testing.T) {
	cases := []struct {
		f    Finding
		want string
	}{
		{Finding{Path: "foo.go", Line: 12}, "foo.go:12"},
		{Finding{Path: "foo.go", Line: 12, LineEnd: 18}, "foo.go:12-18"},
		{Finding{Path: "foo.go"}, "foo.go"},
		// LineEnd == Line collapses to single-line shape (LLM
		// occasionally emits redundant range like "12-12").
		{Finding{Path: "foo.go", Line: 12, LineEnd: 12}, "foo.go:12"},
	}
	for _, tc := range cases {
		if got := formatLocation(tc.f); got != tc.want {
			t.Errorf("formatLocation(%+v) = %q, want %q", tc.f, got, tc.want)
		}
	}
}

// TestIsCleanSentinel_RecognizesPackPhrasing covers the two audit
// packs' clean sentinels plus a tolerance test for surrounding
// whitespace / commentary the LLM might add.
func TestIsCleanSentinel_RecognizesPackPhrasing(t *testing.T) {
	for _, body := range []string{
		"[clean] no security findings in this package",
		"[clean] no tech-debt findings in this package",
		"  [clean] no security findings in this package  ",
		"After review:\n[clean] no security findings in this package\n",
	} {
		if !isCleanSentinel(body) {
			t.Errorf("expected clean sentinel for %q", body)
		}
	}
	// Negative cases: the LLM produces findings but says "clean"
	// somewhere — the sentinel must require the exact shape.
	for _, body := range []string{
		"[warning] foo.go:1\nlooks clean otherwise",
		"the code is clean",
		"no findings",
	} {
		if isCleanSentinel(body) {
			t.Errorf("unexpected clean match for %q", body)
		}
	}
}

// TestFillAggregates_CountsAcrossPackagesAndStatuses verifies the
// summary counters: severities, package status (with-findings /
// clean / errored), and token totals.
func TestFillAggregates_CountsAcrossPackagesAndStatuses(t *testing.T) {
	rep := Report{
		FindingsBySeverity: map[string]int{},
		Packages: []PackageReport{
			{
				Package: "internal/cli",
				Findings: []Finding{
					{Severity: "critical"},
					{Severity: "warning"},
				},
				InputTokens:  1000,
				OutputTokens: 200,
			},
			{Package: "internal/git", Clean: true, InputTokens: 500, OutputTokens: 50},
			{Package: "internal/multi", Error: "timeout", InputTokens: 100, OutputTokens: 0},
			// Parser-miss: no error, not the clean sentinel, zero parsed
			// findings (LLM phrased "looks fine" in prose). Documented to
			// count as clean — pin that branch so a parseFindings
			// regression that drops real findings here is at least visible.
			{Package: "internal/lang", Raw: "Looks fine to me, nothing to flag."},
		},
	}
	fillAggregates(&rep)
	if rep.TotalFindings != 2 {
		t.Errorf("TotalFindings = %d, want 2", rep.TotalFindings)
	}
	if rep.FindingsBySeverity["critical"] != 1 || rep.FindingsBySeverity["warning"] != 1 {
		t.Errorf("FindingsBySeverity wrong: %v", rep.FindingsBySeverity)
	}
	if rep.PackagesWithFindings != 1 || rep.PackagesClean != 2 || rep.PackagesErrored != 1 {
		t.Errorf("package status counts wrong: %+v", rep)
	}
	if rep.TotalInputTokens != 1600 || rep.TotalOutputTokens != 250 {
		t.Errorf("token totals wrong: in=%d out=%d", rep.TotalInputTokens, rep.TotalOutputTokens)
	}
}

// TestWalker_PathPassesFilters covers the include / exclude
// prefix logic. Include defaults to "match anything" when empty;
// exclude always filters.
func TestWalker_PathPassesFilters(t *testing.T) {
	cases := []struct {
		path    string
		include []string
		exclude []string
		want    bool
	}{
		{"internal/cli/foo.go", nil, nil, true},
		{"internal/cli/foo.go", []string{"internal/cli"}, nil, true},
		{"internal/cli/foo.go", []string{"internal/multi"}, nil, false},
		{"internal/cli/foo.go", nil, []string{"internal/cli"}, false},
		{"internal/cli/foo.go", []string{"internal"}, []string{"internal/cli"}, false},
		// Directory-boundary cases (regression for the v0.10.0-c
		// PR review fix): `internal/cli` must NOT match
		// `internal/cli2/foo.go`. Raw HasPrefix used to do this
		// wrong and pull unintended files into the filter.
		{"internal/cli2/foo.go", []string{"internal/cli"}, nil, false},
		{"internal/cli2/foo.go", nil, []string{"internal/cli"}, true},
		// Exact-match (no trailing slash, file at the directory's
		// own path) and trailing-slash tolerance.
		{"internal/cli", []string{"internal/cli"}, nil, true},
		{"internal/cli/foo.go", []string{"internal/cli/"}, nil, true},
	}
	for _, tc := range cases {
		if got := pathPassesFilters(tc.path, tc.include, tc.exclude); got != tc.want {
			t.Errorf("filter(%q, inc=%v, exc=%v) = %v, want %v", tc.path, tc.include, tc.exclude, got, tc.want)
		}
	}
}

// TestWalker_IsAuditable verifies the extension allowlist: known
// languages route via lang.Detect, the built-in extras catch
// common config / shell shapes, and lockfiles are explicitly
// skipped.
func TestWalker_IsAuditable(t *testing.T) {
	for _, path := range []string{
		"main.go", "util.py", "build.gradle.kts",
		"scripts/install.sh",
		".github/workflows/ci.yml",
		"sql/migrations/001.sql",
	} {
		if !isAuditable(path) {
			t.Errorf("expected auditable: %s", path)
		}
	}
	for _, path := range []string{
		"go.sum",
		"package-lock.json",
		"vendor/dep/Cargo.lock",
		"image.png",
		"archive.tar.gz",
	} {
		if isAuditable(path) {
			t.Errorf("expected NOT auditable: %s", path)
		}
	}
}

// --- v0.15.1 parallel audit dispatch ---------------------------------

// fakeAuditInvoker satisfies cli.Invoker for runner tests. Each
// Review call sleeps `delay` so a parallel run can be distinguished
// from a sequential one by wallclock: N chunks at delay D run in
// ~N*D sequentially vs. ~ceil(N/parallelism)*D in parallel. inFlight
// also peaks at parallelism, which a thread-safe gauge exposes.
type fakeAuditInvoker struct {
	delay        time.Duration
	mu           sync.Mutex
	inFlight     int
	peakInFlight int
	totalCalls   int
}

func (f *fakeAuditInvoker) Review(ctx context.Context, systemPrompt, diff string) (string, agents.TokenUsage, error) {
	f.mu.Lock()
	f.inFlight++
	if f.inFlight > f.peakInFlight {
		f.peakInFlight = f.inFlight
	}
	f.totalCalls++
	f.mu.Unlock()

	// Simulate per-chunk LLM latency.
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
		return "", agents.TokenUsage{}, ctx.Err()
	}

	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	return "clean — no issues found", agents.TokenUsage{}, nil
}

func (f *fakeAuditInvoker) RunPrompt(ctx context.Context, prompt string) (string, agents.TokenUsage, error) {
	return "", agents.TokenUsage{}, nil
}

// TestRun_ParallelDispatch_ReducesWallclock pins the v0.15.1
// audit-perf contract: with Parallelism=N (N>1) and chunks taking
// `delay` each, total wallclock is approximately ceil(numChunks/N)
// * delay, not numChunks * delay. The peak-in-flight gauge also
// hits N — direct proof concurrent dispatch happened, not just a
// coincidentally-fast sequential run.
//
// User-driven motivation: a 37-chunk Ollama audit took 22 min in
// v0.15.0 (sequential). With Parallelism=4 and OLLAMA_NUM_PARALLEL=4
// server-side, expected wallclock is ~6 min. This test pins the
// runtime shape so a future refactor can't silently re-serialise.
func TestRun_ParallelDispatch_ReducesWallclock(t *testing.T) {
	chunks := make([]Chunk, 8)
	for i := range chunks {
		chunks[i] = Chunk{Package: fmtPkg(i), Files: []string{"f.go"}, Body: "x", SizeBytes: 1}
	}
	const delay = 100 * time.Millisecond

	// The real invariants are peakInFlight (proves concurrent
	// dispatch happened) and result-order preservation; wallclock
	// is a sanity check only. Self-review caught that strict
	// timing thresholds flake on noisy CI hosts — so we just
	// require par/seq RATIO ≤ 0.6, far more lenient than absolute
	// numbers and doesn't care about CI noise level as long as
	// both runs see the same noise.
	fakeSeq := &fakeAuditInvoker{delay: delay}
	startSeq := time.Now()
	repSeq, err := Run(context.Background(), chunks, Options{
		Topic:       "security",
		LLM:         cli.LLM{Name: "fake"},
		Invoker:     fakeSeq,
		Parallelism: 1,
	})
	if err != nil {
		t.Fatalf("sequential Run: %v", err)
	}
	elapsedSeq := time.Since(startSeq)
	if fakeSeq.peakInFlight != 1 {
		t.Errorf("sequential peak in-flight: got %d, want 1", fakeSeq.peakInFlight)
	}

	fakePar := &fakeAuditInvoker{delay: delay}
	startPar := time.Now()
	repPar, err := Run(context.Background(), chunks, Options{
		Topic:       "security",
		LLM:         cli.LLM{Name: "fake"},
		Invoker:     fakePar,
		Parallelism: 4,
	})
	if err != nil {
		t.Fatalf("parallel Run: %v", err)
	}
	elapsedPar := time.Since(startPar)
	// Primary assertion — peak concurrency. This is the actual
	// concurrency contract; if it passes, dispatch IS parallel
	// regardless of CI clock noise.
	if fakePar.peakInFlight != 4 {
		t.Errorf("parallel peak in-flight: got %d, want 4 (Parallelism cap)", fakePar.peakInFlight)
	}
	if fakePar.totalCalls != 8 {
		t.Errorf("parallel total calls: got %d, want 8 (one per chunk)", fakePar.totalCalls)
	}
	// Secondary sanity — ratio, not absolute. On a flaky CI the
	// ratio still holds as long as both runs see the same noise.
	ratio := float64(elapsedPar) / float64(elapsedSeq)
	if ratio > 0.6 {
		t.Errorf("parallel/sequential wallclock ratio %.2f exceeds 0.6 — concurrent dispatch likely didn't take effect (seq=%v par=%v)", ratio, elapsedSeq, elapsedPar)
	}

	// Order preservation: results must stay in chunk order regardless
	// of which goroutine completed first.
	if len(repSeq.Packages) != len(chunks) || len(repPar.Packages) != len(chunks) {
		t.Fatalf("package counts: seq=%d par=%d want=%d", len(repSeq.Packages), len(repPar.Packages), len(chunks))
	}
	for i := range chunks {
		if got, want := repPar.Packages[i].Package, fmtPkg(i); got != want {
			t.Errorf("parallel result order broken at index %d: got %q, want %q (completion order leaked into final report)", i, got, want)
		}
	}
}

func fmtPkg(i int) string { return "pkg-" + strconv.Itoa(i) }

// stubInvoker returns a fixed output/error, for tests that need to drive
// a specific Review outcome (empty output, cancellation).
type stubInvoker struct {
	out string
	err error
}

func (s stubInvoker) Review(_ context.Context, _, _ string) (string, agents.TokenUsage, error) {
	return s.out, agents.TokenUsage{}, s.err
}
func (s stubInvoker) RunPrompt(_ context.Context, _ string) (string, agents.TokenUsage, error) {
	return s.out, agents.TokenUsage{}, s.err
}

// TestRunOne_EmptyOutputCountsAsErrorNotClean pins MIN-4: a CLI that exits
// 0 with empty stdout is recorded as an error, never folded into the clean
// bucket (which would overstate audit coverage).
func TestRunOne_EmptyOutputCountsAsErrorNotClean(t *testing.T) {
	pr := runOne(context.Background(),
		Chunk{Package: "p", Files: []string{"f.go"}, Body: "x"},
		"pack", stubInvoker{out: "   \n\t"}, time.Second)
	if pr.Clean {
		t.Error("empty output must not be Clean")
	}
	if pr.Error == "" {
		t.Error("empty output must set Error so it lands in PackagesErrored")
	}
	if len(pr.Findings) != 0 {
		t.Errorf("empty output should parse no findings, got %d", len(pr.Findings))
	}
}

// TestReportFiltered pins MIN-10: --min-severity drops sub-threshold
// findings, --max-findings caps the total across packages in order, both
// recompute aggregates, and the hidden count is reported (never silently
// dropped). The source report is left untouched.
func TestReportFiltered(t *testing.T) {
	newBase := func() Report {
		r := Report{
			FindingsBySeverity: map[string]int{},
			Packages: []PackageReport{
				{Package: "a", Findings: []Finding{{Severity: "critical"}, {Severity: "warning"}, {Severity: "info"}}},
				{Package: "b", Findings: []Finding{{Severity: "major"}, {Severity: "info"}}},
			},
		}
		fillAggregates(&r)
		return r
	}

	// No floor, no cap → unchanged.
	if got, hidden := newBase().Filtered("", 0); hidden != 0 || got.TotalFindings != 5 {
		t.Errorf("no-op: total=%d hidden=%d, want 5/0", got.TotalFindings, hidden)
	}

	// Floor = major → keep critical + major; drop warning + 2 info.
	maj, hidden := newBase().Filtered("major", 0)
	if maj.TotalFindings != 2 || hidden != 3 {
		t.Errorf("min-severity major: total=%d hidden=%d, want 2/3", maj.TotalFindings, hidden)
	}
	if maj.FindingsBySeverity["critical"] != 1 || maj.FindingsBySeverity["major"] != 1 || maj.FindingsBySeverity["info"] != 0 {
		t.Errorf("severity breakdown after floor wrong: %v", maj.FindingsBySeverity)
	}

	// Cap = 2 → keep the first 2 findings across packages in order.
	capped, hidden := newBase().Filtered("", 2)
	if capped.TotalFindings != 2 || hidden != 3 {
		t.Errorf("max-findings 2: total=%d hidden=%d, want 2/3", capped.TotalFindings, hidden)
	}

	// nit floor keeps everything (audit emits no nit, rank 0).
	if got, hidden := newBase().Filtered("nit", 0); got.TotalFindings != 5 || hidden != 0 {
		t.Errorf("nit floor: total=%d hidden=%d, want 5/0", got.TotalFindings, hidden)
	}

	// Source report must be untouched by Filtered.
	base := newBase()
	_, _ = base.Filtered("critical", 1)
	total := 0
	for _, p := range base.Packages {
		total += len(p.Findings)
	}
	if total != 5 {
		t.Errorf("Filtered mutated the source report: base now has %d findings, want 5", total)
	}
}

// TestRun_CanceledContextStopsAndErrors pins MIN-2: a canceled audit returns
// a non-nil error so the caller exits non-zero, instead of emitting a
// "completed" report whose unstarted chunks were silently skipped.
func TestRun_CanceledContextStopsAndErrors(t *testing.T) {
	chunks := make([]Chunk, 8)
	for i := range chunks {
		chunks[i] = Chunk{Package: fmtPkg(i), Files: []string{"f.go"}, Body: "x", SizeBytes: 1}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before Run dispatches any chunk

	_, err := Run(ctx, chunks, Options{
		Topic:       "security",
		LLM:         cli.LLM{Name: "fake"},
		Invoker:     stubInvoker{out: "clean — no issues found"},
		Parallelism: 1,
	})
	if err == nil {
		t.Fatal("canceled audit must return an error, not a clean nil")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Errorf("error should mention cancellation, got: %v", err)
	}
}

// TestClampParallelism pins the v0.15.1 self-review fix for the
// unbounded-worker concern. A user passing --parallel 1000 must not
// spawn 1000 goroutines + 1000 concurrent LLM calls. Clamp by both
// the hard ceiling (MaxAuditParallelism) and the work-unit count
// (no point in more workers than chunks).
func TestClampParallelism(t *testing.T) {
	cases := []struct {
		requested, numChunks, want int
		note                       string
	}{
		{requested: 4, numChunks: 100, want: 4, note: "happy path within ceiling and chunk count"},
		{requested: 1, numChunks: 100, want: 1, note: "sequential default preserved"},
		{requested: 0, numChunks: 10, want: 1, note: "zero falls back to 1"},
		{requested: -5, numChunks: 10, want: 1, note: "negative falls back to 1"},
		{requested: 1000, numChunks: 100, want: MaxAuditParallelism, note: "huge request clamped to ceiling"},
		{requested: 8, numChunks: 3, want: 3, note: "request exceeding chunk count clamped to chunk count"},
		{requested: 20, numChunks: 4, want: 4, note: "request exceeds both — chunk count wins (smallest cap)"},
		{requested: 4, numChunks: 0, want: 4, note: "zero chunks: respect request (caller handles empty)"},
	}
	for _, tc := range cases {
		if got := clampParallelism(tc.requested, tc.numChunks); got != tc.want {
			t.Errorf("clampParallelism(req=%d, chunks=%d) = %d, want %d (%s)", tc.requested, tc.numChunks, got, tc.want, tc.note)
		}
	}
}

// TestWalker_IsAuditable_LockfilesAndMinified_v0151 pins the v0.15.1
// fix where lockfiles AND minified bundles must be skipped BEFORE
// the extension allowlist gets a vote. Pre-fix, pnpm-lock.yaml
// matched the .yaml allowlist (CI workflows / k8s manifests) and
// returned auditable=true — a 272 KiB lockfile chunk was burning
// ~5 minutes on Ollama for zero useful signal (user-reported).
func TestWalker_IsAuditable_LockfilesAndMinified_v0151(t *testing.T) {
	mustSkip := []string{
		// Lockfile family — base-name match must beat any extension
		// allowlist (pnpm-lock.yaml is the real-world repro case).
		"pnpm-lock.yaml",
		"apps/web/pnpm-lock.yaml", // nested locations still match base
		"package-lock.json",
		"yarn.lock",
		"npm-shrinkwrap.json",
		"bun.lockb",
		"Cargo.lock",
		"Gemfile.lock",
		"Podfile.lock",
		"composer.lock",
		"poetry.lock",
		"Pipfile.lock",
		"mix.lock",
		"pubspec.lock",
		"flake.lock",
		// Minified bundles — source lives elsewhere.
		"static/app.min.js",
		"public/bundle.min.css",
		"dist/vendor.min.map",
	}
	for _, p := range mustSkip {
		if isAuditable(p) {
			t.Errorf("v0.15.1 default skip: %s must NOT be auditable (regression in lockfile/minified gate)", p)
		}
	}

	// Sanity: legitimate `.yaml` files still get audited — the
	// fix is targeted, not a blanket yaml skip.
	mustAudit := []string{
		".github/workflows/ci.yml",
		"k8s/deployment.yaml",
		"compose.yaml",
	}
	for _, p := range mustAudit {
		if !isAuditable(p) {
			t.Errorf("legitimate yaml %s should still be auditable; v0.15.1 fix must not over-skip", p)
		}
	}
}

// TestWriteJSON_RoundTrip ensures the Report serialises and back
// without losing the headline fields. Catches accidental json-tag
// drift on schema changes.
func TestWriteJSON_RoundTrip(t *testing.T) {
	rep := Report{
		Topic:                "security",
		Generated:            time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		LLM:                  "claude",
		TotalFindings:        3,
		PackagesWithFindings: 1,
		FindingsBySeverity:   map[string]int{"critical": 1, "warning": 2},
		Packages: []PackageReport{
			{
				Package:  "internal/cli",
				Files:    []string{"internal/cli/parsers.go"},
				Findings: []Finding{{Severity: "critical", Path: "internal/cli/parsers.go", Line: 42, Body: "x"}},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, rep); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Topic != "security" || back.TotalFindings != 3 || back.FindingsBySeverity["critical"] != 1 {
		t.Errorf("round-trip lost data: %+v", back)
	}
}

// TestWriteMarkdown_StructureForMixedPackageStatuses verifies the
// markdown shape: summary table, per-package sections for
// findings-bearing packages, a collapsed "Clean packages" line,
// and an "Errored packages" tail. Anchored on substrings so
// future formatting tweaks don't false-fail.
func TestWriteMarkdown_StructureForMixedPackageStatuses(t *testing.T) {
	rep := Report{
		Topic:              "security",
		Generated:          time.Now(),
		LLM:                "claude",
		FindingsBySeverity: map[string]int{"critical": 1, "warning": 1},
		TotalFindings:      2,
		Packages: []PackageReport{
			{
				Package: "internal/cli",
				Files:   []string{"a.go"},
				Findings: []Finding{
					{Severity: "critical", Path: "internal/cli/a.go", Line: 12, Body: "SQLi"},
					{Severity: "warning", Path: "internal/cli/a.go", Line: 40, Body: "broad catch"},
				},
			},
			{Package: "internal/git", Files: []string{"b.go"}, Clean: true},
			{Package: "internal/multi", Files: []string{"c.go"}, Error: "timeout"},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"# Audit — security",
		"## Summary",
		"| critical | 1 |",
		"| warning | 1 |",
		"## internal/cli",
		"**[critical]**", "**[warning]**",
		"## Clean packages",
		"internal/git",
		"## Errored packages",
		"timeout",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestSplitChunk_FitsInOnePackageWhenUnderCap verifies the
// no-split happy path: a package whose total body fits under
// maxBytes emits a single chunk with the package name unchanged.
// No "[part N/M]" suffix when the package wasn't split.
func TestSplitChunk_FitsInOnePackageWhenUnderCap(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "a.go", "package a\n")
	mkFile(t, root, "b.go", "package a\n")
	chunks, err := splitChunk(root, ".", []string{"a.go", "b.go"}, 64*1024, nil)
	if err != nil {
		t.Fatalf("splitChunk: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk; got %d", len(chunks))
	}
	if chunks[0].Package != "." {
		t.Errorf("single chunk should keep package name; got %q", chunks[0].Package)
	}
	if !strings.Contains(chunks[0].Body, "// === FILE: a.go ===") {
		t.Errorf("body missing file marker for a.go: %q", chunks[0].Body)
	}
}

// TestSplitChunk_SplitsAcrossBinsWhenOverCap verifies the
// headline fix: a package whose body exceeds maxBytes is
// auto-split into multiple chunks labelled `pkg [part N/M]`, each
// at-or-below the cap, with file adjacency preserved (greedy
// bin-pack in input order).
//
// Reproduces the user-reported failure shape from PR #73 dogfood
// where 321 of 343 Android packages errored with prompt_too_long
// because the runner just shipped oversized chunks to Claude.
func TestSplitChunk_SplitsAcrossBinsWhenOverCap(t *testing.T) {
	root := t.TempDir()
	// Each file ~30 KiB; cap 50 KiB → expect 4 bins for 4 files
	// (one file per bin since two would exceed the cap).
	body := strings.Repeat("// filler\n", 3000) // ~30 KiB each
	mkFile(t, root, "a.go", body)
	mkFile(t, root, "b.go", body)
	mkFile(t, root, "c.go", body)
	mkFile(t, root, "d.go", body)
	wantFiles := []string{"a.go", "b.go", "c.go", "d.go"}
	chunks, err := splitChunk(root, "pkg", wantFiles, 50*1024, nil)
	if err != nil {
		t.Fatalf("splitChunk: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for over-cap package; got %d", len(chunks))
	}
	// Each chunk must be labelled with the [part N/M] suffix.
	for _, c := range chunks {
		if !strings.Contains(c.Package, "pkg [part ") {
			t.Errorf("split chunk missing [part N/M] suffix: %q", c.Package)
		}
		// Each individual chunk should be at-or-under the cap.
		// (Single oversized files are the exception covered by
		// the other test; here all files are 30 KiB and the cap
		// is 50 KiB so all bins must fit.)
		if c.SizeBytes > 50*1024 {
			t.Errorf("split bin exceeded cap: %s (%d bytes)", c.Package, c.SizeBytes)
		}
	}
	// Flatten chunks back into the file list and assert exact
	// equality with the input — splitChunk must preserve every
	// file in input order, never drop or duplicate. CodeRabbit
	// caught the prior shape as too lax (would have passed even
	// if files were dropped). Hardened on PR #74 review.
	gotFiles := make([]string, 0, len(wantFiles))
	for _, c := range chunks {
		gotFiles = append(gotFiles, c.Files...)
	}
	if len(gotFiles) != len(wantFiles) {
		t.Fatalf("expected %d files across split chunks; got %d (%v)", len(wantFiles), len(gotFiles), gotFiles)
	}
	for i := range wantFiles {
		if gotFiles[i] != wantFiles[i] {
			t.Errorf("split file at index %d: got %q, want %q (full flattened: %v)", i, gotFiles[i], wantFiles[i], gotFiles)
		}
	}
}

// TestWalk_RejectsNegativeMaxBytesPerChunk pins the fail-loud
// guard added in PR #74: a negative cap would make every file
// appear oversized in the bin-packer and produce nonsense
// warnings, so Walk refuses up-front with an actionable error.
// Zero stays valid (means "use the default"). CLAUDE.md rule 4:
// fail loud, fail closed.
func TestWalk_RejectsNegativeMaxBytesPerChunk(t *testing.T) {
	_, err := Walk(WalkOptions{Root: t.TempDir(), MaxBytesPerChunk: -1})
	if err == nil {
		t.Fatal("expected Walk to reject negative MaxBytesPerChunk; got nil error")
	}
	if !strings.Contains(err.Error(), "MaxBytesPerChunk must be >= 0") {
		t.Errorf("error should mention the constraint; got %v", err)
	}
}

// initTestGitRepo creates a real git repo at t.TempDir() with the given
// tracked files, mirroring internal/git's own test pattern (diff_test.go) —
// Walk shells out to real `git ls-files`, so groupFilesByPackage /
// buildChunksFromPackages need an actual repo to exercise, not a mock.
func initTestGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "t@example.com")
	runGit("config", "user.name", "T")
	runGit("config", "commit.gpgsign", "false")
	for rel, content := range files {
		full := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit("add", rel)
	}
	runGit("commit", "-q", "-m", "init")
	return repo
}

// TestWalk_GroupsFilesByPackageAndSortsDeterministically exercises Walk
// end-to-end against a real git repo: two packages, each with tracked
// source files plus one file that pathPassesFilters/isAuditable should
// exclude (Exclude prefix, and a binary-shaped extension). Covers
// groupFilesByPackage + buildChunksFromPackages, which had no direct test
// before the cognitive-complexity refactor extracted them from Walk.
func TestWalk_GroupsFilesByPackageAndSortsDeterministically(t *testing.T) {
	repo := initTestGitRepo(t, map[string]string{
		"pkgb/z.go":       "package pkgb\n",
		"pkgb/a.go":       "package pkgb\n",
		"pkga/main.go":    "package pkga\n",
		"pkga/skip.png":   "not source",
		"excluded/foo.go": "package excluded\n",
	})
	chunks, err := Walk(WalkOptions{Root: repo, Exclude: []string{"excluded"}})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 package chunks (pkga, pkgb), got %d: %+v", len(chunks), chunks)
	}
	// Packages sorted alphabetically: pkga before pkgb.
	if chunks[0].Package != "pkga" || chunks[1].Package != "pkgb" {
		t.Errorf("expected chunks sorted [pkga, pkgb], got [%s, %s]", chunks[0].Package, chunks[1].Package)
	}
	if len(chunks[0].Files) != 1 || chunks[0].Files[0] != "pkga/main.go" {
		t.Errorf("pkga chunk should contain only main.go (skip.png excluded by isAuditable), got %v", chunks[0].Files)
	}
	// Files within a package sorted too: a.go before z.go.
	wantPkgB := []string{"pkgb/a.go", "pkgb/z.go"}
	if len(chunks[1].Files) != 2 || chunks[1].Files[0] != wantPkgB[0] || chunks[1].Files[1] != wantPkgB[1] {
		t.Errorf("pkgb chunk files = %v, want %v (sorted)", chunks[1].Files, wantPkgB)
	}
	for _, c := range chunks {
		if strings.Contains(c.Package, "excluded") {
			t.Errorf("excluded/ package must not appear in chunks, got %+v", c)
		}
	}
}

// TestSplitChunk_AdjacencyPreserved verifies the greedy-pack
// invariant: files end up bin-mates with their input neighbours,
// not interleaved. Important because the audit pack treats a
// chunk as a "coherent unit of scope" — interleaved files would
// degrade LLM reasoning about cross-file relationships within a
// package.
func TestSplitChunk_AdjacencyPreserved(t *testing.T) {
	root := t.TempDir()
	// 2 files of 30 KiB each → fit together in a 70 KiB cap.
	// A third 30 KiB file forces a new bin.
	body := strings.Repeat("// filler\n", 3000)
	mkFile(t, root, "a.go", body)
	mkFile(t, root, "b.go", body)
	mkFile(t, root, "c.go", body)
	chunks, err := splitChunk(root, "pkg", []string{"a.go", "b.go", "c.go"}, 70*1024, nil)
	if err != nil {
		t.Fatalf("splitChunk: %v", err)
	}
	// Expected packing: [a, b] [c]. Verify the first bin contains
	// a and b (in that order), the second contains c.
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks for 3×30KiB files in 70KiB cap; got %d", len(chunks))
	}
	if len(chunks[0].Files) != 2 || chunks[0].Files[0] != "a.go" || chunks[0].Files[1] != "b.go" {
		t.Errorf("first chunk should hold [a.go, b.go] in order; got %v", chunks[0].Files)
	}
	if len(chunks[1].Files) != 1 || chunks[1].Files[0] != "c.go" {
		t.Errorf("second chunk should hold [c.go]; got %v", chunks[1].Files)
	}
}

// TestSplitChunk_OversizedSingleFileEmitsOneChunkAndWarns
// covers the no-split-mid-file invariant: a file individually
// over maxBytes goes through as a single chunk (over the cap)
// and surfaces a warning via the writer. The LLM will probably
// reject it with prompt_too_long but splitting a source file at
// an arbitrary line boundary would produce semantically broken
// chunks (split mid-function, no scope context) — worse than
// failing loudly. Caller can `--include` around the bad file.
func TestSplitChunk_OversizedSingleFileEmitsOneChunkAndWarns(t *testing.T) {
	root := t.TempDir()
	// 200 KiB file; cap 50 KiB.
	big := strings.Repeat("// huge\n", 25000)
	mkFile(t, root, "huge.go", big)
	mkFile(t, root, "small.go", "package x\n")
	var warn bytes.Buffer
	chunks, err := splitChunk(root, "pkg", []string{"huge.go", "small.go"}, 50*1024, &warn)
	if err != nil {
		t.Fatalf("splitChunk: %v", err)
	}
	// Expect: huge.go in its own bin (oversized), small.go in
	// another bin. So 2 chunks, both labelled with [part N/M].
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks (oversized huge.go + sibling small.go); got %d", len(chunks))
	}
	// One of the chunks must contain just huge.go and exceed the
	// cap. Locate it by index first, then read fields off the
	// slice — avoids holding a pointer-to-slice-element across
	// the assertions (anti-idiomatic in Go even when safe here).
	hugeIdx := -1
	for i, c := range chunks {
		for _, f := range c.Files {
			if f == "huge.go" {
				hugeIdx = i
			}
		}
	}
	if hugeIdx == -1 {
		t.Fatal("no chunk contained huge.go")
	}
	huge := chunks[hugeIdx]
	if len(huge.Files) != 1 {
		t.Errorf("oversized file should be alone in its chunk; got %v", huge.Files)
	}
	if huge.SizeBytes <= 50*1024 {
		t.Errorf("oversized chunk should exceed cap by definition; got %d", huge.SizeBytes)
	}
	// Warning must mention the oversized file by name.
	if !strings.Contains(warn.String(), "huge.go") {
		t.Errorf("warning should name the oversized file; got %q", warn.String())
	}
	if !strings.Contains(warn.String(), "cannot split") {
		t.Errorf("warning should explain why we can't split; got %q", warn.String())
	}
}

// TestSplitChunk_NoWarningWhenWriterIsNil pins the contract that
// tests can pass nil for the warn writer without triggering a nil
// deref. The audit Run path passes os.Stderr, but library
// consumers may not want progress noise.
func TestSplitChunk_NoWarningWhenWriterIsNil(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("// huge\n", 25000)
	mkFile(t, root, "huge.go", big)
	// Should not panic.
	chunks, err := splitChunk(root, "pkg", []string{"huge.go"}, 50*1024, nil)
	if err != nil {
		t.Fatalf("splitChunk with nil warn: %v", err)
	}
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk; got %d", len(chunks))
	}
}

// mkFile writes a file under root with the given relative path
// and contents. Used by the splitChunk tests to fabricate input
// trees of controlled size.
func mkFile(t *testing.T, root, name, body string) {
	t.Helper()
	full := filepath.Join(root, name)
	if dir := filepath.Dir(full); dir != root {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}
