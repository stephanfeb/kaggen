package config

import "testing"

func TestReasoningTier2Model(t *testing.T) {
	tests := []struct {
		name             string
		configTier2Model string
		coordinatorModel string
		expected         string
	}{
		{
			name:             "explicit override",
			configTier2Model: "custom/model",
			coordinatorModel: "gemini/gemini-2.0-flash",
			expected:         "custom/model",
		},
		{
			name:             "anthropic coordinator auto-select",
			configTier2Model: "",
			coordinatorModel: "anthropic/claude-sonnet-4-20250514",
			expected:         "anthropic/claude-opus-4-5-20251101",
		},
		{
			name:             "gemini coordinator auto-select",
			configTier2Model: "",
			coordinatorModel: "gemini/gemini-2.0-flash",
			expected:         "gemini/gemini-2.5-pro-preview-06-05",
		},
		{
			name:             "zai coordinator auto-select",
			configTier2Model: "",
			coordinatorModel: "zai/glm-4.7",
			expected:         "zai/glm-4.7",
		},
		{
			name:             "no prefix defaults to anthropic",
			configTier2Model: "",
			coordinatorModel: "claude-sonnet-4",
			expected:         "anthropic/claude-opus-4-5-20251101",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Reasoning: ReasoningConfig{
					Tier2Model: tc.configTier2Model,
				},
			}
			result := cfg.ReasoningTier2Model(tc.coordinatorModel)
			if result != tc.expected {
				t.Errorf("ReasoningTier2Model(%q) = %q, expected %q", tc.coordinatorModel, result, tc.expected)
			}
		})
	}
}
