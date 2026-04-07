package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/tsaarni/gitunstuck/internal/agents"
	"github.com/tsaarni/gitunstuck/internal/git"
	"github.com/tsaarni/gitunstuck/internal/model/acp"
	"github.com/tsaarni/gitunstuck/internal/tools"

	genaianthropic "github.com/achetronic/adk-utils-go/genai/anthropic"
	genaiopenai "github.com/achetronic/adk-utils-go/genai/openai"
	"google.golang.org/adk/agent"
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
	// MaxHistoryItems is the maximum number of items in the conversation history.
	MaxHistoryItems int
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
			cfg.ModelName = "gpt-5.4-mini"
		case "anthropic":
			cfg.ModelName = "claude-4.6-sonnet"
		case "acp":
			cfg.ModelName = "claude-4.6-sonnet"
		case "google":
			// alternative
			// cfg.ModelName = gemini-3-flash-preview
			cfg.ModelName = "gemini-2.5-flash"
		default:
		}
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = 8192
	}
	if cfg.MaxHistoryItems == 0 {
		cfg.MaxHistoryItems = 40
	}
	return &Engine{
		cfg:       cfg,
		gitClient: git.NewClient(cfg.WorkingDir),
	}
}

func (e *Engine) GetInitialState() (map[string]any, error) {
	mergeCtx, err := e.gitClient.GetMergeContext()
	if err != nil {
		return nil, fmt.Errorf("no active merge or rebase context found: %w", err)
	}
	if len(mergeCtx.Files) == 0 {
		return nil, fmt.Errorf("no unmerged files found")
	}

	worktreeDiff, _ := e.gitClient.DiffHEADStat()
	return map[string]any{
		"operation":          mergeCtx.Type,
		"conflicted_files":   strings.Join(mergeCtx.Files, "\n"),
		"base":               mergeCtx.Base,
		"local_diff":         mergeCtx.LocalDiff,
		"local_logs":         strings.Join(mergeCtx.Local, "\n"),
		"incoming_diff":      mergeCtx.IncomingDiff,
		"incoming_logs":      strings.Join(mergeCtx.Incoming, "\n"),
		"incoming_label":     mergeCtx.IncomingLabel,
		"summarizer_summary": "No intention summary available.",
		"fix_history":        "",
		"max_history_items":  e.cfg.MaxHistoryItems,
		"worktree_diff":      worktreeDiff,
	}, nil
}

// Run executes the full conflict resolution and build verification workflow.
func (e *Engine) Run(ctx context.Context) error {
	// 1. Initial State Check
	initialState, err := e.GetInitialState()
	if err != nil {
		return err
	}

	// 2. Prepare Environment
	if err := e.Prepare(ctx); err != nil {
		return err
	}

	// 3. Register Context Variables
	if _, err := e.sessionSvc.Create(ctx, &session.CreateRequest{
		AppName:   "gitunstuck",
		UserID:    "user",
		SessionID: "main",
		State:     initialState,
	}); err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// 4. Load Root Agent from YAML
	slog.Info("Loading Root Agent from YAML")
	rootAgent, err := e.GetRootAgent(ctx)
	if err != nil {
		return fmt.Errorf("failed to load root agent: %w", err)
	}

	// 5. Execute Workflow
	slog.Info("Starting GitUnstuck Workflow")
	if err := e.executeStep(ctx, "main", rootAgent, "Please resolve the merge conflicts and ensure the build is green."); err != nil {
		return fmt.Errorf("workflow execution failed: %w", err)
	}

	slog.Info("Resolution process completed.")
	return nil
}

// SessionSvc returns the engine's session service.
func (e *Engine) SessionSvc() session.Service {
	return e.sessionSvc
}

// ArtifactSvc returns the engine's artifact service.
func (e *Engine) ArtifactSvc() artifact.Service {
	return e.artifactSvc
}

// Prepare initializes dependencies like the LLM and services.
func (e *Engine) Prepare(ctx context.Context) error {
	if e.llm != nil {
		return nil
	}
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

	// Initialize global agents config
	agents.DefaultMaxHistoryItems = e.cfg.MaxHistoryItems

	return nil
}

// GetRootAgent loads and returns the primary agent.
func (e *Engine) GetRootAgent(ctx context.Context) (agent.Agent, error) {
	configPath := "config/root.yaml"
	return agents.LoadAgentFromConfig(ctx, configPath, e.llm)
}

func (e *Engine) prepare(ctx context.Context) error {
	return e.Prepare(ctx)
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
		if e.cfg.APIKey == "" && os.Getenv("GOOGLE_API_KEY") == "" {
			return nil, fmt.Errorf("API Key not set for google")
		}
		genaiCfg := &genai.ClientConfig{
			APIKey:  e.cfg.APIKey,
			Backend: genai.BackendGeminiAPI,
		}
		if genaiCfg.APIKey == "" {
			genaiCfg.APIKey = os.Getenv("GOOGLE_API_KEY")
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
	// Session should already exist (created in Run)
	if _, err := e.sessionSvc.Get(ctx, &session.GetRequest{AppName: "gitunstuck", UserID: "user", SessionID: sessionID}); err != nil {
		if _, err := e.sessionSvc.Create(ctx, &session.CreateRequest{AppName: "gitunstuck", UserID: "user", SessionID: sessionID}); err != nil {
			return err
		}
	}

	// Create Runner with Retry plugin
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
