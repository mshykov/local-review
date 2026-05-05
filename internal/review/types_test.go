package review

import (
	"encoding/json"
	"testing"
)

func TestSeverityJSONRoundTrip(t *testing.T) {
	cases := []Severity{
		SeverityNit,
		SeverityInfo,
		SeverityWarning,
		SeverityMajor,
		SeverityCritical,
	}
	for _, in := range cases {
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("marshal %v: %v", in, err)
		}
		want := `"` + in.String() + `"`
		if string(data) != want {
			t.Errorf("marshal %v = %s, want %s", in, data, want)
		}
		var got Severity
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", data, err)
		}
		if got != in {
			t.Errorf("round-trip %v -> %s -> %v", in, data, got)
		}
	}
}

func TestSeverityUnmarshalUnknown(t *testing.T) {
	// Unknown strings *promote* to major — fail-closed for the gate.
	// LLM typos ("criticl", "sev-high", "BLOCKER") used to silently
	// demote to warning, hiding real blocking findings from the
	// pre-commit hook. We'd rather over-block a malformed finding than
	// under-block a real one.
	var got Severity
	if err := json.Unmarshal([]byte(`"bogus"`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != SeverityMajor {
		t.Errorf("unknown -> %v, want %v (fail-closed)", got, SeverityMajor)
	}
}

func TestSeverityUnmarshalNumericLegacy(t *testing.T) {
	// Backward compat: pre-MarshalJSON --json output and any consumer
	// re-emitting numeric severity must still decode cleanly.
	cases := []struct {
		in   string
		want Severity
	}{
		{"0", SeverityNit},
		{"1", SeverityInfo},
		{"2", SeverityWarning},
		{"3", SeverityMajor},
		{"4", SeverityCritical},
	}
	for _, tc := range cases {
		var got Severity
		if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
			t.Errorf("unmarshal %s: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("unmarshal %s = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSeverityUnmarshalNumericOutOfRangeClamps(t *testing.T) {
	// Out-of-range integers clamp to nearest tier rather than producing
	// garbage values that would later break severity comparisons.
	tests := []struct {
		in   string
		want Severity
	}{
		{"-5", SeverityNit},
		{"99", SeverityCritical},
	}
	for _, tc := range tests {
		var got Severity
		if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
			t.Errorf("unmarshal %s: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("unmarshal %s = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSeverityUnmarshalRejectsGarbage(t *testing.T) {
	// Anything that's neither a string nor a number should fail loudly.
	var got Severity
	err := json.Unmarshal([]byte(`{"oh": "no"}`), &got)
	if err == nil {
		t.Errorf("expected error for object input, got %v", got)
	}
}

func TestFindingJSONUsesSeverityString(t *testing.T) {
	f := Finding{File: "a.go", Severity: SeverityMajor, Title: "t", Body: "b"}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Severity != "major" {
		t.Errorf("severity field = %q, want %q", decoded.Severity, "major")
	}
}
