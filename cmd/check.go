package cmd

import (
	"fmt"
	"os"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check <file>",
	Short: "Analyze a configuration file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}

		feature := cisco_ios_jinja2.New()
		defer feature.Close()

		doc := document.New(path, "cisco_ios_jinja2", 0, content)
		diagnostics, err := feature.DidOpen(cmd.Context(), doc, nil)
		if err != nil {
			return fmt.Errorf("parsing file: %w", err)
		}

		out := cmd.OutOrStdout()
		if len(diagnostics) == 0 {
			fmt.Fprintln(out, "No issues found.")
			return nil
		}

		// Stable, machine-parseable "file:line:col: message" format. The LSP
		// Diagnostic.Source ("chunt") is meaningful over the protocol but is
		// redundant noise here — the user already knows they invoked chunt.
		// (chunt-lto)
		for _, d := range diagnostics {
			fmt.Fprintf(out, "%s:%d:%d: %s\n",
				path,
				d.Range.Start.Line+1,
				d.Range.Start.Character+1,
				d.Message,
			)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(checkCmd)
}
