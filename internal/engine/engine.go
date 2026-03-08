package engine

import (
	"context"
	"fmt"
	"github.com/tsaarni/gitunstuck/internal/agents"
	"github.com/tsaarni/gitunstuck/internal/git"
	"github.com/tsaarni/gitunstuck/internal/model/acp"
	"github.com/tsaarni/gitunstuck/internal/tools"
	"log/slog"
	"os"

	genaianthropic "github.com/achetronic/adk-utils-go/genai/anthropic"
	genaiopenai "github.com/achetronic/adk-utils-go/genai/openai"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/plugin/loggingplugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Config defines the configuration for the Engine.
type Config struct {
	// BuildCommand is the command used to build the project (e.g., "go build").
	BuildCommand string
	// TestCommand is the command used to run tests (e.g., "go test ./...").
	TestCommand string
	// WorkingDir is the directory where git and build commands are executed.
	WorkingDir string
	// Provider is the LLM provider name (e.g., "google", "openai", "anthropic", "acp").
	Provider string
	// ModelName is the specific model to use (e.g., "gemini-3.1-flash-lite-preview", "gpt-5-mini").
	ModelName string
	// APIKey is the authentication key for the LLM provider.
	APIKey string
	// ACPCommand is the full command to run an ACP-compatible agent (e.g. "kiro-cli acp").
	ACPCommand string
	// MaxIterations is the maximum number of build/fix cycles allowed.
	MaxIterations uint
	// MaxOutputTokens is the maximum number of tokens in the LLM response.
	MaxOutputTokens int32
}

// Engine manages the high-level workflow for resolving git conflicts
// and verifying the resulting state through automated build and test cycles.
type Engine struct {
	// cfg is the engine configuration.
	cfg Config
	// llm is the language model provider used by agents.
	llm model.LLM
	// gitClient provides access to the git repository.
	gitClient *git.Client
	// sessionSvc manages agent sessions, including message history and execution state.
	sessionSvc session.Service
	// artifactSvc manages files and metadata produced or used during the agent's workflow.
	artifactSvc artifact.Service
	// logger is the plugin used for logging workflow events.
	logger *plugin.Plugin
}

// New creates a new Engine with the provided configuration.
func New(cfg Config) *Engine {
	if cfg.WorkingDir == "" {
		cfg.WorkingDir, _ = os.Getwd()
	}
	if cfg.Provider == "" {
		cfg.Provider = "google"
	}
	if cfg.ModelName == "" {
		switch cfg.Provider {
		case "openai":
			cfg.ModelName = "gpt-5-mini"
		case "anthropic":
			cfg.ModelName = "claude-4-haiku"
		default:
			cfg.ModelName = "gemini-3.1-flash-lite-preview"
		}
	}
	return &Engine{
		cfg:       cfg,
		gitClient: &git.Client{BaseDir: cfg.WorkingDir},
	}
}

// Run executes the full conflict resolution and build verification workflow.
func (e *Engine) Run(ctx context.Context) error {
	// 1. Initial State Check
	mergeCtx, err := e.gitClient.GetMergeContext()
	if err != nil {
		return fmt.Errorf("no active merge or rebase context found: %w", err)
	}
	if len(mergeCtx.ConflictedFiles) == 0 {
		return fmt.Errorf("no unmerged files found")
	}

	// 2. Prepare Environment
	if err := e.prepare(ctx); err != nil {
		return err
	}

	// 3. Step 1: Conflict Resolution
	slog.Info("Starting Step 1: Conflict Resolution")
	agentCfg := agents.Config{
		Model:           e.llm,
		MaxOutputTokens: e.cfg.MaxOutputTokens,
		GitClient:       e.gitClient,
		MergeContext:    mergeCtx,
	}
	resolver, _ := agents.NewResolver(agentCfg)
	if err := e.executeStep(ctx, "session-resolve", resolver, "Please assess the unmerged files and resolve the conflicts."); err != nil {
		return fmt.Errorf("conflict resolution failed: %w", err)
	}

	// 4. Step 2: Build & Test Verification
	if e.cfg.BuildCommand != "" || e.cfg.TestCommand != "" {
		slog.Info("Starting Step 2: Build and Test Verification")
		fixer, _ := agents.NewFixer(agentCfg)
		loopingFixer, _ := loopagent.New(loopagent.Config{
			AgentConfig:   agent.Config{Name: "BuildFixerLoop", SubAgents: []agent.Agent{fixer}},
			MaxIterations: e.cfg.MaxIterations,
		})
		if err := e.executeStep(ctx, "session-fix", loopingFixer, "Please run the build, identify any errors, and fix them iteratively until the tests pass."); err != nil {
			return fmt.Errorf("build fixing failed: %w", err)
		}
	}

	slog.Info("Resolution process completed.")
	return nil
}

// prepare initializes dependencies like the LLM and services.
func (e *Engine) prepare(ctx context.Context) error {
	slog.Info("Preparing engine",
		"provider", e.cfg.Provider,
		"model", e.cfg.ModelName,
		"working_dir", e.cfg.WorkingDir,
		"build_cmd", e.cfg.BuildCommand,
		"test_cmd", e.cfg.TestCommand,
	)
	var err error
	if e.llm, err = e.setupLLM(ctx); err != nil {
		return err
	}
	e.sessionSvc, e.artifactSvc = e.setupServices()
	e.logger = loggingplugin.MustNew("gitunstuck-log")

	// Initialize global tools config
	tools.BuildCommand = e.cfg.BuildCommand
	tools.TestCommand = e.cfg.TestCommand
	tools.WorkingDir = e.cfg.WorkingDir

	return nil
}

// setupLLM initializes the LLM based on configuration
func (e *Engine) setupLLM(ctx context.Context) (model.LLM, error) {
	var llm model.LLM
	var err error
	switch e.cfg.Provider {
	case "acp":
		if e.cfg.ACPCommand == "" {
			return nil, fmt.Errorf("ACP command not set")
		}
		llm = acp.New(acp.Config{
			Command:         e.cfg.ACPCommand,
			WorkingDir:      e.cfg.WorkingDir,
			ModelName:       e.cfg.ModelName,
			MaxOutputTokens: e.cfg.MaxOutputTokens,
		})
	case "openai":
		if e.cfg.APIKey == "" {
			return nil, fmt.Errorf("API Key not set for openai")
		}
		llm = genaiopenai.New(genaiopenai.Config{
			APIKey:    e.cfg.APIKey,
			ModelName: e.cfg.ModelName,
		})
	case "anthropic":
		if e.cfg.APIKey == "" {
			return nil, fmt.Errorf("API Key not set for anthropic")
		}
		llm = genaianthropic.New(genaianthropic.Config{
			APIKey:    e.cfg.APIKey,
			ModelName: e.cfg.ModelName,
		})
	default:
		if e.cfg.APIKey == "" {
			return nil, fmt.Errorf("API Key not set for google")
		}
		genaiCfg := &genai.ClientConfig{
			APIKey:  e.cfg.APIKey,
			Backend: genai.BackendGeminiAPI,
		}
		llm, err = gemini.NewModel(ctx, e.cfg.ModelName, genaiCfg)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}
	return llm, nil
}

// setupServices initializes the session and artifact services
func (e *Engine) setupServices() (session.Service, artifact.Service) {
	return session.InMemoryService(), artifact.InMemoryService()
}

// executeStep handles the lifecycle of an agent execution within a specific step.
func (e *Engine) executeStep(ctx context.Context, sessionID string, a agent.Agent, prompt string) error {
	// Create Session
	if _, err := e.sessionSvc.Create(ctx, &session.CreateRequest{AppName: "gitunstuck", UserID: "user", SessionID: sessionID}); err != nil {
		return err
	}

	// Create Runner
	r, err := runner.New(runner.Config{
		AppName: "gitunstuck", Agent: a, SessionService: e.sessionSvc, ArtifactService: e.artifactSvc,
		PluginConfig: runner.PluginConfig{Plugins: []*plugin.Plugin{e.logger}},
	})
	if err != nil {
		return err
	}

	// Run
	msg := &genai.Content{Parts: []*genai.Part{{Text: prompt}}, Role: "user"}
	for event, err := range r.Run(ctx, "user", sessionID, msg, agent.RunConfig{}) {
		if err != nil {
			return err
		}
		if event.Content != nil {
			for _, part := range event.Content.Parts {
				if part.Text != "" {
					fmt.Println(part.Text)
				}
			}
		}
	}
	return nil
}
