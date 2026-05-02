package lang

import "testing"

func TestDetect(t *testing.T) {
	cases := map[string]string{
		"src/foo.ts":      TypeScript,
		"src/Foo.TSX":     TypeScript,
		"main.go":         Go,
		"util.py":         Python,
		"App.java":        Java,
		"lib.rs":          Rust,
		"unknown.xyz":     Default,
		"no-extension":    Default,
		"path/to/file.js": JavaScript,
	}
	for path, want := range cases {
		if got := Detect(path); got != want {
			t.Errorf("Detect(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestDominant(t *testing.T) {
	files := []string{"a.go", "b.go", "c.ts"}
	if got := Dominant(files); got != Go {
		t.Errorf("Dominant Go-heavy = %q, want %q", got, Go)
	}
	if got := Dominant(nil); got != Default {
		t.Errorf("Dominant empty = %q, want %q", got, Default)
	}
	// Tie-break is alphabetical
	tie := []string{"a.go", "b.ts"}
	if got := Dominant(tie); got != Go { // "go" < "typescript"
		t.Errorf("Dominant tie = %q, want %q", got, Go)
	}
}
