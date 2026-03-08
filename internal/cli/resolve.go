package cli

import (
	"context"
	"github.com/tsaarni/gitunstuck/internal/engine"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func NewResolveCmd() *cobra.Command {
	var (
		cfg     engine.Config
		timeout string
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
	resolveCmd.Flags().StringVar(&timeout, "timeout", "10m", "Maximum time allowed for the resolution process")

	return resolveCmd
}
