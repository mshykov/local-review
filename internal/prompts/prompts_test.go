package prompts

import (
	"strings"
	"testing"
)

func TestGetKnown(t *testing.T) {
	cases := []string{"default", "typescript", "go", "python", "rust"}
	for _, lang := range cases {
		body, err := Get(lang)
		if err != nil {
			t.Errorf("Get(%q) error: %v", lang, err)
			continue
		}
		if !strings.Contains(body, "review") && !strings.Contains(body, "Review") {
			t.Errorf("Get(%q) doesn't mention review — empty pack?", lang)
		}
	}
}

func TestGetUnknownFallsBack(t *testing.T) {
	body, err := Get("haskell")
	if err != nil {
		t.Fatalf("Get(unknown) should fall back, got error: %v", err)
	}
	def, _ := Get("default")
	if body != def {
		t.Error("unknown language did not fall back to default pack")
	}
}

func TestAvailable(t *testing.T) {
	ids, err := Available()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) < 2 {
		t.Errorf("expected at least default + one language pack, got %d: %v", len(ids), ids)
	}
}
