package output

import (
	"errors"
	"io"
	"testing"

	"github.com/mshykov/local-review/internal/review"
)

// failingWriter returns the configured error after `succeedFor` bytes
// have been written. Lets a single test exercise both the immediate-fail
// path and the partial-write-then-fail path.
type failingWriter struct {
	succeedFor int
	written    int
	err        error
}

func (fw *failingWriter) Write(p []byte) (int, error) {
	if fw.written >= fw.succeedFor {
		return 0, fw.err
	}
	n := len(p)
	if fw.written+n > fw.succeedFor {
		n = fw.succeedFor - fw.written
	}
	fw.written += n
	// io.Writer contract: a short write (n < len(p)) MUST return a
	// non-nil error. Returning (n, nil) here would let fmt.Fprintf
	// callers treat truncated writes as success and the error
	// propagation we're testing wouldn't fire. Use io.ErrShortWrite
	// so the assertion still catches via errors.Is(err, fw.err) when
	// fw.err is set by the caller.
	if n < len(p) {
		if fw.err != nil {
			return n, fw.err
		}
		return n, io.ErrShortWrite
	}
	return n, nil
}

func TestWriteText_ReturnsWriteError(t *testing.T) {
	// Previously this was fire-and-forget — broken-pipe / disk-full
	// would silently exit 0 even though no findings reached the user.
	// Pin the new contract: the first underlying error propagates.
	rep := review.Report{
		Findings: []review.Finding{
			{File: "a.go", Line: 1, Severity: review.SeverityMajor, Title: "x", Body: "y"},
		},
		Meta: review.ReportMeta{Files: 1, Model: "test"},
	}
	want := errors.New("disk full")
	fw := &failingWriter{succeedFor: 0, err: want}

	got := WriteText(fw, rep)
	if !errors.Is(got, want) {
		t.Errorf("WriteText error: got %v, want %v", got, want)
	}
}

func TestWriteText_NoFindingsAlsoPropagates(t *testing.T) {
	// Empty-report path takes a shorter codepath; make sure it threads
	// the error too, not just the loop body.
	rep := review.Report{Meta: review.ReportMeta{Files: 0, Model: "test"}}
	want := errors.New("broken pipe")
	fw := &failingWriter{succeedFor: 0, err: want}
	if got := WriteText(fw, rep); !errors.Is(got, want) {
		t.Errorf("empty-report path: got %v, want %v", got, want)
	}
}

func TestWriteText_SuccessReturnsNil(t *testing.T) {
	// Sanity: happy path doesn't accidentally return a stale error.
	rep := review.Report{Meta: review.ReportMeta{Files: 0, Model: "test"}}
	fw := &failingWriter{succeedFor: 1 << 16, err: errors.New("never reached")}
	if err := WriteText(fw, rep); err != nil {
		t.Errorf("expected nil error on success, got %v", err)
	}
}
