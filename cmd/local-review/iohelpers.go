package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// writeFileWithDirs creates `path` (and any missing parent directories),
// invokes `emit` with the open file, and propagates either the emit
// error OR a close-failure-on-otherwise-success error. Centralises the
// directory-prepare + create + deferred-close-with-error-check sequence
// that three writers — `writeAuditFile` (cmd/local-review/audit.go),
// `writeBenchToFile` and `writeSWEBenchToFile` (cmd/local-review/bench.go)
// — implemented identically pre-consolidation. Each writer picks its
// own wire format (markdown vs JSON) inside the `emit` closure it
// passes in.
//
// audit/tech-debt.md flagged the duplication on bench.go:195 with the
// suggestion: "Extract to a shared writeFileWithDirs(path string,
// emit func(io.Writer) error) helper in this package, called by all
// three." That signature is exactly what we land here.
//
// retErr posture: the close error doesn't shadow the emit error.
// When emit returns a non-nil error AND f.Close() also fails, the
// caller sees emit's error (the more proximate cause); the close
// error is silently swallowed — matching the pre-consolidation
// behaviour of all three writers, which used the same
// `if cerr != nil && retErr == nil` guard. The dropped close error
// would not be actionable on top of the already-failed emit anyway.
//
// Permission: 0o755 on directories matches the project default for
// `.local-review/`, `audit/`, and any user-supplied --out target.
// File perms come from os.Create — which calls open(2) with mode
// 0o666, then the process umask (typically 022) masks it down to
// 0o644 on most systems. Not pinned here; the three pre-
// consolidation writers all called os.Create the same way, so the
// effective on-disk perm is unchanged from v0.10.1.
func writeFileWithDirs(path string, emit func(io.Writer) error) (retErr error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()
	return emit(f)
}
