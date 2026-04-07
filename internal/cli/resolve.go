package cli

import (
	"context"
	"github.com/tsaarni/gitunstuck/internal/engine"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/adk/session"
)

type stateInjectingSessionService struct {
	session.Service
	initialState map[string]any
}

func (s *stateInjectingSessionService) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	if req.State == nil {
		req.State = make(map[string]any)
	}
	for k, v := range s.initialState {
		req.State[k] = v
	}
	return s.Service.Create(ctx, req)
}

func NewResolveCmd() *cobra.Command {
	var (
		cfg     engine.Config
		timeout string
		webAddr string
	)

	resolveCmd := &cobra.Command{
		Use:   "resolve",
		Short: "Resolve merge conflicts using AI",
		Run: func(cmd *cobra.Command, args []string) {
			duration, err := time.ParseDuration(timeout)
			if err != nil {
				slog.Error("Invalid timeout duration", "error", err)
				os.Exit(1)
			}

			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()

			var envVar string

			if cmd.Flags().Changed("acp-command") && !cmd.Flags().Changed("provider") {
				cfg.Provider = "acp"
			}

			if cfg.Provider != "acp" {
				switch cfg.Provider {
				case "openai":
					envVar = "OPENAI_API_KEY"
					cfg.APIKey = os.Getenv(envVar)
				case "anthropic":
					envVar = "ANTHROPIC_API_KEY"
					cfg.APIKey = os.Getenv(envVar)
				default:
					envVar = "GOOGLE_API_KEY"
					cfg.APIKey = os.Getenv(envVar)
				}

				if cfg.APIKey == "" {
					slog.Error("API key not set", "provider", cfg.Provider, "env_var", envVar)
					os.Exit(1)
				}
			}

			e := engine.New(cfg)

			if cmd.Flags().Changed("web") {
				slog.Info("Starting ADK Web UI on http://" + webAddr)
				if err := e.Prepare(ctx); err != nil {
					slog.Error("Engine preparation failed", "error", err)
					os.Exit(1)
				}
				rootAgent, err := e.GetRootAgent(ctx)
				if err != nil {
					slog.Error("Failed to get root agent", "error", err)
					os.Exit(1)
				}

				initialState, err := e.GetInitialState()
				if err != nil {
					slog.Error("Failed to get initial state", "error", err)
					os.Exit(1)
				}

				l := full.NewLauncher()
				cfg := &launcher.Config{
					AgentLoader:     agent.NewSingleLoader(rootAgent),
					SessionService:  &stateInjectingSessionService{e.SessionSvc(), initialState},
					ArtifactService: e.ArtifactSvc(),
				}

				_, port, err := net.SplitHostPort(webAddr)
				if err != nil {
					// Fallback: If not host:port, it might be just port
					port = webAddr
				}

				args := []string{
					"web", "-port", port,
					"api", "-webui_address", webAddr,
					"webui", "-api_server_address", "http://" + webAddr + "/api",
				}

				if err := l.Execute(ctx, cfg, args); err != nil {
					slog.Error("Web launcher failed", "error", err)
					os.Exit(1)
				}
				return
			}

			if err := e.Run(ctx); err != nil {
				slog.Error("Engine failed", "error", err)
				os.Exit(1)
			}
		},
	}

	resolveCmd.Flags().StringVar(&cfg.BuildCommand, "build-cmd", "", "Command to build the software")
	resolveCmd.Flags().StringVar(&cfg.TestCommand, "test-cmd", "", "Command to run tests")
	resolveCmd.Flags().StringVar(&cfg.WorkingDir, "working-dir", "", "Working directory for the resolution process")
	resolveCmd.Flags().StringVar(&cfg.Provider, "provider", "google", "LLM provider (google, openai, anthropic, acp)")
	resolveCmd.Flags().StringVar(&cfg.ModelName, "model", "", "LLM model name")
	resolveCmd.Flags().StringVar(&cfg.ACPCommand, "acp-command", "", "Command to run the ACP agent (e.g. 'kiro-cli acp')")
	resolveCmd.Flags().UintVar(&cfg.MaxIterations, "max-iterations", 5, "Maximum number of iterations for build fixes")
	resolveCmd.Flags().Int32Var(&cfg.MaxOutputTokens, "max-output-tokens", 8192, "Maximum tokens generated in a single response")
	resolveCmd.Flags().IntVar(&cfg.MaxHistoryItems, "max-history", 40, "Maximum number of items to keep in conversation history. An 'item' is a single message (e.g., one user prompt, one agent response, or one tool result).")
	resolveCmd.Flags().StringVar(&timeout, "timeout", "10m", "Maximum time allowed for the resolution process")
	resolveCmd.Flags().StringVar(&webAddr, "web", "localhost:8080", "Start ADK Web UI for debugging and interaction. Optional address: --web=localhost:9980")
	resolveCmd.Flags().Lookup("web").NoOptDefVal = "localhost:8080"

	return resolveCmd
}
