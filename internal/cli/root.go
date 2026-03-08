package cli

import (
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func NewRootCmd() *cobra.Command {
	var logLevel string

	rootCmd := &cobra.Command{
		Use:   "gitunstuck",
		Short: "AI-powered Git merge conflict resolver",
		Long:  `gitunstuck automatically resolves merge conflicts using AI.`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			var level slog.Level
			switch strings.ToLower(logLevel) {
			case "debug":
				level = slog.LevelDebug
			case "warn":
				level = slog.LevelWarn
			case "error":
				level = slog.LevelError
			default:
				level = slog.LevelInfo
			}

			slog.SetLogLoggerLevel(level)
		},
	}

	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Set the logging level (debug, info, warn, error)")

	rootCmd.AddCommand(NewResolveCmd())
	return rootCmd
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		slog.Error("failed to execute command", "error", err)
		os.Exit(1)
	}
}
