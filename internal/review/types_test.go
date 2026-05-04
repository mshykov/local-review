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
	// Unknown strings demote to warning, matching ParseSeverity.
	var got Severity
	if err := json.Unmarshal([]byte(`"bogus"`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != SeverityWarning {
		t.Errorf("unknown -> %v, want %v", got, SeverityWarning)
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
