package model

import "testing"

func TestDetectFamily(t *testing.T) {
	tests := []struct {
		modelString string
		expected    Family
	}{
		{"anthropic/claude-sonnet-4-20250514", FamilyAnthropic},
		{"anthropic/claude-opus-4-5-20251101", FamilyAnthropic},
		{"gemini/gemini-2.0-flash", FamilyGemini},
		{"gemini/gemini-2.5-pro-preview-06-05", FamilyGemini},
		{"zai/glm-4.7", FamilyZAI},
		{"zai/custom-model", FamilyZAI},
		{"claude-sonnet-4-20250514", FamilyAnthropic}, // No prefix defaults to Anthropic
		{"unknown-model", FamilyAnthropic},            // Unknown defaults to Anthropic
		{"", FamilyAnthropic},                         // Empty defaults to Anthropic
	}

	for _, tc := range tests {
		t.Run(tc.modelString, func(t *testing.T) {
			result := DetectFamily(tc.modelString)
			if result != tc.expected {
				t.Errorf("DetectFamily(%q) = %q, expected %q", tc.modelString, result, tc.expected)
			}
		})
	}
}

func TestTier2ModelForFamily(t *testing.T) {
	tests := []struct {
		family   Family
		expected string
	}{
		{FamilyAnthropic, "anthropic/claude-opus-4-5-20251101"},
		{FamilyGemini, "gemini/gemini-2.5-pro-preview-06-05"},
		{FamilyZAI, "zai/glm-4.7"},
	}

	for _, tc := range tests {
		t.Run(string(tc.family), func(t *testing.T) {
			result := Tier2ModelForFamily(tc.family)
			if result != tc.expected {
				t.Errorf("Tier2ModelForFamily(%q) = %q, expected %q", tc.family, result, tc.expected)
			}
		})
	}
}

func TestTier2Defaults(t *testing.T) {
	// Ensure all families have a Tier 2 default
	families := []Family{FamilyAnthropic, FamilyGemini, FamilyZAI}
	for _, f := range families {
		if _, ok := Tier2Defaults[f]; !ok {
			t.Errorf("Tier2Defaults missing entry for family %q", f)
		}
	}

	// Ensure Tier 2 defaults are non-empty
	for family, model := range Tier2Defaults {
		if model == "" {
			t.Errorf("Tier2Defaults[%q] is empty", family)
		}
	}
}
