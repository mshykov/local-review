package bench

import (
	"os"
	"path/filepath"
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

func TestLoadCase_RejectsCleanWithExpected(t *testing.T) {
	dir := t.TempDir()
	caseDir := filepath.Join(dir, "bad")
	_ = os.Mkdir(caseDir, 0o755)
	_ = os.WriteFile(filepath.Join(caseDir, "case.yaml"), []byte(`clean: true
expected:
  - file: foo.go
    line: 1
`), 0o644)
	_ = os.WriteFile(filepath.Join(caseDir, "diff.patch"), []byte("x"), 0o644)

	if _, err := LoadCase(caseDir); err == nil {
		t.Fatal("expected error for clean=true with non-empty expected")
	}
}

func TestLoadCase_RejectsNonCleanWithoutExpected(t *testing.T) {
	dir := t.TempDir()
	caseDir := filepath.Join(dir, "bad2")
	_ = os.Mkdir(caseDir, 0o755)
	_ = os.WriteFile(filepath.Join(caseDir, "case.yaml"), []byte(`title: empty case`), 0o644)
	_ = os.WriteFile(filepath.Join(caseDir, "diff.patch"), []byte("x"), 0o644)

	if _, err := LoadCase(caseDir); err == nil {
		t.Fatal("expected error for non-clean case with no expected findings")
	}
}

func TestLoadDataset_SortsAndSkipsHidden(t *testing.T) {
	dir := t.TempDir()
	mkCase := func(id string, clean bool) {
		cd := filepath.Join(dir, id)
		_ = os.Mkdir(cd, 0o755)
		yamlBody := "title: " + id + "\n"
		if clean {
			yamlBody += "clean: true\n"
		} else {
			yamlBody += "expected:\n  - file: x.go\n    line: 1\n"
		}
		_ = os.WriteFile(filepath.Join(cd, "case.yaml"), []byte(yamlBody), 0o644)
		_ = os.WriteFile(filepath.Join(cd, "diff.patch"), []byte("x"), 0o644)
	}
	mkCase("z-last", false)
	mkCase("a-first", false)
	mkCase("clean-1", true)

	// Hidden directory should be ignored.
	_ = os.Mkdir(filepath.Join(dir, ".cache"), 0o755)

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
	if _, err := LoadDataset(dir); err == nil {
		t.Fatal("expected error for empty dataset directory")
	}
}

func TestLoadDataset_DuplicateIDIsError(t *testing.T) {
	dir := t.TempDir()
	mk := func(subdir, id string) {
		cd := filepath.Join(dir, subdir)
		_ = os.Mkdir(cd, 0o755)
		yamlBody := "id: " + id + "\nclean: true\n"
		_ = os.WriteFile(filepath.Join(cd, "case.yaml"), []byte(yamlBody), 0o644)
		_ = os.WriteFile(filepath.Join(cd, "diff.patch"), []byte("x"), 0o644)
	}
	mk("dir-a", "shared-id")
	mk("dir-b", "shared-id")

	_, err := LoadDataset(dir)
	if err == nil {
		t.Fatal("expected error when two case directories declare the same id")
	}
}
