package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/dgethings/chunt/internal/logger"
)

var logLevel string

var rootCmd = &cobra.Command{
	Use:   "chunt",
	Short: "Language server for network operating systems",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var lvl slog.Level
		if err := lvl.UnmarshalText([]byte(logLevel)); err != nil {
			return fmt.Errorf("invalid log level %q: %w", logLevel, err)
		}
		lv := new(slog.LevelVar)
		lv.Set(lvl)

		logger.SetLogger(lv)
		return nil
	},
}

var Version string

func init() {
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level")
}

func Execute() error {
	return rootCmd.Execute()
}
