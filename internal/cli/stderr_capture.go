package cli

import (
	"io"
	"sync"
	"sync/atomic"
)

// partialStderrField is the common state + PartialStderr() impl
// embedded in every invoker (Claude / Gemini / Codex). Pulling
// the atomic.Pointer + 5-line method out into a shared embedding
// instead of pasting it three times once made Sonar flag PR #91
// for 6.5% duplication on new code (threshold ≤ 3%) — and it WAS
// real duplication; Sonar caught a legitimate copy-paste.
//
// Each invoker embeds this struct unnamed, which auto-promotes
// the `capture` field and `PartialStderr()` method to the
// invoker's public API. The capture field stays goroutine-safe
// via atomic.Pointer because PartialStderr() can be called from
// the probe goroutine concurrently with run() storing a fresh
// buffer.
type partialStderrField struct {
	capture atomic.Pointer[stderrCapture]
}

// PartialStderr implements PartialStderrCapturer for any invoker
// that embeds partialStderrField. Returns whatever's been
// buffered from the most-recent subprocess invocation, or "" if
// no run has started yet (capture pointer is still nil).
func (p *partialStderrField) PartialStderr() string {
	if c := p.capture.Load(); c != nil {
		return c.Snapshot()
	}
	return ""
}

// teeStderr installs a fresh capture into the embedded field and
// returns an io.Writer that tees subprocess stderr through both
// the caller's existing destination AND the live capture. Used
// by each invoker's run() at the cmd.Stderr assignment site so
// the three-step "make capture / store pointer / wrap in
// MultiWriter" pattern lives in one place rather than three
// copy-pasted blocks (Sonar flagged 6.4% duplication on new
// code the first time this was inlined per-invoker — the
// extraction here is the dedup fix).
func (p *partialStderrField) teeStderr(stderrSink io.Writer) io.Writer {
	capture := newStderrCapture(0) // 0 → default 4 KiB cap
	p.capture.Store(capture)
	return io.MultiWriter(stderrSink, capture)
}

// stderrCapture is an io.Writer that retains the first capBytes of
// output, dropping the tail. Goroutine-safe so the invoker's
// subprocess can write to it while the probe layer simultaneously
// peeks the buffer (v0.10.6: surface the vendor's actual error
// message — "You have exhausted your capacity on this model.",
// "auth failed", etc. — in the readiness block when a probe times
// out, instead of the generic "timeout after 10s").
//
// Why first-bytes (not last-bytes): vendor CLIs typically print
// the diagnostic line FIRST, then either retry or hang on the
// network call. The error message we care about is the leading
// content; later output is usually noise (progress dots, banner
// strings, the actual response stream after recovery). Capping at
// the head also lets the buffer go memory-cold once the cap is
// hit — Write becomes O(1) discard.
//
// Cap of 4 KiB is deliberately generous: most CLI errors are <100
// bytes, but stack traces and multi-line "here's what's wrong"
// messages can run to a few KB. Larger caps risk leaking actual
// review content (which we DO NOT want in the readiness block);
// 4 KiB is enough headroom for any realistic startup-time
// diagnostic without bleeding into review payloads.
type stderrCapture struct {
	mu       sync.Mutex
	buf      []byte
	capBytes int
}

// newStderrCapture constructs a capture with a hard byte cap.
// capBytes <= 0 falls back to a sensible 4 KiB default — caller
// errors here would be silent bugs (no-op buffer, never any
// captured content) which is worse than a default.
func newStderrCapture(capBytes int) *stderrCapture {
	if capBytes <= 0 {
		capBytes = 4 * 1024
	}
	return &stderrCapture{capBytes: capBytes}
}

// Write implements io.Writer. Copies up to capBytes total across
// all calls; excess bytes are silently discarded (the cap is the
// behaviour, not a failure).
func (c *stderrCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	remaining := c.capBytes - len(c.buf)
	if remaining <= 0 {
		// Past the cap — Write returns the full input length so
		// io.Copy / cmd's own writer don't perceive a short
		// write (which would surface as an error inside cmd.Run
		// despite the data being intentionally dropped).
		return len(p), nil
	}
	if len(p) > remaining {
		c.buf = append(c.buf, p[:remaining]...)
		return len(p), nil
	}
	c.buf = append(c.buf, p...)
	return len(p), nil
}

// Snapshot returns a copy of the currently-captured bytes. The
// return is a fresh slice so callers can hold it past subsequent
// Write calls without worrying about the underlying array growing
// or being reallocated.
func (c *stderrCapture) Snapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}
