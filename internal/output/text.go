// Package output renders a Report into terminal-friendly text or JSON.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/mshykov/local-review/internal/review"
)

const (
	colorReset   = "\033[0m"
	colorBold    = "\033[1m"
	colorDim     = "\033[2m"
	colorRed     = "\033[31m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
)

// useColor returns true when stdout is a TTY and NO_COLOR is not set.
// Crude check; good enough — most CI captures stdout and forces a pipe.
func useColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func severityColor(sev review.Severity, on bool) string {
	if !on {
		return ""
	}
	switch sev {
	case review.SeverityCritical:
		return colorRed + colorBold
	case review.SeverityMajor:
		return colorRed
	case review.SeverityWarning:
		return colorYellow
	case review.SeverityInfo:
		return colorBlue
	default:
		return colorDim
	}
}

func reset(on bool) string {
	if on {
		return colorReset
	}
	return ""
}

// WriteText renders a Report to w as terminal-friendly text. Returns
// the first I/O error from the underlying writer. Previously this was
// fire-and-forget — a broken-pipe (`local-review staged | head`) or
// disk-full (`> /dev/full`) would silently exit 0 even though no
// findings reached the user. Mirrors the errWriter pattern doctor.go
// uses so callers get a single error to check at the end.
func WriteText(w io.Writer, r review.Report) error {
	ew := &errWriter{w: w}
	color := useColor()
	if len(r.Findings) == 0 {
		fmt.Fprintf(ew, "%sno findings%s — %d files reviewed via %s\n",
			ifColor(color, colorDim), reset(color), r.Meta.Files, r.Meta.Model)
		return ew.err
	}

	fmt.Fprintf(ew, "%s%d finding(s)%s — %d files reviewed via %s\n\n",
		ifColor(color, colorBold), len(r.Findings), reset(color), r.Meta.Files, r.Meta.Model)

	for _, f := range r.Findings {
		sevC := severityColor(f.Severity, color)
		// Severity tag + location
		fmt.Fprintf(ew, "%s[%s]%s %s%s%s\n",
			sevC, f.Severity, reset(color),
			ifColor(color, colorCyan), f.Loc(), reset(color))
		// Title
		fmt.Fprintf(ew, "  %s%s%s\n", ifColor(color, colorBold), f.Title, reset(color))
		// Body
		if f.Body != "" {
			fmt.Fprintf(ew, "  %s\n", f.Body)
		}
		// Optional tag
		if f.Tag != "" {
			fmt.Fprintf(ew, "  %s#%s%s\n", ifColor(color, colorMagenta), f.Tag, reset(color))
		}
		fmt.Fprintln(ew)
	}
	return ew.err
}

// errWriter is an io.Writer that captures the first error from the
// underlying writer and short-circuits subsequent writes — same shape
// as the doctor's errWriter so multi-line output can ignore the per-
// fmt.Fprintf return values and check one error at the end.
type errWriter struct {
	w   io.Writer
	err error
}

func (ew *errWriter) Write(p []byte) (int, error) {
	if ew.err != nil {
		return 0, ew.err
	}
	n, err := ew.w.Write(p)
	if err != nil {
		ew.err = err
	}
	return n, err
}

// WriteJSON renders a Report as machine-readable JSON.
func WriteJSON(w io.Writer, r review.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func ifColor(on bool, code string) string {
	if on {
		return code
	}
	return ""
}
