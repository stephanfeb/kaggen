package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadTestCases loads all test cases from a directory.
// It recursively searches for .yaml and .yml files.
func LoadTestCases(dir string) ([]EvalCase, error) {
	var cases []EvalCase

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		loaded, err := LoadTestCaseFile(path)
		if err != nil {
			return fmt.Errorf("loading %s: %w", path, err)
		}

		// Set category from directory name if not specified
		relPath, _ := filepath.Rel(dir, path)
		dirName := filepath.Dir(relPath)
		for i := range loaded {
			if loaded[i].Category == "" && dirName != "." {
				loaded[i].Category = dirName
			}
		}

		cases = append(cases, loaded...)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking test directory: %w", err)
	}

	return cases, nil
}

// LoadTestCaseFile loads test cases from a single YAML file.
// The file can contain either a single case or a list of cases.
func LoadTestCaseFile(path string) ([]EvalCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	// Try parsing as a list first
	var cases []EvalCase
	if err := yaml.Unmarshal(data, &cases); err == nil && len(cases) > 0 {
		return validateCases(cases, path)
	}

	// Try parsing as a single case
	var single EvalCase
	if err := yaml.Unmarshal(data, &single); err == nil && single.ID != "" {
		return validateCases([]EvalCase{single}, path)
	}

	return nil, fmt.Errorf("could not parse as eval case(s)")
}

// validateCases ensures all cases have required fields.
func validateCases(cases []EvalCase, source string) ([]EvalCase, error) {
	for i, c := range cases {
		if c.ID == "" {
			return nil, fmt.Errorf("case %d in %s: missing 'id' field", i, source)
		}
		if c.UserMessage == "" {
			return nil, fmt.Errorf("case %q in %s: missing 'user_message' field", c.ID, source)
		}
		if len(c.Assert) == 0 {
			return nil, fmt.Errorf("case %q in %s: missing 'assert' field", c.ID, source)
		}
		for j, a := range c.Assert {
			if a.Type == "" {
				return nil, fmt.Errorf("case %q assertion %d in %s: missing 'type' field", c.ID, j, source)
			}
		}
	}
	return cases, nil
}

// FilterByCategory returns only cases matching the given category.
func FilterByCategory(cases []EvalCase, category string) []EvalCase {
	if category == "" {
		return cases
	}
	var filtered []EvalCase
	for _, c := range cases {
		if c.Category == category {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// FilterByIDs returns only cases with IDs in the given list.
func FilterByIDs(cases []EvalCase, ids []string) []EvalCase {
	if len(ids) == 0 {
		return cases
	}
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	var filtered []EvalCase
	for _, c := range cases {
		if idSet[c.ID] {
			filtered = append(filtered, c)
		}
	}
	return filtered
}
