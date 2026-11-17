package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check <file|directory>",
	Short: "Analyze a configuration file, or every template in a directory as one logical device config",
	Long: `Analyze a configuration file, or every template in a directory as one logical device config.

A single file is checked with its directory siblings as context: the other
*.ios.j2 templates in the same directory are loaded (read-only, from disk)
and cross-file rules apply — references resolve across the group, and
conflicting definitions in the same logical section (e.g. two different
router-id values under the same "router bgp" section, in different files)
are reported. Diagnostics anchored in the checked file are printed; pass the
directory to see every file's diagnostics.

A directory checks every *.ios.j2 file it contains (non-recursive) as one
logical device config.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		st, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("reading path: %w", err)
		}

		var targets []string // files to open (and report), in output order
		if st.IsDir() {
			matches, err := filepath.Glob(filepath.Join(path, "*.ios.j2"))
			if err != nil {
				return fmt.Errorf("listing directory: %w", err)
			}
			sort.Strings(matches)
			targets = matches
		} else {
			targets = []string{path}
		}

		feature := cisco_ios_jinja2.New()
		defer feature.Close()

		// Each target is opened with a canonical file:// URI so the
		// feature's directory scan treats these files and its on-disk
		// siblings uniformly. The returned set is the full set for that
		// file (its own tiers plus cross-file diagnostics anchored in it);
		// in directory mode every file is opened in turn, so every anchored
		// diagnostic is reported exactly once.
		total := 0
		out := cmd.OutOrStdout()
		for _, p := range targets {
			content, err := os.ReadFile(p)
			if err != nil {
				return fmt.Errorf("reading file: %w", err)
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				abs = p
			}
			doc := document.New("file://"+abs, "cisco_ios_jinja2", 0, content)
			diagnostics, err := feature.DidOpen(cmd.Context(), doc, nil)
			if err != nil {
				return fmt.Errorf("parsing file: %w", err)
			}
			for _, d := range diagnostics {
				// Stable, machine-parseable "file:line:col: message" format.
				// The LSP Diagnostic.Source ("chunt") is meaningful over the
				// protocol but is redundant noise here — the user already
				// knows they invoked chunt. (chunt-lto)
				fmt.Fprintf(out, "%s:%d:%d: %s\n",
					p,
					d.Range.Start.Line+1,
					d.Range.Start.Character+1,
					d.Message,
				)
				total++
			}
		}
		if total == 0 {
			fmt.Fprintln(out, "No issues found.")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(checkCmd)
}
