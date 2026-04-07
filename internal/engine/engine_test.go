package engine

import (
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name          string
		cfg           Config
		expectedProv  string
		expectedModel string
	}{
		{
			name:          "default",
			cfg:           Config{},
			expectedProv:  "google",
			expectedModel: "gemini-3.1-flash",
		},
		{
			name: "openai_default_model",
			cfg: Config{
				Provider: "openai",
			},
			expectedProv:  "openai",
			expectedModel: "gpt-5.4-mini",
		},
		{
			name: "anthropic_default_model",
			cfg: Config{
				Provider: "anthropic",
			},
			expectedProv:  "anthropic",
			expectedModel: "claude-4.6-sonnet",
		},
		{
			name: "acp_default_model",
			cfg: Config{
				Provider: "acp",
			},
			expectedProv:  "acp",
			expectedModel: "claude-4.6-sonnet",
		},
		{
			name: "custom_model",
			cfg: Config{
				Provider:  "openai",
				ModelName: "gpt-4-turbo",
			},
			expectedProv:  "openai",
			expectedModel: "gpt-4-turbo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(tt.cfg)
			if e.cfg.Provider != tt.expectedProv {
				t.Errorf("expected provider %s, got %s", tt.expectedProv, e.cfg.Provider)
			}
			if e.cfg.ModelName != tt.expectedModel {
				t.Errorf("expected model %s, got %s", tt.expectedModel, e.cfg.ModelName)
			}
		})
	}
}
