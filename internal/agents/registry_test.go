package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAgentFromConfig(t *testing.T) {
	ctx := context.Background()
	
	// When running tests, we are in internal/agents.
	// ConfigFS includes config/*.yaml relative to the registry.go file.
	path := "config/root.yaml"
	
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not found at %s: %v", path, err)
	}

	// SequentialAgent (Root) can be loaded without a default model if its sub-agents don't need it yet.
	// But our factory resolves sub-agents immediately, and they are LLM agents which need a model.
	
	// So we should see an error if we don't provide a model.
	_, err := LoadAgentFromConfig(ctx, path, nil)
	if err == nil {
		t.Errorf("expected error when loading root agent (with LLM sub-agents) without model, got nil")
	}

	// We can't easily mock model.LLM here without importing model package,
	// let's just verify that individual workflow agents (that don't need models themselves) can load.
	
	_, err = LoadAgentFromConfig(ctx, filepath.Join("config", "fixer_loop.yaml"), nil)
	// fixer_loop also resolves fixer.yaml immediately, which needs a model.
	if err == nil {
		t.Errorf("expected error when loading fixer_loop without model, got nil")
	}
}
