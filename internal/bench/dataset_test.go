package bench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCase_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	caseDir := filepath.Join(dir, "case-1")
	if err := os.Mkdir(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	yaml := `id: case-1
title: A Go bug
language: go
expected:
  - file: foo.go
    line: 42
    category: correctness
    note: nil deref
`
	if err := os.WriteFile(filepath.Join(caseDir, "case.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "diff.patch"), []byte("--- a/foo.go\n+++ b/foo.go\n@@ -1 +1 @@\n-x\n+y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := LoadCase(caseDir)
	if err != nil {
		t.Fatalf("LoadCase: %v", err)
	}
	if c.ID != "case-1" || c.Language != "go" || c.Title != "A Go bug" {
		t.Errorf("metadata: %+v", c)
	}
	if len(c.Expected) != 1 || c.Expected[0].File != "foo.go" || c.Expected[0].Line != 42 {
		t.Errorf("expected: %+v", c.Expected)
	}
	if c.Diff == "" {
		t.Errorf("diff body not loaded")
	}
}

// writeCase is a t.Helper that creates one case directory under dir
// with the given subdir name, case.yaml body, and diff.patch body.
// Setup errors fail the test immediately rather than being silently
// swallowed — without this a dataset that failed to materialize
// (transient FS error, restrictive umask, etc.) would make a "no
// cases" / "duplicate id" test pass for the wrong reason.
func writeCase(t *testing.T, dir, subdir, yamlBody, patchBody string) {
	t.Helper()
	cd := filepath.Join(dir, subdir)
	if err := os.Mkdir(cd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cd, "case.yaml"), []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cd, "diff.patch"), []byte(patchBody), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCase_RejectsCleanWithExpected(t *testing.T) {
	dir := t.TempDir()
	writeCase(t, dir, "bad", `clean: true
expected:
  - file: foo.go
    line: 1
`, "x")

	_, err := LoadCase(filepath.Join(dir, "bad"))
	if err == nil {
		t.Fatal("expected error for clean=true with non-empty expected")
	}
	if !strings.Contains(err.Error(), "clean") {
		t.Errorf("error should mention 'clean', got: %v", err)
	}
}

func TestLoadCase_RejectsNonCleanWithoutExpected(t *testing.T) {
	dir := t.TempDir()
	writeCase(t, dir, "bad2", `title: empty case`, "x")

	_, err := LoadCase(filepath.Join(dir, "bad2"))
	if err == nil {
		t.Fatal("expected error for non-clean case with no expected findings")
	}
	if !strings.Contains(err.Error(), "expected") && !strings.Contains(err.Error(), "clean") {
		t.Errorf("error should mention 'expected' or 'clean', got: %v", err)
	}
}

func TestLoadDataset_SortsAndSkipsHidden(t *testing.T) {
	dir := t.TempDir()
	mkCase := func(id string, clean bool) {
		yamlBody := "title: " + id + "\n"
		if clean {
			yamlBody += "clean: true\n"
		} else {
			yamlBody += "expected:\n  - file: x.go\n    line: 1\n"
		}
		writeCase(t, dir, id, yamlBody, "x")
	}
	mkCase("z-last", false)
	mkCase("a-first", false)
	mkCase("clean-1", true)

	// Hidden directory should be ignored.
	if err := os.Mkdir(filepath.Join(dir, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases, err := LoadDataset(dir)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if len(cases) != 3 {
		t.Fatalf("expected 3 cases, got %d", len(cases))
	}
	want := []string{"a-first", "clean-1", "z-last"}
	for i, c := range cases {
		if c.ID != want[i] {
			t.Errorf("cases[%d].ID = %q, want %q", i, c.ID, want[i])
		}
	}
}

func TestLoadDataset_EmptyIsError(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadDataset(dir)
	if err == nil {
		t.Fatal("expected error for empty dataset directory")
	}
	if !strings.Contains(err.Error(), "no cases") {
		t.Errorf("error should mention 'no cases', got: %v", err)
	}
}

func TestLoadDataset_DuplicateIDIsError(t *testing.T) {
	dir := t.TempDir()
	writeCase(t, dir, "dir-a", "id: shared-id\nclean: true\n", "x")
	writeCase(t, dir, "dir-b", "id: shared-id\nclean: true\n", "x")

	_, err := LoadDataset(dir)
	if err == nil {
		t.Fatal("expected error when two case directories declare the same id")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention 'duplicate', got: %v", err)
	}
}
