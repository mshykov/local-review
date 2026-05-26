package cli

import (
	"strings"
	"sync"
	"testing"
)

// stderrCapture is goroutine-safe by contract; pin that via the
// race detector. Without the mutex, this test triggers `go test
// -race` failures on concurrent Write + Snapshot.
func TestStderrCapture_ConcurrentWriteSnapshot(t *testing.T) {
	c := newStderrCapture(1024)

	var wg sync.WaitGroup
	writers := 8
	writes := 100
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < writes; i++ {
				_, _ = c.Write([]byte("xx"))
			}
		}()
	}

	// While writers are running, take snapshots concurrently.
	readers := 4
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < writes; i++ {
				_ = c.Snapshot()
			}
		}()
	}

	wg.Wait()
	// Capped at 1024 bytes; 8 writers × 100 writes × 2 bytes =
	// 1600 attempted, capture must cap.
	if got := len(c.Snapshot()); got > 1024 {
		t.Errorf("Snapshot len = %d, expected ≤ 1024 (cap respected)", got)
	}
}

func TestStderrCapture_CapDiscardsExcess(t *testing.T) {
	c := newStderrCapture(10)
	n, err := c.Write([]byte("0123456789ABCDEF"))
	if err != nil {
		t.Errorf("Write returned err: %v", err)
	}
	// Write must report it accepted the full input length even
	// though most was discarded — otherwise cmd.Run / io.Copy
	// would treat the cap as a write error.
	if n != 16 {
		t.Errorf("Write n = %d, want 16 (cap should report full length, not actual buffered)", n)
	}
	if got, want := c.Snapshot(), "0123456789"; got != want {
		t.Errorf("Snapshot = %q, want %q (cap kept the first 10 bytes only)", got, want)
	}

	// Subsequent writes after cap fill must also report full
	// length and not append anything.
	n, _ = c.Write([]byte("ZZZ"))
	if n != 3 {
		t.Errorf("post-cap Write n = %d, want 3", n)
	}
	if got, want := c.Snapshot(), "0123456789"; got != want {
		t.Errorf("Snapshot after post-cap write = %q, want %q (unchanged)", got, want)
	}
}

// Empty case: a brand-new capture has empty Snapshot, which is
// what PartialStderr() returns when the invoker hasn't run yet.
// Probe checks `strings.TrimSpace(...) != ""` so the empty case
// falls through to the generic "timeout after Ns" message — the
// contract we test by inspecting Snapshot directly here.
func TestStderrCapture_EmptySnapshot(t *testing.T) {
	c := newStderrCapture(0) // 0 → defaults to 4 KiB
	if got := c.Snapshot(); got != "" {
		t.Errorf("Snapshot on empty capture = %q, want \"\"", got)
	}
	if strings.TrimSpace(c.Snapshot()) != "" {
		t.Errorf("post-trim should still be empty")
	}
}

func TestStderrCapture_ZeroCapFallsBackToDefault(t *testing.T) {
	c := newStderrCapture(0)
	// Write 5 KiB; default cap is 4 KiB, so we should see the
	// first 4 KiB only.
	big := strings.Repeat("a", 5*1024)
	_, _ = c.Write([]byte(big))
	if got := len(c.Snapshot()); got != 4*1024 {
		t.Errorf("Snapshot len = %d, want 4096 (default cap)", got)
	}
}
