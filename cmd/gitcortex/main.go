package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gitcortex/internal/extract"

	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "gitcortex",
		Short: "Git metrics extraction and analysis",
	}

	rootCmd.AddCommand(extractCmd())
	rootCmd.AddCommand(statsCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func extractCmd() *cobra.Command {
	var cfg extract.Config

	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract commit data from a Git repository into JSONL",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath, err := filepath.Abs(cfg.Repo)
			if err != nil {
				return fmt.Errorf("resolve repo path: %w", err)
			}
			cfg.Repo = repoPath

			if cfg.CommandTimeout == 0 {
				cfg.CommandTimeout = extract.DefaultCommandTimeout
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return extract.Run(ctx, cfg)
		},
	}

	cmd.Flags().StringVar(&cfg.Repo, "repo", ".", "Path to the Git repository")
	cmd.Flags().IntVar(&cfg.BatchSize, "batch-size", 1000, "Number of commits to process per batch")
	cmd.Flags().StringVar(&cfg.Output, "output", "git_data.jsonl", "Output JSONL file path")
	cmd.Flags().StringVar(&cfg.StateFile, "state-file", "git_state", "File to persist worker state")
	cmd.Flags().IntVar(&cfg.StartOffset, "start-offset", -1, "Number of commits to skip before processing")
	cmd.Flags().StringVar(&cfg.StartSHA, "start-sha", "", "Last processed commit SHA to resume after")
	cmd.Flags().StringVar(&cfg.Branch, "branch", "main", "Branch or ref to traverse")
	cmd.Flags().BoolVar(&cfg.IncludeMessages, "include-commit-messages", false, "Include commit messages in output")
	cmd.Flags().DurationVar(&cfg.CommandTimeout, "command-timeout", extract.DefaultCommandTimeout, "Maximum duration for git commands")
	cmd.Flags().BoolVar(&cfg.FirstParent, "first-parent", false, "Restrict to first-parent chain")
	cmd.Flags().IntVar(&cfg.DiscardWarnLimit, "discard-warn-limit", 20, "Max ignored entries before summarizing")
	cmd.Flags().BoolVar(&cfg.DiscardError, "discard-error", false, "Fail when ignored entries exceed warn limit")
	cmd.Flags().BoolVar(&cfg.Debug, "debug", false, "Enable verbose debug logging")

	return cmd
}

func statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Generate statistics from extracted JSONL data",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("stats command not yet implemented")
			return nil
		},
	}
}
