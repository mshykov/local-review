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
	colorReset    = "\033[0m"
	colorBold     = "\033[1m"
	colorDim      = "\033[2m"
	colorRed      = "\033[31m"
	colorYellow   = "\033[33m"
	colorBlue     = "\033[34m"
	colorMagenta  = "\033[35m"
	colorCyan     = "\033[36m"
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

// WriteText renders a Report to w as terminal-friendly text.
func WriteText(w io.Writer, r review.Report) {
	color := useColor()
	if len(r.Findings) == 0 {
		fmt.Fprintf(w, "%sno findings%s — %d files reviewed via %s\n",
			ifColor(color, colorDim), reset(color), r.Meta.Files, r.Meta.Model)
		return
	}

	fmt.Fprintf(w, "%s%d finding(s)%s — %d files reviewed via %s\n\n",
		ifColor(color, colorBold), len(r.Findings), reset(color), r.Meta.Files, r.Meta.Model)

	for _, f := range r.Findings {
		sevC := severityColor(f.Severity, color)
		// Severity tag + location
		fmt.Fprintf(w, "%s[%s]%s %s%s%s\n",
			sevC, f.Severity, reset(color),
			ifColor(color, colorCyan), f.Loc(), reset(color))
		// Title
		fmt.Fprintf(w, "  %s%s%s\n", ifColor(color, colorBold), f.Title, reset(color))
		// Body
		if f.Body != "" {
			fmt.Fprintf(w, "  %s\n", f.Body)
		}
		// Optional tag
		if f.Tag != "" {
			fmt.Fprintf(w, "  %s#%s%s\n", ifColor(color, colorMagenta), f.Tag, reset(color))
		}
		fmt.Fprintln(w)
	}
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
