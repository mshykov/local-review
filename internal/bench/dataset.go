package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadDataset reads every case under root and returns them sorted by
// ID for deterministic ordering. A "case" is a directory containing a
// case.yaml + diff.patch pair; anything else (loose files, READMEs,
// hidden directories) is silently skipped.
//
// Returns an error if root doesn't exist or no cases were found —
// "ran the bench against an empty dataset" is almost always a wrong
// path, and silent zero-result reports would mask it.
func LoadDataset(root string) ([]Case, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("dataset root %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("dataset root %q is not a directory", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read dataset root %q: %w", root, err)
	}

	var cases []Case
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		caseDir := filepath.Join(root, e.Name())
		c, err := LoadCase(caseDir)
		if err != nil {
			// Surface case-level errors with the directory name attached
			// so the user can fix the offending file without re-running
			// the whole bench. We bail on the first error rather than
			// continuing — a malformed case is almost always a typo
			// that the user wants to know about immediately.
			return nil, fmt.Errorf("load case %q: %w", e.Name(), err)
		}
		cases = append(cases, c)
	}

	if len(cases) == 0 {
		return nil, fmt.Errorf("no cases found under %q (each case is a subdirectory with case.yaml + diff.patch)", root)
	}

	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

// LoadCase reads a single case directory.
func LoadCase(dir string) (Case, error) {
	yamlPath := filepath.Join(dir, "case.yaml")
	diffPath := filepath.Join(dir, "diff.patch")

	yamlBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		return Case{}, fmt.Errorf("read case.yaml: %w", err)
	}
	var c Case
	if err := yaml.Unmarshal(yamlBytes, &c); err != nil {
		return Case{}, fmt.Errorf("parse case.yaml: %w", err)
	}

	// Default ID to the directory name so case.yaml doesn't have to
	// repeat itself. Explicit "id:" wins if set.
	if c.ID == "" {
		c.ID = filepath.Base(dir)
	}

	diffBytes, err := os.ReadFile(diffPath)
	if err != nil {
		return Case{}, fmt.Errorf("read diff.patch: %w", err)
	}
	c.Diff = string(diffBytes)
	c.DiffPath = diffPath

	if c.Clean && len(c.Expected) > 0 {
		return Case{}, fmt.Errorf("case %q is clean=true but has %d expected findings; clean cases must have no expected findings", c.ID, len(c.Expected))
	}
	if !c.Clean && len(c.Expected) == 0 {
		return Case{}, fmt.Errorf("case %q has no expected findings and clean=false; either label findings or set clean: true", c.ID)
	}
	for i, ef := range c.Expected {
		if ef.File == "" {
			return Case{}, fmt.Errorf("case %q expected[%d]: file is required", c.ID, i)
		}
		if ef.Line < 0 {
			return Case{}, fmt.Errorf("case %q expected[%d]: line must be >= 0 (got %d)", c.ID, i, ef.Line)
		}
	}

	return c, nil
}
