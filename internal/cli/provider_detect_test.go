package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// DetectProviders runs the HTTP probe per spec and returns one LLM per
// spec with Available set according to the outcome. Pin the basic
// contract: shape preserved, name/baseURL/model carried through,
// reachable → Available=true, unreachable → Available=false.
func TestDetectProviders_ReachableMarksAvailable(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen2.5-coder:7b"}]}`))
	}))
	t.Cleanup(good.Close)

	specs := []ProviderSpec{
		{Name: "qwen", BaseURL: good.URL, Model: "qwen2.5-coder:7b"},
		// 127.0.0.1:1 is reliably refused (no listener) — fast-fail.
		{Name: "unreachable", BaseURL: "http://127.0.0.1:1/v1", Model: "x"},
	}
	got := DetectProviders(context.Background(), specs)
	if len(got) != 2 {
		t.Fatalf("len: want 2, got %d", len(got))
	}

	// Order preserved (parallel detection still slots results by input index).
	if got[0].Name != "qwen" || got[1].Name != "unreachable" {
		t.Errorf("order not preserved: %+v", got)
	}
	if !got[0].Available {
		t.Errorf("reachable endpoint must be Available=true; got %+v", got[0])
	}
	if got[1].Available {
		t.Errorf("unreachable endpoint must be Available=false; got %+v", got[1])
	}

	// BaseURL + Model carried through so cli.NewInvoker can build a
	// provider.Invoker without a second config lookup.
	if got[0].BaseURL != good.URL || got[0].Model != "qwen2.5-coder:7b" {
		t.Errorf("provider fields not carried through: %+v", got[0])
	}
}

func TestDetectProviders_EmptySliceReturnsEmptyNotNil(t *testing.T) {
	got := DetectProviders(context.Background(), nil)
	if got == nil {
		t.Error("DetectProviders(nil) should return empty slice, not nil — callers append without nil-check")
	}
	if len(got) != 0 {
		t.Errorf("len: want 0, got %d", len(got))
	}
}
