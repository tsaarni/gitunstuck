package agents

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/tsaarni/gitunstuck/internal/tools"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/exitlooptool"
	"google.golang.org/genai"
	"gopkg.in/yaml.v3"
)

//go:embed config/*.yaml
var ConfigFS embed.FS

// This registry logic mirrors google.golang.org/adk/internal/configurable
// to provide YAML loading support since the original is internal.

type AgentFactory func(ctx context.Context, data []byte, path string, defaultModel model.LLM) (agent.Agent, error)
type ToolFactory func(ctx context.Context, args map[string]any) (tool.Tool, error)

var (
	registryMu       sync.RWMutex
	agentFactories   = make(map[string]AgentFactory)
	toolFactories    = make(map[string]ToolFactory)
	callbackRegistry = make(map[string]any)
)

func init() {
	RegisterAgentFactory("LlmAgent", NewLLMAgentFactory)
	RegisterAgentFactory("LoopAgent", NewLoopAgentFactory)
	RegisterAgentFactory("SequentialAgent", NewSequentialAgentFactory)
	RegisterAgentFactory("ParallelAgent", NewParallelAgentFactory)

	// Register GitUnstuck tools
	RegisterTool("git", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return tools.NewGitTool(), nil
	})
	RegisterTool("git_merge_3way", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return tools.NewGitMerge3WayTool(), nil
	})
	RegisterTool("git_merge_context", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return tools.NewGitMergeContextTool(), nil
	})
	RegisterTool("file_read", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return tools.NewFileReadTool(), nil
	})
	RegisterTool("file_edit_regex", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return tools.NewFileEditRegexTool(), nil
	})
	RegisterTool("file_edit_lines", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return tools.NewFileEditLinesTool(), nil
	})
	RegisterTool("file_write", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return tools.NewFileWriteTool(), nil
	})
	RegisterTool("file_create", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return tools.NewFileCreateTool(), nil
	})
	RegisterTool("project_build", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return tools.NewProjectBuildTool(), nil
	})
	RegisterTool("project_test", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return tools.NewProjectTestTool(), nil
	})
	RegisterTool("exit_loop", func(ctx context.Context, args map[string]any) (tool.Tool, error) {
		return exitlooptool.New()
	})

	// Register Callbacks
	RegisterCallbackValue("PruneHistory", llmagent.BeforeModelCallback(PruneHistory))
	RegisterCallbackValue("LogTokenUsage", llmagent.AfterModelCallback(LogTokenUsage))
	RegisterCallbackValue("UpdateFixHistory", agent.AfterAgentCallback(UpdateFixHistory))
	RegisterCallbackValue("SaveSummarizerOutput", agent.AfterAgentCallback(SaveSummarizerOutput))
}

func RegisterAgentFactory(name string, factory AgentFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	agentFactories[name] = factory
}

func RegisterTool(name string, factory ToolFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	toolFactories[name] = factory
}

func RegisterCallback(name string) {
	// For now, we just use the name to look up in our local map in common.go
}

func RegisterCallbackValue(name string, val any) {
	registryMu.Lock()
	defer registryMu.Unlock()
	callbackRegistry[name] = val
}

type baseConfig struct {
	AgentClass           string           `yaml:"agent_class"`
	Name                 string           `yaml:"name"`
	Description          string           `yaml:"description"`
	SubAgents            []agentRefConfig `yaml:"sub_agents"`
	BeforeAgentCallbacks []codeConfig     `yaml:"before_agent_callbacks"`
	AfterAgentCallbacks  []codeConfig     `yaml:"after_agent_callbacks"`
}

type agentRefConfig struct {
	ConfigPath string `yaml:"config_path"`
}

type codeConfig struct {
	Name string `yaml:"name"`
}

type toolConfig struct {
	Name string         `yaml:"name"`
	Args map[string]any `yaml:"args"`
}

type llmAgentConfig struct {
	baseConfig            `yaml:",inline"`
	Model                 string                        `yaml:"model"`
	Instruction           string                        `yaml:"instruction"`
	Tools                 []toolConfig                  `yaml:"tools"`
	BeforeModelCallbacks  []codeConfig                  `yaml:"before_model_callbacks"`
	AfterModelCallbacks   []codeConfig                  `yaml:"after_model_callbacks"`
	GenerateContentConfig *genai.GenerateContentConfig `yaml:"generate_content_config"`
}

type loopAgentConfig struct {
	baseConfig    `yaml:",inline"`
	MaxIterations uint `yaml:"max_iterations"`
}

func LoadAgentFromConfig(ctx context.Context, path string, defaultModel model.LLM) (agent.Agent, error) {
	data, err := ConfigFS.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var base baseConfig
	if err := yaml.Unmarshal(data, &base); err != nil {
		return nil, err
	}

	class := base.AgentClass
	if class == "" {
		class = "LlmAgent"
	}

	registryMu.RLock()
	factory, ok := agentFactories[class]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown agent class: %s", class)
	}

	return factory(ctx, data, path, defaultModel)
}

func NewLLMAgentFactory(ctx context.Context, data []byte, path string, defaultModel model.LLM) (agent.Agent, error) {
	var cfg llmAgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	modelObj := defaultModel
	if cfg.Model != "" {
		var err error
		modelObj, err = gemini.NewModel(ctx, cfg.Model, &genai.ClientConfig{
			APIKey: os.Getenv("GOOGLE_API_KEY"),
		})
		if err != nil {
			return nil, err
		}
	}

	if modelObj == nil {
		return nil, fmt.Errorf("no model provided for LLM agent %s", cfg.Name)
	}

	subAgents, err := resolveSubAgents(ctx, path, cfg.SubAgents, defaultModel)
	if err != nil {
		return nil, err
	}

	tools, err := resolveTools(ctx, cfg.Tools)
	if err != nil {
		return nil, err
	}

	before, err := resolveCallbacks[agent.BeforeAgentCallback](cfg.BeforeAgentCallbacks)
	if err != nil {
		return nil, err
	}

	after, err := resolveCallbacks[agent.AfterAgentCallback](cfg.AfterAgentCallbacks)
	if err != nil {
		return nil, err
	}

	beforeModel, err := resolveCallbacks[llmagent.BeforeModelCallback](cfg.BeforeModelCallbacks)
	if err != nil {
		return nil, err
	}

	afterModel, err := resolveCallbacks[llmagent.AfterModelCallback](cfg.AfterModelCallbacks)
	if err != nil {
		return nil, err
	}

	return llmagent.New(llmagent.Config{
		Name:                 cfg.Name,
		Description:          cfg.Description,
		SubAgents:            subAgents,
		Model:                modelObj,
		Instruction:          cfg.Instruction,
		Tools:                tools,
		GenerateContentConfig: cfg.GenerateContentConfig,
		BeforeAgentCallbacks: before,
		AfterAgentCallbacks:  after,
		BeforeModelCallbacks: beforeModel,
		AfterModelCallbacks:  afterModel,
	})
}

func NewLoopAgentFactory(ctx context.Context, data []byte, path string, defaultModel model.LLM) (agent.Agent, error) {
	var cfg loopAgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	subAgents, err := resolveSubAgents(ctx, path, cfg.SubAgents, defaultModel)
	if err != nil {
		return nil, err
	}

	return loopagent.New(loopagent.Config{
		AgentConfig: agent.Config{
			Name:        cfg.Name,
			Description: cfg.Description,
			SubAgents:   subAgents,
		},
		MaxIterations: cfg.MaxIterations,
	})
}

func NewSequentialAgentFactory(ctx context.Context, data []byte, path string, defaultModel model.LLM) (agent.Agent, error) {
	var cfg baseConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	subAgents, err := resolveSubAgents(ctx, path, cfg.SubAgents, defaultModel)
	if err != nil {
		return nil, err
	}

	return sequentialagent.New(sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        cfg.Name,
			Description: cfg.Description,
			SubAgents:   subAgents,
		},
	})
}

func NewParallelAgentFactory(ctx context.Context, data []byte, path string, defaultModel model.LLM) (agent.Agent, error) {
	var cfg baseConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	subAgents, err := resolveSubAgents(ctx, path, cfg.SubAgents, defaultModel)
	if err != nil {
		return nil, err
	}

	return parallelagent.New(parallelagent.Config{
		AgentConfig: agent.Config{
			Name:        cfg.Name,
			Description: cfg.Description,
			SubAgents:   subAgents,
		},
	})
}

func resolveSubAgents(ctx context.Context, parentPath string, refs []agentRefConfig, defaultModel model.LLM) ([]agent.Agent, error) {
	var agents []agent.Agent
	for _, ref := range refs {
		path := ref.ConfigPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(parentPath), path)
		}
		// When using embed.FS, paths must not have leading slash or start with '.'
		// filepath.Join might produce "config/sub.yaml" which is correct for ReadFile.
		a, err := LoadAgentFromConfig(ctx, path, defaultModel)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, nil
}

func resolveTools(ctx context.Context, configs []toolConfig) ([]tool.Tool, error) {
	var tools []tool.Tool
	for _, tc := range configs {
		registryMu.RLock()
		factory, ok := toolFactories[tc.Name]
		registryMu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("unknown tool: %s", tc.Name)
		}
		t, err := factory(ctx, tc.Args)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, nil
}

func resolveCallbacks[T any](configs []codeConfig) ([]T, error) {
	var cbs []T
	for _, c := range configs {
		registryMu.RLock()
		val, ok := callbackRegistry[c.Name]
		registryMu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("unknown callback: %s", c.Name)
		}
		cb, ok := val.(T)
		if !ok {
			return nil, fmt.Errorf("callback %s has wrong type", c.Name)
		}
		cbs = append(cbs, cb)
	}
	return cbs, nil
}
